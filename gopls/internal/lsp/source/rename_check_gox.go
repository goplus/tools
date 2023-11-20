// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"go/types"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/x/typesutil"
)

// gopForEachLexicalRef calls fn(id, block) for each identifier id in package
// pkg that is a reference to obj in lexical scope.  block is the
// lexical block enclosing the reference.  If fn returns false the
// iteration is terminated and findLexicalRefs returns false.
func gopForEachLexicalRef(pkg Package, obj types.Object, fn func(id *ast.Ident, block *types.Scope) bool) bool {
	ok := true
	var stack []ast.Node

	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1] // pop
			return false
		}
		if !ok {
			return false // bail out
		}

		stack = append(stack, n) // push
		switch n := n.(type) {
		case *ast.Ident:
			if pkg.GopTypesInfo().Uses[n] == obj {
				block := enclosingBlock(pkg.GetTypesInfo(), stack)
				if !fn(n, block) {
					ok = false
				}
			}
			return visit(nil) // pop stack

		case *ast.SelectorExpr:
			// don't visit n.Sel
			ast.Inspect(n.X, visit)
			return visit(nil) // pop stack, don't descend

		case *ast.CompositeLit:
			// Handle recursion ourselves for struct literals
			// so we don't visit field identifiers.
			tv, ok := pkg.GopTypesInfo().Types[n]
			if !ok {
				return visit(nil) // pop stack, don't descend
			}
			if _, ok := Deref(tv.Type).Underlying().(*types.Struct); ok {
				if n.Type != nil {
					ast.Inspect(n.Type, visit)
				}
				for _, elt := range n.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						ast.Inspect(kv.Value, visit)
					} else {
						ast.Inspect(elt, visit)
					}
				}
				return visit(nil) // pop stack, don't descend
			}
		}
		return true
	}

	for _, cgf := range pkg.CompiledGopFiles() {
		f := cgf.File
		ast.Inspect(f, visit)
		if len(stack) != 0 {
			panic(stack)
		}
		if !ok {
			break
		}
	}
	return ok
}

// isLocal reports whether obj is local to some function.
// Precondition: not a struct field or interface method.
func isGopLocal(obj types.Object) bool {
	var depth int
	for scope := obj.Parent(); scope != nil; scope = scope.Parent() {
		depth++
	}
	return depth >= 3
}

func isGopPackageLevel(obj types.Object) bool {
	if obj == nil {
		return false
	}
	// goxls: fast version
	return obj.Pkg().Scope().Lookup(obj.Name()) == obj
}

// check performs safety checks of the renaming of the 'from' object to r.to.
func (r *renamer) gopCheck(from types.Object) {
	if r.objsToUpdate[from] {
		return
	}
	r.objsToUpdate[from] = true

	// NB: order of conditions is important.
	if from_, ok := from.(*types.PkgName); ok {
		r.checkInFileBlock(from_)
	} else if from_, ok := from.(*types.Label); ok {
		r.checkLabel(from_)
	} else if isGopPackageLevel(from) {
		r.checkInPackageBlock(from)
	} else if v, ok := from.(*types.Var); ok && v.IsField() {
		r.checkStructField(v)
	} else if f, ok := from.(*types.Func); ok && recv(f) != nil {
		r.checkMethod(f)
	} else if isGopLocal(from) {
		r.checkInLexicalScope(from)
	} else {
		r.errorf(from.Pos(), "unexpected %s object %q (please report a bug)\n",
			objectKind(from), from)
	}
}

func (r *renamer) gopCheckExport(id *ast.Ident, pkg *types.Package, from types.Object) bool {
	// Reject cross-package references if r.to is unexported.
	// (Such references may be qualified identifiers or field/method
	// selections.)
	if !ast.IsExported(r.to) && pkg != from.Pkg() {
		r.errorf(from.Pos(),
			"renaming %q to %q would make it unexported",
			from.Name(), r.to)
		r.errorf(id.Pos(), "\tbreaking references from packages such as %q",
			pkg.Path())
		return false
	}
	return true
}

// gopSomeUse returns an arbitrary use of obj within info.
func gopSomeUse(info *typesutil.Info, obj types.Object) *ast.Ident {
	for id, o := range info.Uses {
		if o == obj {
			return id
		}
	}
	return nil
}
