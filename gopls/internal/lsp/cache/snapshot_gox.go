// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"go/token"

	"github.com/goplus/gop/ast"
	"github.com/goplus/mod/gopmod"
	"golang.org/x/tools/gop/goputil"
	"golang.org/x/tools/gopls/internal/bug"
	"golang.org/x/tools/gopls/internal/goxls/parserutil"
	"golang.org/x/tools/gopls/internal/lsp/source"
)

func checkGopFile(fh source.FileHandle, fext string) bool {
	return goputil.FileKind(fext) != goputil.FileUnknown
}

func gopExtensions() string {
	return goputil.Exts()
}

// gopMetadataChanges detects features of the change from oldFH->newFH that may
// affect package metadata.
//
// It uses lockedSnapshot to access cached parse information. lockedSnapshot
// must be locked.
//
// The result parameters have the following meaning:
//   - invalidate means that package metadata for packages containing the file
//     should be invalidated.
//   - pkgFileChanged means that the file->package associates for the file have
//     changed (possibly because the file is new, or because its package name has
//     changed).
//   - importDeleted means that an import has been deleted, or we can't
//     determine if an import was deleted due to errors.
func gopMetadataChanges(ctx context.Context, lockedSnapshot *snapshot, oldFH, newFH source.FileHandle) (invalidate, pkgFileChanged, importDeleted bool) {
	if oldFH == nil || newFH == nil { // existential changes
		changed := (oldFH == nil) != (newFH == nil)
		return changed, changed, (newFH == nil) // we don't know if an import was deleted
	}

	// If the file hasn't changed, there's no need to reload.
	if oldFH.FileIdentity() == newFH.FileIdentity() {
		return false, false, false
	}

	// check locked snapshot mod for uri
	ids := lockedSnapshot.meta.ids[oldFH.URI()]
	var mod *gopmod.Module
	for _, id := range ids {
		if m := lockedSnapshot.meta.metadata[id]; m != nil {
			mod = m.GopMod_()
			if mod != nil {
				break
			}
		}
	}

	fset := token.NewFileSet()
	// Parse headers to compare package names and imports.
	oldHeads, oldErr := lockedSnapshot.view.parseCache.parseGopFiles(ctx, mod, fset, parserutil.ParseHeader, false, oldFH)
	newHeads, newErr := lockedSnapshot.view.parseCache.parseGopFiles(ctx, mod, fset, parserutil.ParseHeader, false, newFH)

	if oldErr != nil || newErr != nil {
		// TODO(rfindley): we can get here if newFH does not exist. There is
		// asymmetry, in that newFH may be non-nil even if the underlying file does
		// not exist.
		//
		// We should not produce a non-nil filehandle for a file that does not exist.
		errChanged := (oldErr == nil) != (newErr == nil)
		return errChanged, errChanged, (newErr != nil) // we don't know if an import was deleted
	}

	oldHead := oldHeads[0]
	newHead := newHeads[0]

	// `go list` fails completely if the file header cannot be parsed. If we go
	// from a non-parsing state to a parsing state, we should reload.
	if oldHead.ParseErr != nil && newHead.ParseErr == nil {
		return true, true, true // We don't know what changed, so fall back on full invalidation.
	}

	// If a package name has changed, the set of package imports may have changed
	// in ways we can't detect here. Assume an import has been deleted.
	if oldHead.File.Name.Name != newHead.File.Name.Name {
		return true, true, true
	}

	// Check whether package imports have changed. Only consider potentially
	// valid imports paths.
	oldImports := validGopImports(oldHead.File.Imports)
	newImports := validGopImports(newHead.File.Imports)

	for path := range newImports {
		if _, ok := oldImports[path]; ok {
			delete(oldImports, path)
		} else {
			invalidate = true // a new, potentially valid import was added
		}
	}

	if len(oldImports) > 0 {
		invalidate = true
		importDeleted = true
	}

	// If the change does not otherwise invalidate metadata, get the full ASTs in
	// order to check magic comments.
	//
	// Note: if this affects performance we can probably avoid parsing in the
	// common case by first scanning the source for potential comments.
	if !invalidate {
		origFulls, oldErr := lockedSnapshot.view.parseCache.parseGopFiles(ctx, mod, fset, parserutil.ParseFull, false, oldFH)
		newFulls, newErr := lockedSnapshot.view.parseCache.parseGopFiles(ctx, mod, fset, parserutil.ParseFull, false, newFH)
		if oldErr == nil && newErr == nil {
			invalidate = gopMagicCommentsChanged(origFulls[0].File, newFulls[0].File)
		} else {
			// At this point, we shouldn't ever fail to produce a ParsedGoFile, as
			// we're already past header parsing.
			bug.Reportf("metadataChanges: unparseable file %v (old error: %v, new error: %v)", oldFH.URI(), oldErr, newErr)
		}
	}

	return invalidate, pkgFileChanged, importDeleted
}

// validGopImports extracts the set of valid import paths from imports.
func validGopImports(imports []*ast.ImportSpec) map[string]struct{} {
	m := make(map[string]struct{})
	for _, spec := range imports {
		if path := spec.Path.Value; validImportPath(path) {
			m[path] = struct{}{}
		}
	}
	return m
}

func gopMagicCommentsChanged(original *ast.File, current *ast.File) bool {
	oldComments := extractGopMagicComments(original)
	newComments := extractGopMagicComments(current)
	if len(oldComments) != len(newComments) {
		return true
	}
	for i := range oldComments {
		if oldComments[i] != newComments[i] {
			return true
		}
	}
	return false
}

// extractGopMagicComments finds magic comments that affect metadata in f.
func extractGopMagicComments(f *ast.File) []string {
	var results []string
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if buildConstraintOrEmbedRe.MatchString(c.Text) {
				results = append(results, c.Text)
			}
		}
	}
	return results
}
