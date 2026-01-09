// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"go/types"

	"github.com/goplus/xgo/ast"
	"github.com/goplus/xgo/token"
	"github.com/goplus/xgo/x/typesutil"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/safetoken"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/internal/diff"
)

// gopExprAtPos returns the index of the expression containing pos.
func gopExprAtPos(pos token.Pos, args []ast.Expr) int {
	for i, expr := range args {
		if expr.Pos() <= pos && pos <= expr.End() {
			return i
		}
	}
	return len(args)
}

// gopPrevStmt returns the statement that precedes the statement containing pos.
// For example:
//
//	foo := 1
//	bar(1 + 2<>)
//
// If "<>" is pos, prevStmt returns "foo := 1"
func gopPrevStmt(pos token.Pos, path []ast.Node) ast.Stmt {
	var blockLines []ast.Stmt
	for i := 0; i < len(path) && blockLines == nil; i++ {
		switch n := path[i].(type) {
		case *ast.BlockStmt:
			blockLines = n.List
		case *ast.CommClause:
			blockLines = n.Body
		case *ast.CaseClause:
			blockLines = n.Body
		}
	}

	for i := len(blockLines) - 1; i >= 0; i-- {
		if blockLines[i].End() < pos {
			return blockLines[i]
		}
	}

	return nil
}

// gopExprObj returns the types.Object associated with the *ast.Ident or
// *ast.SelectorExpr e.
func gopExprObj(info *typesutil.Info, e ast.Expr) types.Object {
	var ident *ast.Ident
	switch expr := e.(type) {
	case *ast.Ident:
		ident = expr
	case *ast.SelectorExpr:
		ident = expr.Sel
	default:
		return nil
	}

	return info.ObjectOf(ident)
}

// gopTypeConversion returns the type being converted to if call is a type
// conversion expression.
func gopTypeConversion(call *ast.CallExpr, info *typesutil.Info) types.Type {
	// Type conversion (e.g. "float64(foo)").
	if fun, _ := gopExprObj(info, call.Fun).(*types.TypeName); fun != nil {
		return fun.Type()
	}

	return nil
}

// gopResolveInvalid traverses the node of the AST that defines the scope
// containing the declaration of obj, and attempts to find a user-friendly
// name for its invalid type. The resulting Object and its Type are fake.
func gopResolveInvalid(fset *token.FileSet, obj types.Object, node ast.Node, info *typesutil.Info) types.Object {
	var resultExpr ast.Expr
	ast.Inspect(node, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.ValueSpec:
			for _, name := range n.Names {
				if info.Defs[name] == obj {
					resultExpr = n.Type
				}
			}
			return false
		case *ast.Field: // This case handles parameters and results of a FuncDecl or FuncLit.
			for _, name := range n.Names {
				if info.Defs[name] == obj {
					resultExpr = n.Type
				}
			}
			return false
		default:
			return true
		}
	})
	// Construct a fake type for the object and return a fake object with this type.
	typename := source.GopFormatNode(fset, resultExpr)
	typ := types.NewNamed(types.NewTypeName(token.NoPos, obj.Pkg(), typename, nil), types.Typ[types.Invalid], nil)
	return types.NewVar(obj.Pos(), obj.Pkg(), obj.Name(), typ)
}

func (c *gopCompleter) editText(from, to token.Pos, newText string) ([]protocol.TextEdit, error) {
	start, end, err := safetoken.Offsets(c.tokFile, from, to)
	if err != nil {
		return nil, err // can't happen: from/to came from c
	}
	return source.ToProtocolEdits(c.mapper, []diff.Edit{{
		Start: start,
		End:   end,
		New:   newText,
	}})
}
