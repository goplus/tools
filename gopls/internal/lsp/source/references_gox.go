// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"bytes"
	"context"
	"fmt"
	"go/token"
	"go/types"
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/x/typesutil"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/types/objectpath"
	"golang.org/x/tools/gopls/internal/bug"
	"golang.org/x/tools/gopls/internal/goxls/parserutil"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/safetoken"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/event"
)

// References returns a list of all references (sorted with
// definitions before uses) to the object denoted by the identifier at
// the given file/position, searching the entire workspace.
func GopReferences(ctx context.Context, snapshot Snapshot, fh FileHandle, pp protocol.Position, includeDeclaration bool) ([]protocol.Location, error) {
	references, err := gopReferences(ctx, snapshot, fh, pp, includeDeclaration)
	log.Println("source.gopReferences: includeDeclaration =", includeDeclaration, "len(references) =", len(references))
	if err != nil {
		return nil, err
	}
	locations := make([]protocol.Location, len(references))
	for i, ref := range references {
		log.Println("source.gopReferences:", i, ref.location.URI, "isDeclaration:", ref.isDeclaration)
		locations[i] = ref.location
	}
	return locations, nil
}

// gopReferences returns a list of all references (sorted with
// definitions before uses) to the object denoted by the identifier at
// the given file/position, searching the entire workspace.
func gopReferences(ctx context.Context, snapshot Snapshot, f FileHandle, pp protocol.Position, includeDeclaration bool) ([]reference, error) {
	ctx, done := event.Start(ctx, "source.gopReferences")
	defer done()

	// Is the cursor within the package name declaration?
	_, inPackageName, err := gopParsePackageNameDecl(ctx, snapshot, f, pp)
	if err != nil {
		return nil, err
	}

	var refs []reference
	if inPackageName {
		refs, err = gopPackageReferences(ctx, snapshot, f.URI())
	} else {
		refs, err = gopOrdinaryReferences(ctx, snapshot, f.URI(), pp)
	}
	if err != nil {
		return nil, err
	}

	sort.Slice(refs, func(i, j int) bool {
		x, y := refs[i], refs[j]
		if x.isDeclaration != y.isDeclaration {
			return x.isDeclaration // decls < refs
		}
		return protocol.CompareLocation(x.location, y.location) < 0
	})

	// De-duplicate by location, and optionally remove declarations.
	out := refs[:0]
	for _, ref := range refs {
		if !includeDeclaration && ref.isDeclaration {
			continue
		}
		if len(out) == 0 || out[len(out)-1].location != ref.location {
			out = append(out, ref)
		}
	}
	refs = out

	return refs, nil
}

// gopPackageReferences returns a list of references to the package
// declaration of the specified name and uri by searching among the
// import declarations of all packages that directly import the target
// package.
func gopPackageReferences(ctx context.Context, snapshot Snapshot, uri span.URI) ([]reference, error) {
	metas, err := snapshot.MetadataForFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	if len(metas) == 0 {
		return nil, fmt.Errorf("found no package containing %s", uri)
	}

	var refs []reference

	// Find external references to the package declaration
	// from each direct import of the package.
	//
	// The narrowest package is the most broadly imported,
	// so we choose it for the external references.
	//
	// But if the file ends with _test.go then we need to
	// find the package it is testing; there's no direct way
	// to do that, so pick a file from the same package that
	// doesn't end in _test.go and start over.
	narrowest := metas[0]
	if narrowest.ForTest != "" && strings.HasSuffix(string(uri), "_test.go") {
		for _, f := range narrowest.CompiledGopFiles {
			if !strings.HasSuffix(string(f), "_test.go") {
				return gopPackageReferences(ctx, snapshot, f)
			}
		}
		for _, f := range narrowest.CompiledNongenGoFiles {
			if !strings.HasSuffix(string(f), "_test.go") {
				return packageReferences(ctx, snapshot, f)
			}
		}
		// This package has no non-test files.
		// Skip the search for external references.
		// (Conceivably one could blank-import an empty package, but why?)
	} else {
		rdeps, err := snapshot.ReverseDependencies(ctx, narrowest.ID, false) // direct
		if err != nil {
			return nil, err
		}

		// Restrict search to workspace packages.
		workspace, err := snapshot.WorkspaceMetadata(ctx)
		if err != nil {
			return nil, err
		}
		workspaceMap := make(map[PackageID]*Metadata, len(workspace))
		for _, m := range workspace {
			workspaceMap[m.ID] = m
		}

		for _, rdep := range rdeps {
			if _, ok := workspaceMap[rdep.ID]; !ok {
				continue
			}
			for _, uri := range rdep.CompiledGopFiles {
				fh, err := snapshot.ReadFile(ctx, uri)
				if err != nil {
					return nil, err
				}
				f, err := snapshot.ParseGop(ctx, fh, parserutil.ParseHeader)
				if err != nil {
					return nil, err
				}
				for _, imp := range f.File.Imports {
					if rdep.DepsByImpPath[GopUnquoteImportPath(imp)] == narrowest.ID {
						refs = append(refs, reference{
							isDeclaration: false,
							location:      gopMustLocation(f, imp),
							pkgPath:       narrowest.PkgPath,
						})
					}
				}
			}
			for _, uri := range rdep.CompiledNongenGoFiles {
				fh, err := snapshot.ReadFile(ctx, uri)
				if err != nil {
					return nil, err
				}
				f, err := snapshot.ParseGo(ctx, fh, ParseHeader)
				if err != nil {
					return nil, err
				}
				for _, imp := range f.File.Imports {
					if rdep.DepsByImpPath[UnquoteImportPath(imp)] == narrowest.ID {
						refs = append(refs, reference{
							isDeclaration: false,
							location:      mustLocation(f, imp),
							pkgPath:       narrowest.PkgPath,
						})
					}
				}
			}
		}
	}

	// Find internal "references" to the package from
	// of each package declaration in the target package itself.
	//
	// The widest package (possibly a test variant) has the
	// greatest number of files and thus we choose it for the
	// "internal" references.
	widest := metas[len(metas)-1] // may include _test.go files
	for _, uri := range widest.CompiledGopFiles {
		fh, err := snapshot.ReadFile(ctx, uri)
		if err != nil {
			return nil, err
		}
		f, err := snapshot.ParseGop(ctx, fh, parserutil.ParseHeader)
		if err != nil {
			return nil, err
		}
		fileName := f.File.Name
		if !f.HasPkgDecl() {
			name := *fileName
			name.Name = "" // goxls: change name from `main` to empty string
			fileName = &name
		}
		refs = append(refs, reference{
			isDeclaration: true, // (one of many)
			location:      gopMustLocation(f, fileName),
			pkgPath:       widest.PkgPath,
		})
	}
	for _, uri := range widest.CompiledNongenGoFiles {
		fh, err := snapshot.ReadFile(ctx, uri)
		if err != nil {
			return nil, err
		}
		f, err := snapshot.ParseGo(ctx, fh, ParseHeader)
		if err != nil {
			return nil, err
		}
		refs = append(refs, reference{
			isDeclaration: true, // (one of many)
			location:      mustLocation(f, f.File.Name),
			pkgPath:       widest.PkgPath,
		})
	}

	return refs, nil
}

// gopOrdinaryReferences computes references for all ordinary objects (not package declarations).
func gopOrdinaryReferences(ctx context.Context, snapshot Snapshot, uri span.URI, pp protocol.Position) ([]reference, error) {
	// Strategy: use the reference information computed by the
	// type checker to find the declaration. First type-check this
	// package to find the declaration, then type check the
	// declaring package (which may be different), plus variants,
	// to find local (in-package) references.
	// Global references are satisfied by the index.

	// Strictly speaking, a wider package could provide a different
	// declaration (e.g. because the _test.go files can change the
	// meaning of a field or method selection), but the narrower
	// package reports the more broadly referenced object.
	pkg, pgf, err := NarrowestPackageForGopFile(ctx, snapshot, uri)
	if err != nil {
		return nil, err
	}

	// Find the selected object (declaration or reference).
	// For struct{T}, we choose the field (Def) over the type (Use).
	pos, err := pgf.PositionPos(pp)
	if err != nil {
		return nil, err
	}
	candidates, _, err := gopObjectsAt(pkg.GopTypesInfo(), pgf.File, pos)
	if err != nil {
		return nil, err
	}

	// Pick first object arbitrarily.
	// The case variables of a type switch have different
	// types but that difference is immaterial here.
	var obj types.Object
	for obj = range candidates {
		break
	}
	if obj == nil {
		return nil, ErrNoIdentFound // can't happen
	}

	// nil, error, error.Error, iota, or other built-in?
	if obj.Pkg() == nil {
		return nil, fmt.Errorf("references to builtin %q are not supported", obj.Name())
	}
	if !obj.Pos().IsValid() {
		if obj.Pkg().Path() != "unsafe" {
			bug.Reportf("references: object %v has no position", obj)
		}
		return nil, fmt.Errorf("references to unsafe.%s are not supported", obj.Name())
	}

	// Find metadata of all packages containing the object's defining file.
	// This may include the query pkg, and possibly other variants.
	declPosn := safetoken.StartPosition(pkg.FileSet(), obj.Pos())
	declURI := span.URIFromPath(declPosn.Filename)
	variants, err := snapshot.MetadataForFile(ctx, declURI)
	if err != nil {
		return nil, err
	}
	if len(variants) == 0 {
		return nil, fmt.Errorf("no packages for file %q", declURI) // can't happen
	}
	// (variants must include ITVs for reverse dependency computation below.)

	// Is object exported?
	// If so, compute scope and targets of the global search.
	var (
		globalScope   = make(map[PackageID]*Metadata) // (excludes ITVs)
		globalTargets map[PackagePath]map[objectpath.Path]unit
		expansions    = make(map[PackageID]unit) // packages that caused search expansion
	)
	// TODO(adonovan): what about generic functions? Need to consider both
	// uninstantiated and instantiated. The latter have no objectpath. Use Origin?
	if path, err := objectpath.For(obj); err == nil && obj.Exported() {
		pkgPath := variants[0].PkgPath // (all variants have same package path)
		globalTargets = map[PackagePath]map[objectpath.Path]unit{
			pkgPath: {path: {}}, // primary target
		}

		// Compute set of (non-ITV) workspace packages.
		// We restrict references to this subset.
		workspace, err := snapshot.WorkspaceMetadata(ctx)
		if err != nil {
			return nil, err
		}
		workspaceMap := make(map[PackageID]*Metadata, len(workspace))
		workspaceIDs := make([]PackageID, 0, len(workspace))
		for _, m := range workspace {
			workspaceMap[m.ID] = m
			workspaceIDs = append(workspaceIDs, m.ID)
		}

		// addRdeps expands the global scope to include the
		// reverse dependencies of the specified package.
		addRdeps := func(id PackageID, transitive bool) error {
			rdeps, err := snapshot.ReverseDependencies(ctx, id, transitive)
			if err != nil {
				return err
			}
			for rdepID, rdep := range rdeps {
				// Skip non-workspace packages.
				//
				// This means we also skip any expansion of the
				// search that might be caused by a non-workspace
				// package, possibly causing us to miss references
				// to the expanded target set from workspace packages.
				//
				// TODO(adonovan): don't skip those expansions.
				// The challenge is how to so without type-checking
				// a lot of non-workspace packages not covered by
				// the initial workspace load.
				if _, ok := workspaceMap[rdepID]; !ok {
					continue
				}

				globalScope[rdepID] = rdep
			}
			return nil
		}

		// How far need we search?
		// For package-level objects, we need only search the direct importers.
		// For fields and methods, we must search transitively.
		transitive := obj.Pkg().Scope().Lookup(obj.Name()) != obj

		// The scope is the union of rdeps of each variant.
		// (Each set is disjoint so there's no benefit to
		// combining the metadata graph traversals.)
		for _, m := range variants {
			if err := addRdeps(m.ID, transitive); err != nil {
				return nil, err
			}
		}

		// Is object a method?
		//
		// If so, expand the search so that the targets include
		// all methods that correspond to it through interface
		// satisfaction, and the scope includes the rdeps of
		// the package that declares each corresponding type.
		//
		// 'expansions' records the packages that declared
		// such types.
		if recv := effectiveReceiver(obj); recv != nil {
			if err := expandMethodSearch(ctx, snapshot, workspaceIDs, obj.(*types.Func), recv, addRdeps, globalTargets, expansions); err != nil {
				return nil, err
			}
		}
	}

	// The search functions will call report(loc) for each hit.
	var (
		refsMu sync.Mutex
		refs   []reference
	)
	report := func(loc protocol.Location, isDecl bool) {
		ref := reference{
			isDeclaration: isDecl,
			location:      loc,
			pkgPath:       pkg.Metadata().PkgPath,
		}
		refsMu.Lock()
		refs = append(refs, ref)
		refsMu.Unlock()
	}

	// Loop over the variants of the declaring package,
	// and perform both the local (in-package) and global
	// (cross-package) searches, in parallel.
	//
	// TODO(adonovan): opt: support LSP reference streaming. See:
	// - https://github.com/microsoft/vscode-languageserver-node/pull/164
	// - https://github.com/microsoft/language-server-protocol/pull/182
	//
	// Careful: this goroutine must not return before group.Wait.
	var group errgroup.Group

	// Compute local references for each variant.
	// The target objects are identified by (URI, offset).
	for _, m := range variants {
		// We want the ordinary importable package,
		// plus any test-augmented variants, since
		// declarations in _test.go files may change
		// the reference of a selection, or even a
		// field into a method or vice versa.
		//
		// But we don't need intermediate test variants,
		// as their local references will be covered
		// already by other variants.
		if m.IsIntermediateTestVariant() {
			continue
		}
		m := m
		group.Go(func() error {
			// TODO(adonovan): opt: batch these TypeChecks.
			pkgs, err := snapshot.TypeCheck(ctx, m.ID)
			if err != nil {
				return err
			}
			pkg := pkgs[0]

			// find go files
			if strings.HasSuffix(string(declURI), ".go") {
				return goFindLocalReferences(pkg, declURI, declPosn, report)
			}

			// Find the declaration of the corresponding
			// object in this package based on (URI, offset).
			pgf, err := pkg.GopFile(declURI)
			if err != nil {
				return err
			}
			pos, err := safetoken.Pos(pgf.Tok, declPosn.Offset)
			if err != nil {
				return err
			}
			objects, _, err := gopObjectsAt(pkg.GopTypesInfo(), pgf.File, pos)
			if err != nil {
				return err // unreachable? (probably caught earlier)
			}

			// Report the locations of the declaration(s).
			// TODO(adonovan): what about for corresponding methods? Add tests.
			for _, node := range objects {
				if funcLit, ok := node.(*ast.FuncLit); ok {
					report(gopMustLocation(pgf, funcLit.Type), true)
				} else {
					report(gopMustLocation(pgf, node), true)
				}
			}

			// Convert targets map to set.
			targets := make(map[types.Object]bool)
			for obj := range objects {
				targets[obj] = true
			}

			return localReferences(pkg, targets, true, report)
		})
	}

	// Also compute local references within packages that declare
	// corresponding methods (see above), which expand the global search.
	// The target objects are identified by (PkgPath, objectpath).
	for id := range expansions {
		id := id
		group.Go(func() error {
			// TODO(adonovan): opt: batch these TypeChecks.
			pkgs, err := snapshot.TypeCheck(ctx, id)
			if err != nil {
				return err
			}
			pkg := pkgs[0]

			targets := make(map[types.Object]bool)
			for objpath := range globalTargets[pkg.Metadata().PkgPath] {
				obj, err := objectpath.Object(pkg.GetTypes(), objpath)
				if err != nil {
					// No such object, because it was
					// declared only in the test variant.
					continue
				}
				targets[obj] = true
			}

			// Don't include corresponding types or methods
			// since expansions did that already, and we don't
			// want (e.g.) concrete -> interface -> concrete.
			const correspond = false
			return localReferences(pkg, targets, correspond, report)
		})
	}

	// Compute global references for selected reverse dependencies.
	group.Go(func() error {
		var globalIDs []PackageID
		for id := range globalScope {
			globalIDs = append(globalIDs, id)
		}
		indexes, err := snapshot.References(ctx, globalIDs...)
		if err != nil {
			return err
		}
		for _, index := range indexes {
			for _, loc := range index.Lookup(globalTargets) {
				report(loc, false)
			}
		}
		return nil
	})

	if err := group.Wait(); err != nil {
		return nil, err
	}
	return refs, nil
}

// gopObjectsAt returns the non-empty set of objects denoted (def or use)
// by the specified position within a file syntax tree, or an error if
// none were found.
//
// The result may contain more than one element because all case
// variables of a type switch appear to be declared at the same
// position.
//
// Each object is mapped to the syntax node that was treated as an
// identifier, which is not always an ast.Ident. The second component
// of the result is the innermost node enclosing pos.
//
// TODO(adonovan): factor in common with referencedObject.
func gopObjectsAt(info *typesutil.Info, file *ast.File, pos token.Pos) (map[types.Object]ast.Node, ast.Node, error) {
	path := gopPathEnclosingObjNode(file, pos)
	if path == nil {
		return nil, nil, ErrNoIdentFound
	}

	targets := make(map[types.Object]ast.Node)

	switch leaf := path[0].(type) {
	case *ast.Ident:
		// If leaf represents an implicit type switch object or the type
		// switch "assign" variable, expand to all of the type switch's
		// implicit objects.
		if implicits, _ := gopTypeSwitchImplicits(info, path); len(implicits) > 0 {
			for _, obj := range implicits {
				targets[obj] = leaf
			}
		} else {
			// Note: prior to go1.21, go/types issue #60372 causes the position
			// a field Var T created for struct{*p.T} to be recorded at the
			// start of the field type ("*") not the location of the T.
			// This affects references and other gopls operations (issue #60369).
			// TODO(adonovan): delete this comment when we drop support for go1.20.

			// For struct{T}, we prefer the defined field Var over the used TypeName.
			obj := info.ObjectOf(leaf)
			if obj == nil {
				return nil, nil, fmt.Errorf("%w for %q", errNoObjectFound, leaf.Name)
			}
			targets[obj] = leaf
		}
	case *ast.ImportSpec:
		// Look up the implicit *types.PkgName.
		obj := info.Implicits[leaf]
		if obj == nil {
			return nil, nil, fmt.Errorf("%w for import %s", errNoObjectFound, GopUnquoteImportPath(leaf))
		}
		targets[obj] = leaf
	case *ast.FuncLit:
		// Look up the implicit *types.Func (overload member)
		obj := info.Implicits[leaf]
		if obj == nil {
			var buf bytes.Buffer
			typesutil.WriteExpr(&buf, leaf)
			return nil, nil, fmt.Errorf("%w for %q", errNoObjectFound, buf.String())
		}
		targets[obj] = leaf
	}

	if len(targets) == 0 {
		return nil, nil, fmt.Errorf("objectAt: internal error: no targets") // can't happen
	}
	return targets, path[0], nil
}

// gopMustLocation reports the location interval a syntax node,
// which must belong to m.File.
//
// Safe for use only by references and implementations.
func gopMustLocation(pgf *ParsedGopFile, n ast.Node) protocol.Location {
	loc, err := pgf.NodeLocation(n)
	if err != nil {
		panic(err) // can't happen in references or implementations
	}
	return loc
}

func goFindLocalReferences(pkg Package, declURI span.URI, declPosn token.Position, report func(loc protocol.Location, isDecl bool)) error {
	pgf, err := pkg.File(declURI)
	if err != nil {
		return err
	}
	pos, err := safetoken.Pos(pgf.Tok, declPosn.Offset)
	if err != nil {
		return err
	}
	objects, _, err := objectsAt(pkg.GetTypesInfo(), pgf.File, pos)
	if err != nil {
		return err // unreachable? (probably caught earlier)
	}

	// Report the locations of the declaration(s).
	// TODO(adonovan): what about for corresponding methods? Add tests.
	for _, node := range objects {
		report(mustLocation(pgf, node), true)
	}

	// Convert targets map to set.
	targets := make(map[types.Object]bool)
	for obj := range objects {
		targets[obj] = true
	}

	return localReferences(pkg, targets, true, report)
}

func gopFindLocalReferences(pkg Package, declURI span.URI, declPosn token.Position, report func(loc protocol.Location, isDecl bool)) error {
	pgf, err := pkg.GopFile(declURI)
	if err != nil {
		return err
	}
	pos, err := safetoken.Pos(pgf.Tok, declPosn.Offset)
	if err != nil {
		return err
	}
	objects, _, err := gopObjectsAt(pkg.GopTypesInfo(), pgf.File, pos)
	if err != nil {
		return err // unreachable? (probably caught earlier)
	}

	// Report the locations of the declaration(s).
	// TODO(adonovan): what about for corresponding methods? Add tests.
	for _, node := range objects {
		report(gopMustLocation(pgf, node), true)
	}

	// Convert targets map to set.
	targets := make(map[types.Object]bool)
	for obj := range objects {
		targets[obj] = true
	}

	return localReferences(pkg, targets, true, report)
}
