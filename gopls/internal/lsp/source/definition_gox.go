// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"fmt"
	"go/types"
	"log"
	"strings"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/token"
	"golang.org/x/tools/gopls/internal/bug"
	"golang.org/x/tools/gopls/internal/goxls"
	"golang.org/x/tools/gopls/internal/goxls/parserutil"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/safetoken"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/event"
)

// GopDefinition handles the textDocument/definition request for Go files.
func GopDefinition(ctx context.Context, snapshot Snapshot, fh FileHandle, position protocol.Position) ([]protocol.Location, error) {
	if goxls.DbgDefinition {
		log.Println("GopDefinition:", fh.URI().Filename(), position.Line+1, position.Character+1)
		defer log.Println("GopDefinition done:", fh.URI().Filename(), position.Line+1, position.Character+1)
	}
	ctx, done := event.Start(ctx, "source.GopDefinition")
	defer done()

	pkg, pgf, err := NarrowestPackageForGopFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	pos, err := pgf.PositionPos(position)
	if err != nil {
		return nil, err
	}

	// Handle the case where the cursor is in an import.
	importLocations, err := gopImportDefinition(ctx, snapshot, pkg, pgf, pos)
	if err != nil {
		return nil, err
	}
	if goxls.DbgDefinition {
		log.Println("gopImportDefinition: len(importLocations) =", len(importLocations))
	}
	if len(importLocations) > 0 {
		return importLocations, nil
	}

	// Handle the case where the cursor is in the package name.
	// We use "<= End" to accept a query immediately after the package name.
	// goxls: a Go+ file maybe no `package xxx`
	// if pgf.File != nil && pgf.File.Name.Pos() <= pos && pos <= pgf.File.Name.End() {
	if pgf.File != nil && pgf.HasPkgDecl() && pgf.File.Name.Pos() <= pos && pos <= pgf.File.Name.End() {
		for _, pgf := range pkg.CompiledNongenGoFiles() {
			if pgf.File.Name != nil && pgf.File.Doc != nil {
				loc, err := pgf.NodeLocation(pgf.File.Name)
				if err != nil {
					return nil, err
				}
				return []protocol.Location{loc}, nil
			}
		}
		// If there's no package documentation, just use current file.
		declFile := pgf
		for _, pgf := range pkg.CompiledGopFiles() {
			if pgf.HasPkgDecl() && pgf.File.Doc != nil {
				declFile = pgf
				break
			}
		}
		loc, err := declFile.NodeLocation(declFile.File.Name)
		if err != nil {
			return nil, err
		}
		return []protocol.Location{loc}, nil
	}

	// The general case: the cursor is on an identifier.
	_, obj, _ := gopReferencedObject(pkg, pgf, pos)
	if obj == nil {
		return nil, nil
	}

	var anonyOvFunc *ast.FuncLit //goxls:overload anonymous member
	if ovPkg, ovFuncLit, ovObj, ok := IsOverloadAnonymousMember(ctx, snapshot, pkg, obj); ok {
		pkg = ovPkg
		obj = ovObj
		anonyOvFunc = ovFuncLit
	}

	if goxls.DbgDefinition {
		log.Println("gopReferencedObject ret:", obj, "pos:", obj.Pos())
	}

	// Handle objects with no position: builtin, unsafe.
	if !obj.Pos().IsValid() {
		var pgf *ParsedGoFile
		if obj.Parent() == types.Universe {
			// pseudo-package "builtin"
			builtinPGF, err := snapshot.BuiltinFile(ctx)
			if err != nil {
				return nil, err
			}
			pgf = builtinPGF

		} else if obj.Pkg() == types.Unsafe {
			// package "unsafe"
			unsafe := snapshot.Metadata("unsafe")
			if unsafe == nil {
				return nil, fmt.Errorf("no metadata for package 'unsafe'")
			}
			uri := unsafe.GoFiles[0]
			fh, err := snapshot.ReadFile(ctx, uri)
			if err != nil {
				return nil, err
			}
			pgf, err = snapshot.ParseGo(ctx, fh, ParseFull&^SkipObjectResolution)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, bug.Errorf("internal error: no position for %v", obj.Name())
		}
		// Inv: pgf ∈ {builtin,unsafe}.go

		// Use legacy (go/ast) object resolution.
		astObj := pgf.File.Scope.Lookup(obj.Name())
		if astObj == nil {
			// Every built-in should have documentation syntax.
			return nil, bug.Errorf("internal error: no object for %s", obj.Name())
		}
		decl, ok := astObj.Decl.(ast.Node)
		if !ok {
			return nil, bug.Errorf("internal error: no declaration for %s", obj.Name())
		}
		loc, err := pgf.PosLocation(decl.Pos(), decl.Pos()+token.Pos(len(obj.Name())))
		if err != nil {
			return nil, err
		}
		return []protocol.Location{loc}, nil
	}

	typeEnd := adjustedObjEnd(obj)
	if anonyOvFunc != nil { // goxls: anonymous overload function
		typeEnd = anonyOvFunc.Type.End()
	}
	// Finally, map the object position.
	loc, err := mapPosition(ctx, pkg.FileSet(), snapshot, obj.Pos(), typeEnd)
	if goxls.DbgDefinition {
		log.Println("gopReferencedObject mapPosition:", obj, "err:", err, "loc:", loc)
	}
	if err != nil {
		return nil, err
	}
	return []protocol.Location{loc}, nil
}

// gopReferencedObject returns the identifier and object referenced at the
// specified position, which must be within the file pgf, for the purposes of
// definition/hover/call hierarchy operations. It returns a nil object if no
// object was found at the given position.
//
// If the returned identifier is a type-switch implicit (i.e. the x in x :=
// e.(type)), the third result will be the type of the expression being
// switched on (the type of e in the example). This facilitates workarounds for
// limitations of the go/types API, which does not report an object for the
// identifier x.
//
// For embedded fields, referencedObject returns the type name object rather
// than the var (field) object.
//
// TODO(rfindley): this function exists to preserve the pre-existing behavior
// of source.Identifier. Eliminate this helper in favor of sharing
// functionality with objectsAt, after choosing suitable primitives.
func gopReferencedObject(pkg Package, pgf *ParsedGopFile, pos token.Pos) (*ast.Ident, types.Object, types.Type) {
	path := gopPathEnclosingObjNode(pgf.File, pos)
	if len(path) == 0 {
		return nil, nil, nil
	}
	var obj types.Object
	info := pkg.GopTypesInfo()
	switch n := path[0].(type) {
	case *ast.Ident:
		obj = info.ObjectOf(n)
		if goxls.DbgDefinition {
			log.Println("gop info.ObjectOf:", n, obj, "def:", info.Defs[n])
		}

		// If n is the var's declaring ident in a type switch
		// [i.e. the x in x := foo.(type)], it will not have an object. In this
		// case, set obj to the first implicit object (if any), and return the type
		// of the expression being switched on.
		//
		// The type switch may have no case clauses and thus no
		// implicit objects; this is a type error ("unused x"),
		if obj == nil {
			if implicits, typ := gopTypeSwitchImplicits(info, path); len(implicits) > 0 {
				if goxls.DbgDefinition {
					log.Println("gopTypeSwitchImplicits:", implicits[0])
				}
				return n, implicits[0], typ
			}
		}

		// If the original position was an embedded field, we want to jump
		// to the field's type definition, not the field's definition.
		if v, ok := obj.(*types.Var); ok && v.Embedded() {
			// types.Info.Uses contains the embedded field's *types.TypeName.
			if typeName := info.Uses[n]; typeName != nil {
				obj = typeName
			}
		}
		return n, obj, nil
	}
	return nil, nil, nil
}

// gopImportDefinition returns locations defining a package referenced by the
// import spec containing pos.
//
// If pos is not inside an import spec, it returns nil, nil.
func gopImportDefinition(ctx context.Context, s Snapshot, pkg Package, pgf *ParsedGopFile, pos token.Pos) ([]protocol.Location, error) {
	var imp *ast.ImportSpec
	for _, spec := range pgf.File.Imports {
		// We use "<= End" to accept a query immediately after an ImportSpec.
		if spec.Path.Pos() <= pos && pos <= spec.Path.End() {
			imp = spec
		}
	}
	if imp == nil {
		return nil, nil
	}

	importPath := GopUnquoteImportPath(imp)
	impID := pkg.Metadata().DepsByImpPath[importPath]
	if impID == "" {
		return nil, fmt.Errorf("failed to resolve import %q", importPath)
	}
	impMetadata := s.Metadata(impID)
	if impMetadata == nil {
		return nil, fmt.Errorf("missing information for package %q", impID)
	}

	var locs []protocol.Location
	for _, f := range impMetadata.CompiledNongenGoFiles {
		fh, err := s.ReadFile(ctx, f)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		pgf, err := s.ParseGo(ctx, fh, ParseHeader)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		loc, err := pgf.NodeLocation(pgf.File)
		if err != nil {
			return nil, err
		}
		locs = append(locs, loc)
	}
	for _, f := range impMetadata.CompiledGopFiles {
		fh, err := s.ReadFile(ctx, f)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		pgf, err := s.ParseGop(ctx, fh, parserutil.ParseHeader)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		loc, err := pgf.NodeLocation(pgf.File)
		if err != nil {
			return nil, err
		}
		locs = append(locs, loc)
	}

	if len(locs) == 0 {
		return nil, fmt.Errorf("package %q has no readable files", impID) // incl. unsafe
	}

	return locs, nil
}

// goxls:match in current package & variants
func IsOverloadAnonymousMember(ctx context.Context, snapshot Snapshot, pkg Package, obj types.Object) (Package, *ast.FuncLit, types.Object, bool) {
	if _, ok := obj.(*types.Func); !ok {
		return nil, nil, nil, false
	}

	declPosn := safetoken.StartPosition(pkg.FileSet(), obj.Pos())
	declURI := span.URIFromPath(declPosn.Filename)

	inPkg := func(searchPkg Package) (*ast.FuncLit, types.Object, bool) {
		fset := searchPkg.FileSet()
		for ov, om := range searchPkg.GopTypesInfo().Implicits {
			if anonyOvFunc, ok := ov.(*ast.FuncLit); ok {
				funPos := safetoken.StartPosition(fset, anonyOvFunc.Pos())
				if declPosn.Offset == funPos.Offset {
					return anonyOvFunc, om, true
				}
			}
		}
		return nil, nil, false
	}

	// goxls:match in current package
	if funcLit, ovObj, ok := inPkg(pkg); ok {
		return pkg, funcLit, ovObj, true
	}

	// goxls:match in variants package
	if strings.HasSuffix(string(declURI), ".gop") {
		variants, err := snapshot.MetadataForFile(ctx, declURI)
		if err != nil {
			return nil, nil, nil, false
		}
		for _, m := range variants {
			varPkgs, err := snapshot.TypeCheck(ctx, m.ID)
			if err != nil {
				return nil, nil, nil, false
			}
			varPkg := varPkgs[0]
			if funcLit, ovObj, ok := inPkg(varPkg); ok {
				return varPkg, funcLit, ovObj, true
			}
		}
	}
	return nil, nil, nil, false
}
