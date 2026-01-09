// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package analysisutil_test

import (
	"go/types"
	"testing"

	"github.com/goplus/xgo/ast"
	"github.com/goplus/xgo/parser"
	"github.com/goplus/xgo/token"
	"github.com/goplus/xgo/x/typesutil"
	"golang.org/x/tools/gop/analysis/passes/internal/analysisutil"
)

func TestHasSideEffects(t *testing.T) {
	src := `package p

type T int

func _() {
	var x int
	_ = T(x)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.gop", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var conf typesutil.CheckConfig
	info := &typesutil.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
	}
	typesutil.SetDebug(typesutil.DbgFlagAll)
	_, err = conf.Check("", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if got := analysisutil.HasSideEffects(info, call); got != false {
			t.Errorf("HasSideEffects(%s) = true, want false", typesutil.ExprString(call))
		}
		return true
	})
}
