// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package packages

import (
	"golang.org/x/tools/go/packages"
	internal "golang.org/x/tools/internal/packagesinternal"

	"golang.org/x/tools/gopls/internal/goxls/packagesinternal"
	"golang.org/x/tools/gopls/internal/goxls/typesutil"
)

// An Error describes a problem with a package's metadata, syntax, or types.
type Error = packages.Error

// A LoadMode controls the amount of detail to return when loading.
// The bits below can be combined to specify which fields should be
// filled in the result packages.
// The zero value is a special case, equivalent to combining
// the NeedName, NeedFiles, and NeedCompiledGoFiles bits.
// ID and Errors (if present) will always be filled.
// Load may return more information than requested.
type LoadMode = packages.LoadMode

const (
	// NeedName adds Name and PkgPath.
	NeedName = packages.NeedName

	// NeedFiles adds GoFiles, GopFiles and OtherFiles.
	NeedFiles = packages.NeedFiles

	// NeedCompiledGoFiles adds CompiledGoFiles/CompiledGopFiles.
	NeedCompiledGoFiles = packages.NeedCompiledGoFiles

	// NeedCompiledGopFiles adds CompiledGoFiles/CompiledGopFiles.
	NeedCompiledGopFiles = packages.NeedCompiledGoFiles

	// NeedImports adds Imports. If NeedDeps is not set, the Imports field will contain
	// "placeholder" Packages with only the ID set.
	NeedImports = packages.NeedImports

	// NeedDeps adds the fields requested by the LoadMode in the packages in Imports.
	NeedDeps = packages.NeedDeps

	// NeedExportFile adds ExportFile.
	NeedExportFile = packages.NeedExportFile

	// NeedTypes adds Types, Fset, and IllTyped.
	NeedTypes = packages.NeedTypes

	// NeedSyntax adds Syntax.
	NeedSyntax = packages.NeedSyntax

	// NeedTypesInfo adds TypesInfo.
	NeedTypesInfo = packages.NeedTypesInfo

	// NeedTypesSizes adds TypesSizes.
	NeedTypesSizes = packages.NeedTypesSizes

	// NeedModule adds Module.
	NeedModule = packages.NeedModule

	// NeedEmbedFiles adds EmbedFiles.
	NeedEmbedFiles = packages.NeedEmbedFiles

	// NeedEmbedPatterns adds EmbedPatterns.
	NeedEmbedPatterns = packages.NeedEmbedPatterns
)

// A Config specifies details about how packages should be loaded.
// The zero value is a valid configuration.
// Calls to Load do not modify this struct.
type Config = packages.Config

// A Package describes a loaded Go+ package.
type Package struct {
	packages.Package

	// GopFiles lists the absolute file paths of the package's Go source files.
	// It may include files that should not be compiled, for example because
	// they contain non-matching build tags, are documentary pseudo-files such as
	// unsafe/unsafe.go or builtin/builtin.go, or are subject to cgo preprocessing.
	GopFiles []string

	// CompiledGopFiles lists the absolute file paths of the package's source
	// files that are suitable for type checking.
	// This may differ from GoFiles if files are processed before compilation.
	CompiledGopFiles []string

	// Imports maps import paths appearing in the package's Go source files
	// to corresponding loaded Packages.
	Imports map[string]*Package

	// TypesInfo provides type information about the package's syntax trees.
	// It is set only when Syntax is set.
	TypesInfo *typesutil.Info
}

// Load loads and returns the Go packages named by the given patterns.
//
// Config specifies loading options;
// nil behaves the same as an empty Config.
//
// Load returns an error if any of the patterns was invalid
// as defined by the underlying build system.
// It may return an empty list of packages without an error,
// for instance for an empty expansion of a valid wildcard.
// Errors associated with a particular package are recorded in the
// corresponding Package's Errors list, and do not cause Load to
// return an error. Clients may need to handle such errors before
// proceeding with further analysis. The PrintErrors function is
// provided for convenient display of all errors.
func Load(cfg *Config, patterns ...string) ([]*Package, error) {
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	ret := make([]*Package, len(pkgs))
	for i, pkg := range pkgs {
		ret[i] = &Package{Package: *pkg, Imports: importPkgs(pkg.Imports)}
	}
	return ret, nil
}

func importPkgs(pkgs map[string]*packages.Package) map[string]*Package {
	if len(pkgs) == 0 {
		return nil
	}
	ret := make(map[string]*Package, len(pkgs))
	for path, pkg := range pkgs {
		ret[path] = &Package{Package: *pkg}
	}
	return ret
}

func init() {
	packagesinternal.GetForTest = func(p interface{}) string {
		return internal.GetForTest(&p.(*Package).Package)
	}
	packagesinternal.GetDepsErrors = func(p interface{}) []*packagesinternal.PackageError {
		return internal.GetDepsErrors(&p.(*Package).Package)
	}
	packagesinternal.GetGoCmdRunner = internal.GetGoCmdRunner
	packagesinternal.SetGoCmdRunner = internal.SetGoCmdRunner
	packagesinternal.SetModFile = internal.SetModFile
	packagesinternal.SetModFlag = internal.SetModFlag
	packagesinternal.TypecheckCgo = internal.TypecheckCgo
	packagesinternal.DepsErrors = internal.DepsErrors
	packagesinternal.ForTest = internal.ForTest
}
