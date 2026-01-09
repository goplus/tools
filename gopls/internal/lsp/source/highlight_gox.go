// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"fmt"
	"go/types"
	"log"

	"github.com/goplus/xgo/ast"
	"github.com/goplus/xgo/token"
	"github.com/goplus/xgo/x/typesutil"
	"golang.org/x/tools/gop/ast/astutil"
	"golang.org/x/tools/gopls/internal/goxls"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/internal/event"
)

func GopHighlight(ctx context.Context, snapshot Snapshot, fh FileHandle, position protocol.Position) ([]protocol.Range, error) {
	ctx, done := event.Start(ctx, "source.GopHighlight")
	defer done()

	// We always want fully parsed files for highlight, regardless
	// of whether the file belongs to a workspace package.
	pkg, pgf, err := NarrowestPackageForGopFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, fmt.Errorf("getting package for GopHighlight: %w", err)
	}

	pos, err := pgf.PositionPos(position)
	if err != nil {
		return nil, err
	}
	path, _ := astutil.PathEnclosingInterval(pgf.File, pos, pos)
	if len(path) == 0 {
		return nil, fmt.Errorf("no enclosing position found for %v:%v", position.Line, position.Character)
	}
	if goxls.DbgHighlight {
		log.Println("GopHighlight PathEnclosingInterval:", path[0])
	}
	// If start == end for astutil.PathEnclosingInterval, the 1-char interval
	// following start is used instead. As a result, we might not get an exact
	// match so we should check the 1-char interval to the left of the passed
	// in position to see if that is an exact match.
	if _, ok := path[0].(*ast.Ident); !ok {
		if p, _ := astutil.PathEnclosingInterval(pgf.File, pos-1, pos-1); p != nil {
			switch p[0].(type) {
			case *ast.Ident, *ast.SelectorExpr:
				path = p // use preceding ident/selector
			}
		}
	}
	result, err := gopHighlightPath(path, pgf.File, pkg.GopTypesInfo())
	if err != nil {
		return nil, err
	}
	var ranges []protocol.Range
	for rng := range result {
		rng, err := pgf.PosRange(rng.start, rng.end)
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, rng)
	}
	return ranges, nil
}

func gopHighlightPath(path []ast.Node, file *ast.File, info *typesutil.Info) (map[posRange]struct{}, error) {
	result := make(map[posRange]struct{})
	switch node := path[0].(type) {
	case *ast.BasicLit:
		// Import path string literal?
		if len(path) > 1 {
			if imp, ok := path[1].(*ast.ImportSpec); ok {
				highlight := func(n ast.Node) {
					result[posRange{start: n.Pos(), end: n.End()}] = struct{}{}
				}

				// Highlight the import itself...
				highlight(imp)

				// ...and all references to it in the file.
				if pkgname, ok := GopImportedPkgName(info, imp); ok {
					ast.Inspect(file, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok &&
							info.Uses[id] == pkgname {
							highlight(id)
						}
						return true
					})
				}
				return result, nil
			}
		}
		gopHighlightFuncControlFlow(path, result)
	case *ast.ReturnStmt, *ast.FuncDecl, *ast.FuncType:
		gopHighlightFuncControlFlow(path, result)
	case *ast.Ident:
		// Check if ident is inside return or func decl.
		gopHighlightFuncControlFlow(path, result)
		gopHighlightIdentifier(node, file, info, result)
	case *ast.ForStmt, *ast.RangeStmt:
		gopHighlightLoopControlFlow(path, info, result)
	case *ast.SwitchStmt:
		gopHighlightSwitchFlow(path, info, result)
	case *ast.BranchStmt:
		// BREAK can exit a loop, switch or select, while CONTINUE exit a loop so
		// these need to be handled separately. They can also be embedded in any
		// other loop/switch/select if they have a label. TODO: add support for
		// GOTO and FALLTHROUGH as well.
		switch node.Tok {
		case token.BREAK:
			if node.Label != nil {
				gopHighlightLabeledFlow(path, info, node, result)
			} else {
				gopHighlightUnlabeledBreakFlow(path, info, result)
			}
		case token.CONTINUE:
			if node.Label != nil {
				gopHighlightLabeledFlow(path, info, node, result)
			} else {
				gopHighlightLoopControlFlow(path, info, result)
			}
		}
	default:
		// If the cursor is in an unidentified area, return empty results.
		return nil, nil
	}
	return result, nil
}

func gopHighlightFuncControlFlow(path []ast.Node, result map[posRange]struct{}) {
	var enclosingFunc ast.Node
	var returnStmt *ast.ReturnStmt
	var resultsList *ast.FieldList
	inReturnList := false

Outer:
	// Reverse walk the path till we get to the func block.
	for i, n := range path {
		switch node := n.(type) {
		case *ast.KeyValueExpr:
			// If cursor is in a key: value expr, we don't want control flow highlighting
			return
		case *ast.CallExpr:
			// If cursor is an arg in a callExpr, we don't want control flow highlighting.
			if i > 0 {
				for _, arg := range node.Args {
					if arg == path[i-1] {
						return
					}
				}
			}
		case *ast.Field:
			inReturnList = true
		case *ast.FuncLit:
			enclosingFunc = n
			resultsList = node.Type.Results
			break Outer
		case *ast.FuncDecl:
			enclosingFunc = n
			resultsList = node.Type.Results
			break Outer
		case *ast.ReturnStmt:
			returnStmt = node
			// If the cursor is not directly in a *ast.ReturnStmt, then
			// we need to know if it is within one of the values that is being returned.
			inReturnList = inReturnList || path[0] != returnStmt
		}
	}
	// Cursor is not in a function.
	if enclosingFunc == nil {
		return
	}
	// If the cursor is on a "return" or "func" keyword, we should highlight all of the exit
	// points of the function, including the "return" and "func" keywords.
	highlightAllReturnsAndFunc := path[0] == returnStmt || path[0] == enclosingFunc
	switch path[0].(type) {
	case *ast.Ident, *ast.BasicLit:
		// Cursor is in an identifier and not in a return statement or in the results list.
		if returnStmt == nil && !inReturnList {
			return
		}
	case *ast.FuncType:
		highlightAllReturnsAndFunc = true
	}
	// The user's cursor may be within the return statement of a function,
	// or within the result section of a function's signature.
	// index := -1
	var nodes []ast.Node
	if returnStmt != nil {
		for _, n := range returnStmt.Results {
			nodes = append(nodes, n)
		}
	} else if resultsList != nil {
		for _, n := range resultsList.List {
			nodes = append(nodes, n)
		}
	}
	_, index := nodeAtPos(nodes, path[0].Pos())

	// Highlight the correct argument in the function declaration return types.
	if resultsList != nil && -1 < index && index < len(resultsList.List) {
		rng := posRange{
			start: resultsList.List[index].Pos(),
			end:   resultsList.List[index].End(),
		}
		result[rng] = struct{}{}
	}
	// Add the "func" part of the func declaration.
	if highlightAllReturnsAndFunc {
		r := posRange{
			start: enclosingFunc.Pos(),
			end:   enclosingFunc.Pos() + token.Pos(len("func")),
		}
		result[r] = struct{}{}
	}
	ast.Inspect(enclosingFunc, func(n ast.Node) bool {
		// Don't traverse any other functions.
		switch n.(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			return enclosingFunc == n
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		var toAdd ast.Node
		// Add the entire return statement, applies when highlight the word "return" or "func".
		if highlightAllReturnsAndFunc {
			toAdd = n
		}
		// Add the relevant field within the entire return statement.
		if -1 < index && index < len(ret.Results) {
			toAdd = ret.Results[index]
		}
		if toAdd != nil {
			result[posRange{start: toAdd.Pos(), end: toAdd.End()}] = struct{}{}
		}
		return false
	})
}

// gopHighlightUnlabeledBreakFlow highlights the innermost enclosing for/range/switch or swlect
func gopHighlightUnlabeledBreakFlow(path []ast.Node, info *typesutil.Info, result map[posRange]struct{}) {
	// Reverse walk the path until we find closest loop, select, or switch.
	for _, n := range path {
		switch n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			gopHighlightLoopControlFlow(path, info, result)
			return // only highlight the innermost statement
		case *ast.SwitchStmt:
			gopHighlightSwitchFlow(path, info, result)
			return
		case *ast.SelectStmt:
			// TODO: add highlight when breaking a select.
			return
		}
	}
}

// gopHighlightLabeledFlow highlights the enclosing labeled for, range,
// or switch statement denoted by a labeled break or continue stmt.
func gopHighlightLabeledFlow(path []ast.Node, info *typesutil.Info, stmt *ast.BranchStmt, result map[posRange]struct{}) {
	use := info.Uses[stmt.Label]
	if use == nil {
		return
	}
	for _, n := range path {
		if label, ok := n.(*ast.LabeledStmt); ok && info.Defs[label.Label] == use {
			switch label.Stmt.(type) {
			case *ast.ForStmt, *ast.RangeStmt:
				gopHighlightLoopControlFlow([]ast.Node{label.Stmt, label}, info, result)
			case *ast.SwitchStmt:
				gopHighlightSwitchFlow([]ast.Node{label.Stmt, label}, info, result)
			}
			return
		}
	}
}

func gopLabelFor(path []ast.Node) *ast.Ident {
	if len(path) > 1 {
		if n, ok := path[1].(*ast.LabeledStmt); ok {
			return n.Label
		}
	}
	return nil
}

func gopHighlightLoopControlFlow(path []ast.Node, info *typesutil.Info, result map[posRange]struct{}) {
	var loop ast.Node
	var loopLabel *ast.Ident
	stmtLabel := gopLabelFor(path)
Outer:
	// Reverse walk the path till we get to the for loop.
	for i := range path {
		switch n := path[i].(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			loopLabel = gopLabelFor(path[i:])

			if stmtLabel == nil || loopLabel == stmtLabel {
				loop = n
				break Outer
			}
		}
	}
	if loop == nil {
		return
	}

	// Add the for statement.
	rng := posRange{
		start: loop.Pos(),
		end:   loop.Pos() + token.Pos(len("for")),
	}
	result[rng] = struct{}{}

	// Traverse AST to find branch statements within the same for-loop.
	ast.Inspect(loop, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return loop == n
		case *ast.SwitchStmt, *ast.SelectStmt:
			return false
		}
		b, ok := n.(*ast.BranchStmt)
		if !ok {
			return true
		}
		if b.Label == nil || info.Uses[b.Label] == info.Defs[loopLabel] {
			result[posRange{start: b.Pos(), end: b.End()}] = struct{}{}
		}
		return true
	})

	// Find continue statements in the same loop or switches/selects.
	ast.Inspect(loop, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return loop == n
		}

		if n, ok := n.(*ast.BranchStmt); ok && n.Tok == token.CONTINUE {
			result[posRange{start: n.Pos(), end: n.End()}] = struct{}{}
		}
		return true
	})

	// We don't need to check other for loops if we aren't looking for labeled statements.
	if loopLabel == nil {
		return
	}

	// Find labeled branch statements in any loop.
	ast.Inspect(loop, func(n ast.Node) bool {
		b, ok := n.(*ast.BranchStmt)
		if !ok {
			return true
		}
		// statement with labels that matches the loop
		if b.Label != nil && info.Uses[b.Label] == info.Defs[loopLabel] {
			result[posRange{start: b.Pos(), end: b.End()}] = struct{}{}
		}
		return true
	})
}

func gopHighlightSwitchFlow(path []ast.Node, info *typesutil.Info, result map[posRange]struct{}) {
	var switchNode ast.Node
	var switchNodeLabel *ast.Ident
	stmtLabel := gopLabelFor(path)
Outer:
	// Reverse walk the path till we get to the switch statement.
	for i := range path {
		switch n := path[i].(type) {
		case *ast.SwitchStmt:
			switchNodeLabel = gopLabelFor(path[i:])
			if stmtLabel == nil || switchNodeLabel == stmtLabel {
				switchNode = n
				break Outer
			}
		}
	}
	// Cursor is not in a switch statement
	if switchNode == nil {
		return
	}

	// Add the switch statement.
	rng := posRange{
		start: switchNode.Pos(),
		end:   switchNode.Pos() + token.Pos(len("switch")),
	}
	result[rng] = struct{}{}

	// Traverse AST to find break statements within the same switch.
	ast.Inspect(switchNode, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.SwitchStmt:
			return switchNode == n
		case *ast.ForStmt, *ast.RangeStmt, *ast.SelectStmt:
			return false
		}

		b, ok := n.(*ast.BranchStmt)
		if !ok || b.Tok != token.BREAK {
			return true
		}

		if b.Label == nil || info.Uses[b.Label] == info.Defs[switchNodeLabel] {
			result[posRange{start: b.Pos(), end: b.End()}] = struct{}{}
		}
		return true
	})

	// We don't need to check other switches if we aren't looking for labeled statements.
	if switchNodeLabel == nil {
		return
	}

	// Find labeled break statements in any switch
	ast.Inspect(switchNode, func(n ast.Node) bool {
		b, ok := n.(*ast.BranchStmt)
		if !ok || b.Tok != token.BREAK {
			return true
		}

		if b.Label != nil && info.Uses[b.Label] == info.Defs[switchNodeLabel] {
			result[posRange{start: b.Pos(), end: b.End()}] = struct{}{}
		}

		return true
	})
}

func gopHighlightIdentifier(id *ast.Ident, file *ast.File, info *typesutil.Info, result map[posRange]struct{}) {
	highlight := func(n ast.Node) {
		result[posRange{start: n.Pos(), end: n.End()}] = struct{}{}
	}

	// obj may be nil if the Ident is undefined.
	// In this case, the behavior expected by tests is
	// to match other undefined Idents of the same name.
	obj := info.ObjectOf(id)

	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			if n.Name == id.Name && info.ObjectOf(n) == obj {
				highlight(n)
			}

		case *ast.ImportSpec:
			pkgname, ok := GopImportedPkgName(info, n)
			if ok && pkgname == obj {
				if n.Name != nil {
					highlight(n.Name)
				} else {
					highlight(n)
				}
			}
		}
		return true
	})
}

// GopImportedPkgName returns the PkgName object declared by an ImportSpec.
// TODO(adonovan): make this a method of types.Info.
func GopImportedPkgName(info *typesutil.Info, imp *ast.ImportSpec) (*types.PkgName, bool) {
	var obj types.Object
	if imp.Name != nil {
		obj = info.Defs[imp.Name]
	} else {
		obj = info.Implicits[imp]
	}
	pkgname, ok := obj.(*types.PkgName)
	return pkgname, ok
}
