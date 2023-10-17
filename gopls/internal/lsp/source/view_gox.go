// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/parser"
	"github.com/goplus/gop/scanner"
	"github.com/goplus/gop/token"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/safetoken"
	"golang.org/x/tools/gopls/internal/span"
)

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

/*
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
*/
