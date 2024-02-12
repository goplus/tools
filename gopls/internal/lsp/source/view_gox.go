// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"fmt"
	"go/types"
	"path/filepath"

	"github.com/goplus/gop"
	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/parser"
	"github.com/goplus/gop/scanner"
	"github.com/goplus/gop/token"
	"github.com/goplus/gop/x/gopenv"
	"github.com/goplus/mod/gopmod"
	"golang.org/x/tools/gop/packages"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/safetoken"
	"golang.org/x/tools/gopls/internal/span"
)

func (m *Metadata) Dir() string {
	var uri span.URI
	if len(m.GoFiles) > 0 {
		uri = m.GoFiles[0]
	} else if len(m.GopFiles) > 0 {
		uri = m.GopFiles[0]
	} else {
		return ""
	}
	return filepath.Dir(uri.Filename())
}

/*
// CompiledNongenGoFiles returns all Go files excluding "gop_autogen*.go".
func (m *Metadata) CompiledNongenGoFiles() []span.URI {
	ret := make([]span.URI, 0, len(m.CompiledGoFiles))
	for _, f := range m.CompiledGoFiles {
		fname := filepath.Base(f.Filename())
		if strings.HasPrefix(fname, "gop_autogen") {
			continue
		}
		ret = append(ret, f)
	}
	return ret
}
*/

// A ParsedGopFile contains the results of parsing a Go+ file.
type ParsedGopFile struct {
	URI  span.URI
	Mode parser.Mode
	File *ast.File
	Tok  *token.File
	// Source code used to build the AST. It may be different from the
	// actual content of the file if we have fixed the AST.
	Src []byte

	// FixedSrc and Fixed AST report on "fixing" that occurred during parsing of
	// this file.
	//
	// If FixedSrc == true, the source contained in the Src field was modified
	// from the original source to improve parsing.
	//
	// If FixedAST == true, the ast was modified after parsing, and therefore
	// positions encoded in the AST may not accurately represent the content of
	// the Src field.
	//
	// TODO(rfindley): there are many places where we haphazardly use the Src or
	// positions without checking these fields. Audit these places and guard
	// accordingly. After doing so, we may find that we don't need to
	// differentiate FixedSrc and FixedAST.
	FixedSrc bool
	FixedAST bool
	Mapper   *protocol.Mapper // may map fixed Src, not file content
	ParseErr scanner.ErrorList
}

// Fixed reports whether p was "Fixed", meaning that its source or positions
// may not correlate with the original file.
func (p ParsedGopFile) Fixed() bool {
	return p.FixedSrc || p.FixedAST
}

// HasPkgDecl checks if `package xxx` exists or not.
func (pgf *ParsedGopFile) HasPkgDecl() bool {
	return pgf.File.Package != token.NoPos
}

// PositionPos returns the token.Pos of protocol position p within the file.
func (pgf *ParsedGopFile) PositionPos(p protocol.Position) (token.Pos, error) {
	offset, err := pgf.Mapper.PositionOffset(p)
	if err != nil {
		return token.NoPos, err
	}
	return safetoken.Pos(pgf.Tok, offset)
}

// NodeRange returns a protocol Range for the ast.Node interval in this file.
func (pgf *ParsedGopFile) NodeRange(node ast.Node) (protocol.Range, error) {
	return pgf.Mapper.NodeRange(pgf.Tok, node)
}

// NodeLocation returns a protocol Location for the ast.Node interval in this file.
func (pgf *ParsedGopFile) NodeLocation(node ast.Node) (protocol.Location, error) {
	return pgf.Mapper.PosLocation(pgf.Tok, node.Pos(), node.End())
}

// RangePos parses a protocol Range back into the go/token domain.
func (pgf *ParsedGopFile) RangePos(r protocol.Range) (token.Pos, token.Pos, error) {
	start, end, err := pgf.Mapper.RangeOffsets(r)
	if err != nil {
		return token.NoPos, token.NoPos, err
	}
	return pgf.Tok.Pos(start), pgf.Tok.Pos(end), nil
}

// PosRange returns a protocol Range for the token.Pos interval in this file.
func (pgf *ParsedGopFile) PosRange(start, end token.Pos) (protocol.Range, error) {
	return pgf.Mapper.PosRange(pgf.Tok, start, end)
}

// PosLocation returns a protocol Location for the token.Pos interval in this file.
func (pgf *ParsedGopFile) PosLocation(start, end token.Pos) (protocol.Location, error) {
	return pgf.Mapper.PosLocation(pgf.Tok, start, end)
}

func (m *Metadata) LoadGopMod() {
	m.gopMod_, _ = gop.LoadMod(m.LoadDir)
}

func (m *Metadata) GopMod_() *gopmod.Module {
	if m.gopMod_ == nil {
		m.gopMod_ = packages.Default.LoadMod(m.Module)
	}
	return m.gopMod_
}

func (m *Metadata) GopImporter(fset *token.FileSet) types.Importer {
	if m.gopImporter == nil {
		m.gopImporter = gop.NewImporter(m.GopMod_(), gopenv.Get(), fset)
	}
	return m.gopImporter
}

// NarrowestPackageForGopFile is a convenience function that selects the
// narrowest non-ITV package to which this file belongs, type-checks
// it in the requested mode (full or workspace), and returns it, along
// with the parse tree of that file.
//
// The "narrowest" package is the one with the fewest number of files
// that includes the given file. This solves the problem of test
// variants, as the test will have more files than the non-test package.
// (Historically the preference was a parameter but widest was almost
// never needed.)
//
// An intermediate test variant (ITV) package has identical source
// to a regular package but resolves imports differently.
// gopls should never need to type-check them.
//
// Type-checking is expensive. Call snapshot.ParseGo if all you need
// is a parse tree, or snapshot.MetadataForFile if you only need metadata.
func NarrowestPackageForGopFile(ctx context.Context, snapshot Snapshot, uri span.URI) (Package, *ParsedGopFile, error) {
	metas, err := snapshot.MetadataForFile(ctx, uri)
	if err != nil {
		return nil, nil, err
	}
	RemoveIntermediateTestVariants(&metas)
	if len(metas) == 0 {
		return nil, nil, fmt.Errorf("no package metadata for file %s", uri)
	}
	narrowest := metas[0]
	pkgs, err := snapshot.TypeCheck(ctx, narrowest.ID)
	if err != nil {
		return nil, nil, err
	}
	pkg := pkgs[0]
	pgf, err := pkg.GopFile(uri)
	if err != nil {
		return nil, nil, err // "can't happen"
	}
	return pkg, pgf, err
}
