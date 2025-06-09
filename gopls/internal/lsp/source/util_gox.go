// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"go/types"
	"strconv"
	"strings"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/cl"
	"github.com/goplus/gop/printer"
	"github.com/goplus/gop/token"
	"github.com/goplus/gop/x/typesutil"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/gop/typeparams"
	"golang.org/x/tools/internal/tokeninternal"
)

// GopFormatNode returns the "pretty-print" output for an ast node.
func GopFormatNode(fset *token.FileSet, n ast.Node) string {
	var buf strings.Builder
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return ""
	}
	return buf.String()
}

// GopFormatNodeFile is like FormatNode, but requires only the token.File for the
// syntax containing the given ast node.
func GopFormatNodeFile(file *token.File, n ast.Node) string {
	fset := tokeninternal.FileSetFor(file)
	return GopFormatNode(fset, n)
}

// GopCollectScopes returns all scopes in an ast path, ordered as innermost scope
// first.
func GopCollectScopes(info *typesutil.Info, path []ast.Node, pos token.Pos) []*types.Scope {
	// scopes[i], where i<len(path), is the possibly nil Scope of path[i].
	var scopes []*types.Scope
	for _, n := range path {
		// Include *FuncType scope if pos is inside the function body.
		switch node := n.(type) {
		case *ast.FuncDecl:
			if node.Body != nil && NodeContains(node.Body, pos) {
				n = node.Type
			}
		case *ast.FuncLit:
			if node.Body != nil && NodeContains(node.Body, pos) {
				n = node.Type
			}
		}
		scopes = append(scopes, info.Scopes[n])
	}
	return scopes
}

// MetadataQualifierForGopFile returns a metadata qualifier that chooses the best
// qualification of an imported package relative to the file f in package with
// metadata m.
func MetadataQualifierForGopFile(s MetadataSource, f *ast.File, m *Metadata) MetadataQualifier {
	// Record local names for import paths.
	localNames := make(map[ImportPath]string) // local names for imports in f
	for _, imp := range f.Imports {
		name, _, impPath, _ := gopImportInfo(s, imp, m)
		localNames[impPath] = name
	}

	// Record a package path -> import path mapping.
	inverseDeps := make(map[PackageID]PackagePath)
	for path, id := range m.DepsByPkgPath {
		inverseDeps[id] = path
	}
	importsByPkgPath := make(map[PackagePath]ImportPath) // best import paths by pkgPath
	for impPath, id := range m.DepsByImpPath {
		if id == "" {
			continue
		}
		pkgPath := inverseDeps[id]
		_, hasPath := importsByPkgPath[pkgPath]
		_, hasImp := localNames[impPath]
		// In rare cases, there may be multiple import paths with the same package
		// path. In such scenarios, prefer an import path that already exists in
		// the file.
		if !hasPath || hasImp {
			importsByPkgPath[pkgPath] = impPath
		}
	}

	return func(pkgName PackageName, impPath ImportPath, pkgPath PackagePath) string {
		// If supplied, translate the package path to an import path in the source
		// package.
		if pkgPath != "" {
			if srcImp := importsByPkgPath[pkgPath]; srcImp != "" {
				impPath = srcImp
			}
			if pkgPath == m.PkgPath {
				return ""
			}
		}
		if localName, ok := localNames[impPath]; ok && impPath != "" {
			return string(localName)
		}
		if pkgName != "" {
			return string(pkgName)
		}
		idx := strings.LastIndexByte(string(impPath), '/')
		return string(impPath[idx+1:])
	}
}

// gopImportInfo collects information about the import specified by imp,
// extracting its file-local name, package name, import path, and package path.
//
// If metadata is missing for the import, the resulting package name and
// package path may be empty, and the file local name may be guessed based on
// the import path.
//
// Note: previous versions of this helper used a PackageID->PackagePath map
// extracted from m, for extracting package path even in the case where
// metadata for a dep was missing. This should not be necessary, as we should
// always have metadata for IDs contained in DepsByPkgPath.
func gopImportInfo(s MetadataSource, imp *ast.ImportSpec, m *Metadata) (string, PackageName, ImportPath, PackagePath) {
	var (
		name    string // local name
		pkgName PackageName
		impPath = GopUnquoteImportPath(imp)
		pkgPath PackagePath
	)

	// If the import has a local name, use it.
	if imp.Name != nil {
		name = imp.Name.Name
	}

	// Try to find metadata for the import. If successful and there is no local
	// name, the package name is the local name.
	if depID := m.DepsByImpPath[impPath]; depID != "" {
		if depm := s.Metadata(depID); depm != nil {
			if name == "" {
				name = string(depm.Name)
			}
			pkgName = depm.Name
			pkgPath = depm.PkgPath
		}
	}

	// If the local name is still unknown, guess it based on the import path.
	if name == "" {
		idx := strings.LastIndexByte(string(impPath), '/')
		name = string(impPath[idx+1:])
	}
	return name, pkgName, impPath, pkgPath
}

// IsGopGenerated gets and reads the file denoted by uri and reports
// whether it contains a "generated file" comment as described at
// https://golang.org/s/generatedcode.
//
// TODO(adonovan): opt: this function does too much.
// Move snapshot.ReadFile into the caller (most of which have already done it).
func IsGopGenerated(ctx context.Context, snapshot Snapshot, uri span.URI) bool {
	return false
}

/*
// gopFindFileInDeps finds package metadata containing URI in the transitive
// dependencies of m. When using the Gop command, the answer is unique.
//
// TODO(rfindley): refactor to share logic with findPackageInDeps?
func gopFindFileInDeps(s MetadataSource, m *Metadata, uri span.URI) *Metadata {
	seen := make(map[PackageID]bool)
	var search func(*Metadata) *Metadata
	search = func(m *Metadata) *Metadata {
		if seen[m.ID] {
			return nil
		}
		seen[m.ID] = true
		for _, cgf := range m.CompiledGopFiles {
			if cgf == uri {
				return m
			}
		}
		for _, cgf := range m.CompiledGoFiles {
			if cgf == uri {
				return m
			}
		}
		for _, dep := range m.DepsByPkgPath {
			m := s.Metadata(dep)
			if m == nil {
				bug.Reportf("nil metadata for %q", dep)
				continue
			}
			if found := search(m); found != nil {
				return found
			}
		}
		return nil
	}
	return search(m)
}
*/

// GopUnquoteImportPath returns the unquoted import path of s,
// or "" if the path is not properly quoted.
func GopUnquoteImportPath(s *ast.ImportSpec) ImportPath {
	path, err := strconv.Unquote(s.Path.Value)
	if err != nil {
		return ""
	}
	return ImportPath(path)
}

// GopQualifier returns a function that appropriately formats a types.PkgName
// appearing in a *ast.File.
func GopQualifier(f *ast.File, pkg *types.Package, info *typesutil.Info) types.Qualifier {
	// Construct mapping of import paths to their defined or implicit names.
	imports := make(map[*types.Package]string)
	for _, imp := range f.Imports {
		if pkgname, ok := GopImportedPkgName(info, imp); ok {
			imports[pkgname.Imported()] = pkgname.Name()
		}
	}
	// Define qualifier to replace full package paths with names of the imports.
	return func(p *types.Package) string {
		if p == pkg {
			return ""
		}
		if name, ok := imports[p]; ok {
			if name == "." {
				return ""
			}
			return name
		}
		return p.Name()
	}
}

// gopRequalifier returns a function that re-qualifies identifiers and qualified
// identifiers contained in targetFile using the given metadata qualifier.
func gopRequalifier(s MetadataSource, targetFile *ast.File, targetMeta *Metadata, mq MetadataQualifier) func(string) string {
	qm := map[string]string{
		"": mq(targetMeta.Name, "", targetMeta.PkgPath),
	}

	// Construct mapping of import paths to their defined or implicit names.
	for _, imp := range targetFile.Imports {
		name, pkgName, impPath, pkgPath := gopImportInfo(s, imp, targetMeta)

		// Re-map the target name for the source file.
		qm[name] = mq(pkgName, impPath, pkgPath)
	}

	return func(name string) string {
		if newName, ok := qm[name]; ok {
			return newName
		}
		return name
	}
}

// gopEmbeddedIdent returns the type name identifier for an embedding x, if x in a
// valid embedding. Otherwise, it returns nil.
//
// Spec: An embedded field must be specified as a type name T or as a pointer
// to a non-interface type name *T
func gopEmbeddedIdent(x ast.Expr) *ast.Ident {
	if star, ok := x.(*ast.StarExpr); ok {
		x = star.X
	}
	switch ix := x.(type) { // check for instantiated receivers
	case *ast.IndexExpr:
		x = ix.X
	case *typeparams.IndexListExpr:
		x = ix.X
	}
	switch x := x.(type) {
	case *ast.Ident:
		return x
	case *ast.SelectorExpr:
		if _, ok := x.X.(*ast.Ident); ok {
			return x.Sel
		}
	}
	return nil
}

func GoxTestClassType(file *ast.File, filename string) (classType string, isGoxTest bool) {
	if file.IsClass {
		var ext string
		classType, _, ext = cl.ClassNameAndExt(filename)
		if file.IsNormalGox {
			isGoxTest = strings.HasSuffix(ext, "_test.gox")
		} else {
			isGoxTest = strings.HasSuffix(ext, "test.gox")
		}
	}
	return
}
