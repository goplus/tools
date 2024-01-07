// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xrefs

import (
	"go/types"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/x/typesutil"
	"golang.org/x/tools/go/types/objectpath"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/internal/typeparams"
)

func gopIndex(
	files []*source.ParsedGopFile, pkg *types.Package, info *typesutil.Info,
	getObjects func(pkg *types.Package) map[types.Object]*gobObject,
	objectpathFor func(obj types.Object) (objectpath.Path, error)) {

	for fileIndex, pgf := range files {

		nodeRange := func(n ast.Node) protocol.Range {
			rng, err := pgf.PosRange(n.Pos(), n.End())
			if err != nil {
				panic(err) // can't fail
			}
			return rng
		}

		ast.Inspect(pgf.File, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Ident:
				// Report a reference for each identifier that
				// uses a symbol exported from another package.
				// (The built-in error.Error method has no package.)
				//if n.IsExported() {
				if obj, ok := info.Uses[n]; ok &&
					obj.Exported() && // gox obj.Exported replace ident.IsExported
					obj.Pkg() != nil &&
					obj.Pkg() != pkg {

					// For instantiations of generic methods,
					// use the generic object (see issue #60622).
					if fn, ok := obj.(*types.Func); ok {
						obj = typeparams.OriginMethod(fn)
					}

					objects := getObjects(obj.Pkg())
					gobObj, ok := objects[obj]
					if !ok {
						path, err := objectpathFor(obj)
						if err != nil {
							// Capitalized but not exported
							// (e.g. local const/var/type).
							return true
						}
						gobObj = &gobObject{Path: path}
						objects[obj] = gobObj
					}

					gobObj.GopRefs = append(gobObj.GopRefs, gobRef{
						FileIndex: fileIndex,
						Range:     nodeRange(n),
					})
				}
				//}

			case *ast.ImportSpec:
				// Report a reference from each import path
				// string to the imported package.
				pkgname, ok := source.GopImportedPkgName(info, n)
				if !ok {
					return true // missing import
				}
				objects := getObjects(pkgname.Imported())
				gobObj, ok := objects[nil]
				if !ok {
					gobObj = &gobObject{Path: ""}
					objects[nil] = gobObj
				}
				gobObj.GopRefs = append(gobObj.GopRefs, gobRef{
					FileIndex: fileIndex,
					Range:     nodeRange(n.Path),
				})
			}
			return true
		})
	}
}
