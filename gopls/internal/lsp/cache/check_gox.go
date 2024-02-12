// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"go/types"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/x/typesutil"
)

func newGopTypeInfo() *typesutil.Info {
	return &typesutil.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:     make(map[ast.Node]*types.Scope),
		Instances:  make(map[*ast.Ident]types.Instance),
		Overloads:  make(map[*ast.Ident][]types.Object),
	}
}

type gopImporter struct {
	imp types.Importer
	gop types.Importer
}

func (p *gopImporter) Import(path string) (*types.Package, error) {
	if pkg, err := p.imp.Import(path); err == nil {
		return pkg, nil
	}
	return p.gop.Import(path)
}

func newGopImporter(imp, gop types.Importer) types.Importer {
	return &gopImporter{imp, gop}
}
