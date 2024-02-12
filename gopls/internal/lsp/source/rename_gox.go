// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"errors"
	"fmt"
	"go/types"
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/token"
	"golang.org/x/tools/go/types/objectpath"
	"golang.org/x/tools/gop/ast/astutil"
	"golang.org/x/tools/gopls/internal/bug"
	"golang.org/x/tools/gopls/internal/goxls/parserutil"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/safetoken"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/typeparams"
)

// GopPrepareRename searches for a valid renaming at position pp.
//
// The returned usererr is intended to be displayed to the user to explain why
// the prepare fails. Probably we could eliminate the redundancy in returning
// two errors, but for now this is done defensively.
func GopPrepareRename(ctx context.Context, snapshot Snapshot, f FileHandle, pp protocol.Position) (_ *PrepareItem, usererr, err error) {
	ctx, done := event.Start(ctx, "source.GopPrepareRename")
	defer done()

	// Is the cursor within the package name declaration?
	if pgf, inPackageName, err := gopParsePackageNameDecl(ctx, snapshot, f, pp); err != nil {
		return nil, err, err
	} else if inPackageName {
		item, err := gopPrepareRenamePackageName(ctx, snapshot, pgf)
		return item, err, err
	}

	// Ordinary (non-package) renaming.
	//
	// Type-check the current package, locate the reference at the position,
	// validate the object, and report its name and range.
	//
	// TODO(adonovan): in all cases below, we return usererr=nil,
	// which means we return (nil, nil) at the protocol
	// layer. This seems like a bug, or at best an exploitation of
	// knowledge of VSCode-specific behavior. Can we avoid that?
	pkg, pgf, err := NarrowestPackageForGopFile(ctx, snapshot, f.URI())
	if err != nil {
		return nil, nil, err
	}
	pos, err := pgf.PositionPos(pp)
	if err != nil {
		return nil, nil, err
	}
	targets, node, err := gopObjectsAt(pkg.GopTypesInfo(), pgf.File, pos)
	if err != nil {
		return nil, nil, err
	}
	var obj types.Object
	for obj = range targets {
		break // pick one arbitrarily
	}
	if err := checkRenamable(obj); err != nil {
		return nil, nil, err
	}
	rng, err := pgf.NodeRange(node)
	if err != nil {
		return nil, nil, err
	}
	if _, isImport := node.(*ast.ImportSpec); isImport {
		// We're not really renaming the import path.
		rng.End = rng.Start
	}
	return &PrepareItem{
		Range: rng,
		Text:  obj.Name(),
	}, nil, nil
}

func gopPrepareRenamePackageName(ctx context.Context, snapshot Snapshot, pgf *ParsedGopFile) (*PrepareItem, error) {
	// Does the client support file renaming?
	fileRenameSupported := false
	for _, op := range snapshot.View().Options().SupportedResourceOperations {
		if op == protocol.Rename {
			fileRenameSupported = true
			break
		}
	}
	if !fileRenameSupported {
		return nil, errors.New("can't rename package: LSP client does not support file renaming")
	}

	// Check validity of the metadata for the file's containing package.
	meta, err := NarrowestMetadataForFile(ctx, snapshot, pgf.URI)
	if err != nil {
		return nil, err
	}
	if meta.Name == "main" {
		return nil, fmt.Errorf("can't rename package \"main\"")
	}
	if strings.HasSuffix(string(meta.Name), "_test") {
		return nil, fmt.Errorf("can't rename x_test packages")
	}
	if meta.Module == nil {
		return nil, fmt.Errorf("can't rename package: missing module information for package %q", meta.PkgPath)
	}
	if meta.Module.Path == string(meta.PkgPath) {
		return nil, fmt.Errorf("can't rename package: package path %q is the same as module path %q", meta.PkgPath, meta.Module.Path)
	}

	// Return the location of the package declaration.
	rng, err := pgf.NodeRange(pgf.File.Name)
	if err != nil {
		return nil, err
	}
	return &PrepareItem{
		Range: rng,
		Text:  string(meta.Name),
	}, nil
}

// GopRename returns a map of TextEdits for each file modified when renaming a
// given identifier within a package and a boolean value of true for renaming
// package and false otherwise.
func GopRename(ctx context.Context, snapshot Snapshot, f FileHandle, pp protocol.Position, newName string) (map[span.URI][]protocol.TextEdit, bool, error) {
	ctx, done := event.Start(ctx, "source.GopRename")
	defer done()

	if !isValidIdentifier(newName) {
		return nil, false, fmt.Errorf("invalid identifier to rename: %q", newName)
	}

	// Cursor within package name declaration?
	_, inPackageName, err := gopParsePackageNameDecl(ctx, snapshot, f, pp)
	if err != nil {
		return nil, false, err
	}

	var editMap map[span.URI][]diff.Edit
	if inPackageName {
		editMap, err = gopRenamePackageName(ctx, snapshot, f, PackageName(newName))
	} else {
		editMap, err = gopRenameOrdinary(ctx, snapshot, f, pp, newName)
	}
	if err != nil {
		return nil, false, err
	}

	// Convert edits to protocol form.
	result := make(map[span.URI][]protocol.TextEdit)
	for uri, edits := range editMap {
		// Sort and de-duplicate edits.
		//
		// Overlapping edits may arise in local renamings (due
		// to type switch implicits) and globals ones (due to
		// processing multiple package variants).
		//
		// We assume renaming produces diffs that are all
		// replacements (no adjacent insertions that might
		// become reordered) and that are either identical or
		// non-overlapping.
		diff.SortEdits(edits)
		filtered := edits[:0]
		for i, edit := range edits {
			if i == 0 || edit != filtered[len(filtered)-1] {
				filtered = append(filtered, edit)
			}
		}
		edits = filtered

		// TODO(adonovan): the logic above handles repeat edits to the
		// same file URI (e.g. as a member of package p and p_test) but
		// is not sufficient to handle file-system level aliasing arising
		// from symbolic or hard links. For that, we should use a
		// robustio-FileID-keyed map.
		// See https://go.dev/cl/457615 for example.
		// This really occurs in practice, e.g. kubernetes has
		// vendor/k8s.io/kubectl -> ../../staging/src/k8s.io/kubectl.
		fh, err := snapshot.ReadFile(ctx, uri)
		if err != nil {
			return nil, false, err
		}
		data, err := fh.Content()
		if err != nil {
			return nil, false, err
		}
		m := protocol.NewMapper(uri, data)
		protocolEdits, err := ToProtocolEdits(m, edits)
		if err != nil {
			return nil, false, err
		}
		result[uri] = protocolEdits
	}

	return result, inPackageName, nil
}

// gopRenameOrdinary renames an ordinary (non-package) name throughout the workspace.
func gopRenameOrdinary(ctx context.Context, snapshot Snapshot, f FileHandle, pp protocol.Position, newName string) (map[span.URI][]diff.Edit, error) {
	// Type-check the referring package and locate the object(s).
	//
	// Unlike NarrowestPackageForFile, this operation prefers the
	// widest variant as, for non-exported identifiers, it is the
	// only package we need. (In case you're wondering why
	// 'references' doesn't also want the widest variant: it
	// computes the union across all variants.)
	var targets map[types.Object]ast.Node
	var pkg Package
	{
		metas, err := snapshot.MetadataForFile(ctx, f.URI())
		if err != nil {
			return nil, err
		}
		RemoveIntermediateTestVariants(&metas)
		if len(metas) == 0 {
			return nil, fmt.Errorf("no package metadata for file %s", f.URI())
		}
		widest := metas[len(metas)-1] // widest variant may include _test.go files
		pkgs, err := snapshot.TypeCheck(ctx, widest.ID)
		if err != nil {
			return nil, err
		}
		pkg = pkgs[0]
		pgf, err := pkg.GopFile(f.URI())
		if err != nil {
			return nil, err // "can't happen"
		}
		pos, err := pgf.PositionPos(pp)
		if err != nil {
			return nil, err
		}
		objects, _, err := gopObjectsAt(pkg.GopTypesInfo(), pgf.File, pos)
		if err != nil {
			return nil, err
		}
		targets = objects
	}

	// Pick a representative object arbitrarily.
	// (All share the same name, pos, and kind.)
	var obj types.Object
	for obj = range targets {
		break
	}
	if obj.Name() == newName {
		return nil, fmt.Errorf("old and new names are the same: %s", newName)
	}
	if err := checkRenamable(obj); err != nil {
		return nil, err
	}

	// Find objectpath, if object is exported ("" otherwise).
	var declObjPath objectpath.Path
	if obj.Exported() {
		// objectpath.For requires the origin of a generic function or type, not an
		// instantiation (a bug?). Unfortunately we can't call Func.Origin as this
		// is not available in go/types@go1.18. So we take a scenic route.
		//
		// Note that unlike Funcs, TypeNames are always canonical (they are "left"
		// of the type parameters, unlike methods).
		switch v := obj.(type) { // avoid "obj :=" since cases reassign the var
		case *types.TypeName:
			if _, ok := v.Type().(*typeparams.TypeParam); ok {
				// As with capitalized function parameters below, type parameters are
				// local.
				goto skipObjectPath
			}
		case *types.Func:
			obj = funcOrigin(v)
		case *types.Var:
			// TODO(adonovan): do vars need the origin treatment too? (issue #58462)

			// Function parameter and result vars that are (unusually)
			// capitalized are technically exported, even though they
			// cannot be referenced, because they may affect downstream
			// error messages. But we can safely treat them as local.
			//
			// This is not merely an optimization: the renameExported
			// operation gets confused by such vars. It finds them from
			// objectpath, the classifies them as local vars, but as
			// they came from export data they lack syntax and the
			// correct scope tree (issue #61294).
			if !v.IsField() && !isPackageLevel(obj) {
				goto skipObjectPath
			}
		}
		if path, err := objectpath.For(obj); err == nil {
			declObjPath = path
		}
	skipObjectPath:
	}

	// Nonexported? Search locally.
	if declObjPath == "" {
		var objects []types.Object
		for obj := range targets {
			objects = append(objects, obj)
		}
		editMap, _, err := renameObjects(ctx, snapshot, newName, pkg, objects...)
		return editMap, err
	}

	// Exported: search globally.
	//
	// For exported package-level var/const/func/type objects, the
	// search scope is just the direct importers.
	//
	// For exported fields and methods, the scope is the
	// transitive rdeps. (The exportedness of the field's struct
	// or method's receiver is irrelevant.)
	transitive := false
	switch v := obj.(type) {
	case *types.TypeName:
		// Renaming an exported package-level type
		// requires us to inspect all transitive rdeps
		// in the event that the type is embedded.
		//
		// TODO(adonovan): opt: this is conservative
		// but inefficient. Instead, expand the scope
		// of the search only if we actually encounter
		// an embedding of the type, and only then to
		// the rdeps of the embedding package.
		if v.Parent() == v.Pkg().Scope() {
			transitive = true
		}

	case *types.Var:
		if v.IsField() {
			transitive = true // field
		}

		// TODO(adonovan): opt: process only packages that
		// contain a reference (xrefs) to the target field.

	case *types.Func:
		if v.Type().(*types.Signature).Recv() != nil {
			transitive = true // method
		}

		// It's tempting to optimize by skipping
		// packages that don't contain a reference to
		// the method in the xrefs index, but we still
		// need to apply the satisfy check to those
		// packages to find assignment statements that
		// might expands the scope of the renaming.
	}

	// Type-check all the packages to inspect.
	declURI := span.URIFromPath(pkg.FileSet().File(obj.Pos()).Name())
	pkgs, err := typeCheckReverseDependencies(ctx, snapshot, declURI, transitive)
	if err != nil {
		return nil, err
	}

	// Apply the renaming to the (initial) object.
	declPkgPath := PackagePath(obj.Pkg().Path())
	return renameExported(ctx, snapshot, pkgs, declPkgPath, declObjPath, newName)
}

// gopRenamePackageName renames package declarations, imports, and go.mod files.
func gopRenamePackageName(ctx context.Context, s Snapshot, f FileHandle, newName PackageName) (map[span.URI][]diff.Edit, error) {
	log.Panicln("todo: Go+ files")
	return nil, nil
}

// Rename all references to the target objects.
func (r *renamer) gopUpdate(result map[span.URI][]diff.Edit) (map[span.URI][]diff.Edit, error) {
	// shouldUpdate reports whether obj is one of (or an
	// instantiation of one of) the target objects.
	shouldUpdate := func(obj types.Object) bool {
		if r.objsToUpdate[obj] {
			return true
		}
		if fn, ok := obj.(*types.Func); ok && r.objsToUpdate[funcOrigin(fn)] {
			return true
		}
		return false
	}

	// Find all identifiers in the package that define or use a
	// renamed object. We iterate over info as it is more efficient
	// than calling ast.Inspect for each of r.pkg.CompiledGoFiles().
	type item struct {
		node  ast.Node // Ident, ImportSpec (obj=PkgName), or CaseClause (obj=Var)
		obj   types.Object
		isDef bool
	}
	var items []item
	info := r.pkg.GopTypesInfo()
	if info == nil {
		return result, nil
	}
	for id, obj := range info.Uses {
		if shouldUpdate(obj) {
			items = append(items, item{id, obj, false})
		}
	}
	for id, obj := range info.Defs {
		if shouldUpdate(obj) {
			items = append(items, item{id, obj, true})
		}
	}
	for node, obj := range info.Implicits {
		if shouldUpdate(obj) {
			switch node.(type) {
			case *ast.ImportSpec, *ast.CaseClause:
				items = append(items, item{node, obj, true})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].node.Pos() < items[j].node.Pos()
	})

	// Update each identifier.
	for _, item := range items {
		pgf, ok := gopEnclosingFile(r.pkg, item.node.Pos())
		if !ok {
			bug.Reportf("edit does not belong to syntax of package %q", r.pkg)
			continue
		}

		// Renaming a types.PkgName may result in the addition or removal of an identifier,
		// so we deal with this separately.
		if pkgName, ok := item.obj.(*types.PkgName); ok && item.isDef {
			edit, err := r.gopUpdatePkgName(pgf, pkgName)
			if err != nil {
				return nil, err
			}
			result[pgf.URI] = append(result[pgf.URI], edit)
			continue
		}

		// Workaround the unfortunate lack of a Var object
		// for x in "switch x := expr.(type) {}" by adjusting
		// the case clause to the switch ident.
		// This may result in duplicate edits, but we de-dup later.
		if _, ok := item.node.(*ast.CaseClause); ok {
			path, _ := astutil.PathEnclosingInterval(pgf.File, item.obj.Pos(), item.obj.Pos())
			item.node = path[0].(*ast.Ident)
		}

		// Replace the identifier with r.to.
		edit, err := posEdit(pgf.Tok, item.node.Pos(), item.node.End(), r.to)
		if err != nil {
			return nil, err
		}

		result[pgf.URI] = append(result[pgf.URI], edit)

		if !item.isDef { // uses do not have doc comments to update.
			continue
		}

		doc := gopDocComment(pgf, item.node.(*ast.Ident))
		if doc == nil {
			continue
		}

		// Perform the rename in doc comments declared in the original package.
		// go/parser strips out \r\n returns from the comment text, so go
		// line-by-line through the comment text to get the correct positions.
		docRegexp := regexp.MustCompile(`\b` + r.from + `\b`) // valid identifier => valid regexp
		for _, comment := range doc.List {
			if isDirective(comment.Text) {
				continue
			}
			// TODO(adonovan): why are we looping over lines?
			// Just run the loop body once over the entire multiline comment.
			lines := strings.Split(comment.Text, "\n")
			tokFile := pgf.Tok
			commentLine := safetoken.Line(tokFile, comment.Pos())
			uri := span.URIFromPath(tokFile.Name())
			for i, line := range lines {
				lineStart := comment.Pos()
				if i > 0 {
					lineStart = tokFile.LineStart(commentLine + i)
				}
				for _, locs := range docRegexp.FindAllIndex([]byte(line), -1) {
					edit, err := posEdit(tokFile, lineStart+token.Pos(locs[0]), lineStart+token.Pos(locs[1]), r.to)
					if err != nil {
						return nil, err // can't happen
					}
					result[uri] = append(result[uri], edit)
				}
			}
		}
	}

	return result, nil
}

// gopDocComment returns the doc for an identifier within the specified file.
func gopDocComment(pgf *ParsedGopFile, id *ast.Ident) *ast.CommentGroup {
	nodes, _ := astutil.PathEnclosingInterval(pgf.File, id.Pos(), id.End())
	for _, node := range nodes {
		switch decl := node.(type) {
		case *ast.FuncDecl:
			return decl.Doc
		case *ast.Field:
			return decl.Doc
		case *ast.GenDecl:
			return decl.Doc
		// For {Type,Value}Spec, if the doc on the spec is absent,
		// search for the enclosing GenDecl
		case *ast.TypeSpec:
			if decl.Doc != nil {
				return decl.Doc
			}
		case *ast.ValueSpec:
			if decl.Doc != nil {
				return decl.Doc
			}
		case *ast.Ident:
		case *ast.AssignStmt:
			// *ast.AssignStmt doesn't have an associated comment group.
			// So, we try to find a comment just before the identifier.

			// Try to find a comment group only for short variable declarations (:=).
			if decl.Tok != token.DEFINE {
				return nil
			}

			identLine := safetoken.Line(pgf.Tok, id.Pos())
			for _, comment := range nodes[len(nodes)-1].(*ast.File).Comments {
				if comment.Pos() > id.Pos() {
					// Comment is after the identifier.
					continue
				}

				lastCommentLine := safetoken.Line(pgf.Tok, comment.End())
				if lastCommentLine+1 == identLine {
					return comment
				}
			}
		default:
			return nil
		}
	}
	return nil
}

// gopUpdatePkgName returns the updates to rename a pkgName in the import spec by
// only modifying the package name portion of the import declaration.
func (r *renamer) gopUpdatePkgName(pgf *ParsedGopFile, pkgName *types.PkgName) (diff.Edit, error) {
	// Modify ImportSpec syntax to add or remove the Name as needed.
	path, _ := astutil.PathEnclosingInterval(pgf.File, pkgName.Pos(), pkgName.Pos())
	if len(path) < 2 {
		return diff.Edit{}, fmt.Errorf("no path enclosing interval for %s", pkgName.Name())
	}
	spec, ok := path[1].(*ast.ImportSpec)
	if !ok {
		return diff.Edit{}, fmt.Errorf("failed to update PkgName for %s", pkgName.Name())
	}

	newText := ""
	if pkgName.Imported().Name() != r.to {
		newText = r.to + " "
	}

	// Replace the portion (possibly empty) of the spec before the path:
	//     local "path"      or      "path"
	//   ->      <-                -><-
	return posEdit(pgf.Tok, spec.Pos(), spec.Path.Pos(), newText)
}

// gopParsePackageNameDecl is a convenience function that parses and
// returns the package name declaration of file fh, and reports
// whether the position ppos lies within it.
//
// Note: also used by references.
func gopParsePackageNameDecl(ctx context.Context, snapshot Snapshot, fh FileHandle, ppos protocol.Position) (*ParsedGopFile, bool, error) {
	pgf, err := snapshot.ParseGop(ctx, fh, parserutil.ParseHeader)
	if err != nil {
		return nil, false, err
	}
	// Careful: because we used ParseHeader,
	// pgf.Pos(ppos) may be beyond EOF => (0, err).
	pos, _ := pgf.PositionPos(ppos)
	return pgf, pgf.HasPkgDecl() && pgf.File.Name.Pos() <= pos && pos <= pgf.File.Name.End(), nil
}

// gopEnclosingFile returns the CompiledGoFile of pkg that contains the specified position.
func gopEnclosingFile(pkg Package, pos token.Pos) (*ParsedGopFile, bool) {
	for _, pgf := range pkg.CompiledGopFiles() {
		if pgf.File.Pos() <= pos && pos <= pgf.File.End() {
			return pgf, true
		}
	}
	return nil, false
}
