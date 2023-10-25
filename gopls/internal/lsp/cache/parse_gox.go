// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"

	goplsastutil "golang.org/x/tools/gopls/internal/astutil"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/parser"
	"github.com/goplus/gop/scanner"
	"github.com/goplus/gop/token"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/tag"
)

// ParseGop parses the file whose contents are provided by fh, using a cache.
// The resulting tree may have beeen fixed up.
func (s *snapshot) ParseGop(ctx context.Context, fh source.FileHandle, mode parser.Mode) (*source.ParsedGopFile, error) {
	pgfs, err := s.view.parseCache.parseGopFiles(ctx, token.NewFileSet(), mode, false, fh)
	if err != nil {
		return nil, err
	}
	return pgfs[0], nil
}

// parseGopImpl parses the Go+ source file whose content is provided by fh.
func parseGopImpl(ctx context.Context, fset *token.FileSet, fh source.FileHandle, mode parser.Mode, purgeFuncBodies bool) (*source.ParsedGopFile, error) {
	/*
		// goxls: don't check Go+ files by extension
		ext := filepath.Ext(fh.URI().Filename())
		if ext != ".go" && ext != "" { // files generated by cgo have no extension
			return nil, fmt.Errorf("cannot parse non-Go+ file %s", fh.URI())
		}
	*/
	content, err := fh.Content()
	if err != nil {
		return nil, err
	}
	// Check for context cancellation before actually doing the parse.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	pgf, _ := ParseGopSrc(ctx, fset, fh.URI(), content, mode, purgeFuncBodies)
	return pgf, nil
}

// ParseGopSrc parses a buffer of Go+ source, repairing the tree if necessary.
//
// The provided ctx is used only for logging.
func ParseGopSrc(ctx context.Context, fset *token.FileSet, uri span.URI, src []byte, mode parser.Mode, purgeFuncBodies bool) (res *source.ParsedGopFile, fixes []fixType) {
	if purgeFuncBodies {
		src = goplsastutil.PurgeFuncBodies(src)
	}
	ctx, done := event.Start(ctx, "cache.ParseGopSrc", tag.File.Of(uri.Filename()))
	defer done()

	file, err := parser.ParseFile(fset, uri.Filename(), src, mode)
	var parseErr scanner.ErrorList
	if err != nil {
		// We passed a byte slice, so the only possible error is a parse error.
		parseErr = err.(scanner.ErrorList)
	}

	tok := fset.File(file.Pos())
	if tok == nil {
		// file.Pos is the location of the package declaration (issue #53202). If there was
		// none, we can't find the token.File that ParseFile created, and we
		// have no choice but to recreate it.
		tok = fset.AddFile(uri.Filename(), -1, len(src))
		tok.SetLinesForContent(src)
	}

	fixedSrc := false
	fixedAST := false
	// If there were parse errors, attempt to fix them up.
	if parseErr != nil {
		// Fix any badly parsed parts of the AST.
		astFixes := fixGopAST(file, tok, src)
		fixedAST = len(fixes) > 0
		if fixedAST {
			fixes = append(fixes, astFixes...)
		}

		for i := 0; i < 10; i++ {
			// Fix certain syntax errors that render the file unparseable.
			newSrc, srcFix := fixGopSrc(file, tok, src)
			if newSrc == nil {
				break
			}

			// If we thought there was something to fix 10 times in a row,
			// it is likely we got stuck in a loop somehow. Log out a diff
			// of the last changes we made to aid in debugging.
			if i == 9 {
				unified := diff.Unified("before", "after", string(src), string(newSrc))
				event.Log(ctx, fmt.Sprintf("fixGopSrc loop - last diff:\n%v", unified), tag.File.Of(tok.Name()))
			}

			newFile, newErr := parser.ParseFile(fset, uri.Filename(), newSrc, mode)
			if newFile == nil {
				break // no progress
			}

			// Maintain the original parseError so we don't try formatting the
			// doctored file.
			file = newFile
			src = newSrc
			tok = fset.File(file.Pos())

			// Only now that we accept the fix do we record the src fix from above.
			fixes = append(fixes, srcFix)
			fixedSrc = true

			if newErr == nil {
				break // nothing to fix
			}

			// Note that fixedAST is reset after we fix src.
			astFixes = fixGopAST(file, tok, src)
			fixedAST = len(astFixes) > 0
			if fixedAST {
				fixes = append(fixes, astFixes...)
			}
		}
	}

	return &source.ParsedGopFile{
		URI:      uri,
		Mode:     mode,
		Src:      src,
		FixedSrc: fixedSrc,
		FixedAST: fixedAST,
		File:     file,
		Tok:      tok,
		Mapper:   protocol.NewMapper(uri, src),
		ParseErr: parseErr,
	}, fixes
}

// fixGopAST inspects the AST and potentially modifies any *ast.BadStmts so that it can be
// type-checked more effectively.
//
// If fixGopAST returns true, the resulting AST is considered "fixed", meaning
// positions have been mangled, and type checker errors may not make sense.
func fixGopAST(n ast.Node, tok *token.File, src []byte) (fixes []fixType) {
	return
}

// fixGopSrc attempts to modify the file's source code to fix certain
// syntax errors that leave the rest of the file unparsed.
//
// fixSrc returns a non-nil result if and only if a fix was applied.
func fixGopSrc(f *ast.File, tf *token.File, src []byte) (newSrc []byte, fix fixType) {
	return
}
