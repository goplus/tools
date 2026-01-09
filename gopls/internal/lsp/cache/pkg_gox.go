// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"fmt"

	"github.com/goplus/xgo/x/typesutil"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/span"
)

func (p *Package) GopTypesInfo() *typesutil.Info {
	return p.pkg.gopTypesInfo
}

/*
// CompiledNongenGoFiles returns all Go files excluding "xgo_autogen*.go".
func (p *Package) CompiledNongenGoFiles() []*source.ParsedGoFile {
	gofs := p.pkg.compiledGoFiles
	ret := make([]*source.ParsedGoFile, 0, len(gofs))
	for _, f := range gofs {
		fname := filepath.Base(f.URI.Filename())
		if strings.HasPrefix(fname, "xgo_autogen") {
			continue
		}
		ret = append(ret, f)
	}
	return ret
}

func (p *Package) GetNongenSyntax() []*goast.File {
	gofs := p.pkg.compiledGoFiles
	ret := make([]*goast.File, 0, len(gofs))
	for _, f := range gofs {
		fname := filepath.Base(f.URI.Filename())
		if strings.HasPrefix(fname, "xgo_autogen") {
			continue
		}
		ret = append(ret, f.File)
	}
	return ret
}
*/

func (p *Package) CompiledGopFiles() []*source.ParsedGopFile {
	return p.pkg.compiledGopFiles
}

func (p *Package) GopFile(uri span.URI) (*source.ParsedGopFile, error) {
	return p.pkg.GopFile(uri)
}

func (pkg *syntaxPackage) GopFile(uri span.URI) (*source.ParsedGopFile, error) {
	for _, cgf := range pkg.compiledGopFiles {
		if cgf.URI == uri {
			return cgf, nil
		}
	}
	for _, gf := range pkg.gopFiles {
		if gf.URI == uri {
			return gf, nil
		}
	}
	return nil, fmt.Errorf("no parsed file for %s in %v", uri, pkg.id)
}
