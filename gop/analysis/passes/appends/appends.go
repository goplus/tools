// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package appends defines an Analyzer that detects
// if there is only one variable in append.
package appends

import (
	_ "embed"
	"go/types"

	"github.com/goplus/gop/ast"
	"golang.org/x/tools/go/analysis/passes/appends"
	"golang.org/x/tools/gop/analysis"
	"golang.org/x/tools/gop/analysis/passes/inspect"
	"golang.org/x/tools/gop/analysis/passes/internal/analysisutil"
	"golang.org/x/tools/gop/ast/inspector"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "gopAppends",
	Doc:      analysisutil.MustExtractDoc(doc, "appends"),
	URL:      "https://pkg.go.dev/golang.org/x/tools/gop/analysis/passes/appends",
	Requires: []analysis.IAnalyzer{appends.Analyzer, inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.CallExpr)(nil),
	}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "append" {
			if _, ok := pass.GopTypesInfo.Uses[ident].(*types.Builtin); ok {
				if len(call.Args) == 1 {
					pass.ReportRangef(call, "append with no values")
				}
			}
		}
	})

	return nil, nil
}
