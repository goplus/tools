// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parserutil

import (
	"github.com/goplus/gop"
	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/parser"
	"github.com/goplus/gop/token"
	"github.com/goplus/mod/gopmod"
)

const (
	// ParseHeader specifies that the main package declaration and imports are needed.
	// This is the mode used when attempting to examine the package graph structure.
	ParseHeader = parser.AllErrors | parser.ParseComments | parser.ImportsOnly

	// ParseFull specifies the full AST is needed.
	// This is used for files of direct interest where the entire contents must
	// be considered.
	ParseFull = parser.AllErrors | parser.ParseComments

	// SkipObjectResolution - don't resolve identifiers to objects - see ParseFile
	SkipObjectResolution = parser.SkipObjectResolution
)

func ParseFile(fset *token.FileSet, filename string, src interface{}, mode parser.Mode) (f *ast.File, err error) {
	return ParseFileEx(nil, fset, filename, src, mode)
}

func ParseFileEx(mod *gopmod.Module, fset *token.FileSet, filename string, src interface{}, mode parser.Mode) (f *ast.File, err error) {
	if filename != "" {
		conf := parser.Config{
			Mode: mode,
		}
		if mod != nil {
			conf.ClassKind = mod.ClassKind
		}
		f, err = parser.ParseEntry(fset, filename, src, conf)
		if err == parser.ErrUnknownFileKind { // fallback to a normal Go+ file
			f, err = parser.ParseFile(fset, filename, src, mode)
		}
	} else {
		f, err = parser.ParseFile(fset, filename, src, mode)
	}
	return
}

func GetClassType(file *ast.File, filename string, getMod func() (*gopmod.Module, error)) (classType string, isTest bool, err error) {
	var mod *gopmod.Module
	if file.IsProj {
		mod, err = getMod()
		if err != nil {
			return
		}
	}
	classType, isTest = gop.GetFileClassType(mod, file, filename)
	return
}
