// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"context"
	"fmt"
	"go/token"
	"go/types"
	"log"
	"strings"

	"github.com/goplus/xgo/ast"
	"golang.org/x/tools/gopls/internal/goxls"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/safetoken"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/imports"
)

func (c *gopCompleter) addPostfixSnippetCandidates(ctx context.Context, sel *ast.SelectorExpr) {
	if !c.opts.postfix {
		return
	}

	initPostfixRules()

	if sel == nil || sel.Sel == nil {
		return
	}

	selType := c.pkg.GopTypesInfo().TypeOf(sel.X)
	if goxls.DbgCompletion {
		log.Println("gopCompleter.addPostfixSnippetCandidates selType:", selType, "c.path:", len(c.path))
	}
	if selType == nil {
		return
	}

	// Skip empty tuples since there is no value to operate on.
	if tuple, ok := selType.Underlying().(*types.Tuple); ok && tuple == nil {
		return
	}

	tokFile := c.pkg.FileSet().File(c.pos)

	// Only replace sel with a statement if sel is already a statement.
	var stmtOK bool
	for i, n := range c.path {
		if n == sel && i < len(c.path)-1 {
			switch p := c.path[i+1].(type) {
			case *ast.ExprStmt:
				stmtOK = true
			case *ast.AssignStmt:
				// In cases like:
				//
				//   foo.<>
				//   bar = 123
				//
				// detect that "foo." makes up the entire statement since the
				// apparent selector spans lines.
				stmtOK = safetoken.Line(tokFile, c.pos) < safetoken.Line(tokFile, p.TokPos)
			}
			break
		}
	}

	scope := c.pkg.GetTypes().Scope().Innermost(c.pos)
	if goxls.DbgCompletion {
		log.Println("gopCompleter.addPostfixSnippetCandidates Innermost:", scope, "c.pos:", c.pos)
	}
	if scope == nil {
		return
	}

	// afterDot is the position after selector dot, e.g. "|" in
	// "foo.|print".
	afterDot := sel.Sel.Pos()

	// We must detect dangling selectors such as:
	//
	//    foo.<>
	//    bar
	//
	// and adjust afterDot so that we don't mistakenly delete the
	// newline thinking "bar" is part of our selector.
	if startLine := safetoken.Line(tokFile, sel.Pos()); startLine != safetoken.Line(tokFile, afterDot) {
		if safetoken.Line(tokFile, c.pos) != startLine {
			return
		}
		afterDot = c.pos
	}

	for _, rule := range postfixTmpls {
		// When completing foo.print<>, "print" is naturally overwritten,
		// but we need to also remove "foo." so the snippet has a clean
		// slate.
		edits, err := c.editText(sel.Pos(), afterDot, "")
		if err != nil {
			event.Error(ctx, "error calculating postfix edits", err)
			return
		}

		tmplArgs := postfixTmplArgs{
			X:              source.GopFormatNode(c.pkg.FileSet(), sel.X),
			StmtOK:         stmtOK,
			Obj:            gopExprObj(c.pkg.GopTypesInfo(), sel.X),
			Type:           selType,
			qf:             c.qf,
			importIfNeeded: c.importIfNeeded,
			scope:          scope,
			varNames:       make(map[string]bool),
		}

		// Feed the template straight into the snippet builder. This
		// allows templates to build snippets as they are executed.
		err = rule.tmpl.Execute(&tmplArgs.snip, &tmplArgs)
		if err != nil {
			event.Error(ctx, "error executing postfix template", err)
			continue
		}

		if strings.TrimSpace(tmplArgs.snip.String()) == "" {
			continue
		}

		score := c.matcher.Score(rule.label)
		if score <= 0 {
			continue
		}

		c.items = append(c.items, CompletionItem{
			Label:               rule.label + "!",
			Detail:              rule.details,
			Score:               float64(score) * 0.01,
			Kind:                protocol.SnippetCompletion,
			snippet:             &tmplArgs.snip,
			AdditionalTextEdits: append(edits, tmplArgs.edits...),
		})
	}
}

// importIfNeeded returns the package identifier and any necessary
// edits to import package pkgPath.
func (c *gopCompleter) importIfNeeded(pkgPath string, scope *types.Scope) (string, []protocol.TextEdit, error) {
	defaultName := imports.ImportPathToAssumedName(pkgPath)

	// Check if file already imports pkgPath.
	for _, s := range c.file.Imports {
		// TODO(adonovan): what if pkgPath has a vendor/ suffix?
		// This may be the cause of go.dev/issue/56291.
		if source.GopUnquoteImportPath(s) == source.ImportPath(pkgPath) {
			if s.Name == nil {
				return defaultName, nil, nil
			}
			if s.Name.Name != "_" {
				return s.Name.Name, nil, nil
			}
		}
	}

	// Give up if the package's name is already in use by another object.
	if _, obj := scope.LookupParent(defaultName, token.NoPos); obj != nil {
		return "", nil, fmt.Errorf("import name %q of %q already in use", defaultName, pkgPath)
	}

	edits, err := c.importEdits(&importInfo{
		importPath: pkgPath,
	})
	if err != nil {
		return "", nil, err
	}

	return defaultName, edits, nil
}
