// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	goast "go/ast"
	"go/types"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/x/typesutil"
	"github.com/goplus/mod/gopmod"
	"golang.org/x/tools/gopls/internal/lsp/safetoken"
	"golang.org/x/tools/gopls/internal/lsp/source"
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

func checkCompiledFiles(cfg *typesutil.Config, check *typesutil.Checker, goFiles []*goast.File, compiledGopFiles []*source.ParsedGopFile) error {
	gopFiles := make([]*ast.File, 0, len(compiledGopFiles))
	checkKind := cfg.Mod != nil && cfg.Mod != gopmod.Default
	for _, cgf := range compiledGopFiles {
		f := cgf.File
		if checkKind && f.IsNormalGox {
			var isClass bool
			pos := safetoken.StartPosition(cfg.Fset, f.Pos())
			f.IsProj, isClass = cfg.Mod.ClassKind(pos.Filename)
			if isClass {
				f.IsNormalGox = false
			}
		}
		gopFiles = append(gopFiles, f)
	}
	return check.Files(goFiles, gopFiles)
}

func checkFiles(cfg *typesutil.Config, check *typesutil.Checker, gofiles []*goast.File, files []*ast.File) error {
	if cfg.Mod != nil && cfg.Mod != gopmod.Default {
		for _, f := range files {
			if f.IsNormalGox {
				var isClass bool
				pos := safetoken.StartPosition(cfg.Fset, f.Pos())
				f.IsProj, isClass = cfg.Mod.ClassKind(pos.Filename)
				if isClass {
					f.IsNormalGox = false
				}
			}
		}
	}
	return check.Files(gofiles, files)
}
