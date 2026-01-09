// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/goplus/xgo/ast"
	"github.com/goplus/xgo/parser"
	"github.com/goplus/xgo/token"
	"golang.org/x/tools/gopls/internal/goxls/parserutil"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/safetoken"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/span"
)

// gopPackageClauseCompletions offers completions for a package declaration when
// one is not present in the given file.
func gopPackageClauseCompletions(ctx context.Context, snapshot source.Snapshot, fh source.FileHandle, position protocol.Position) ([]CompletionItem, *Selection, error) {
	// We know that the AST for this file will be empty due to the missing
	// package declaration, but parse it anyway to get a mapper.
	// TODO(adonovan): opt: there's no need to parse just to get a mapper.
	pgf, err := snapshot.ParseGop(ctx, fh, parserutil.ParseFull)
	if err != nil {
		return nil, nil, err
	}

	offset, err := pgf.Mapper.PositionOffset(position)
	if err != nil {
		return nil, nil, err
	}
	surrounding, err := gopPackageCompletionSurrounding(pgf, offset)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid position for package completion: %w", err)
	}

	packageSuggestions, err := packageSuggestions(ctx, snapshot, fh.URI(), "")
	if err != nil {
		return nil, nil, err
	}

	var items []CompletionItem
	for _, pkg := range packageSuggestions {
		insertText := fmt.Sprintf("package %s", pkg.name)
		items = append(items, CompletionItem{
			Label:      insertText,
			Kind:       protocol.ModuleCompletion,
			InsertText: insertText,
			Score:      pkg.score,
		})
	}

	return items, surrounding, nil
}

// gopPackageCompletionSurrounding returns surrounding for package completion if a
// package completions can be suggested at a given cursor offset. A valid location
// for package completion is above any declarations or import statements.
func gopPackageCompletionSurrounding(pgf *source.ParsedGopFile, offset int) (*Selection, error) {
	m := pgf.Mapper
	// If the file lacks a package declaration, the parser will return an empty
	// AST. As a work-around, try to parse an expression from the file contents.
	fset := token.NewFileSet()
	expr, _ := parser.ParseExprFrom(fset, m.URI.Filename(), pgf.Src, parser.Mode(0))
	if expr == nil {
		return nil, fmt.Errorf("unparseable file (%s)", m.URI)
	}
	tok := fset.File(expr.Pos())
	cursor := tok.Pos(offset)

	// If we were able to parse out an identifier as the first expression from
	// the file, it may be the beginning of a package declaration ("pack ").
	// We can offer package completions if the cursor is in the identifier.
	if name, ok := expr.(*ast.Ident); ok {
		if cursor >= name.Pos() && cursor <= name.End() {
			if !strings.HasPrefix(PACKAGE, name.Name) {
				return nil, fmt.Errorf("cursor in non-matching ident")
			}
			return &Selection{
				content: name.Name,
				cursor:  cursor,
				tokFile: tok,
				start:   name.Pos(),
				end:     name.End(),
				mapper:  m,
			}, nil
		}
	}

	// The file is invalid, but it contains an expression that we were able to
	// parse. We will use this expression to construct the cursor's
	// "surrounding".

	// First, consider the possibility that we have a valid "package" keyword
	// with an empty package name ("package "). "package" is parsed as an
	// *ast.BadDecl since it is a keyword. This logic would allow "package" to
	// appear on any line of the file as long as it's the first code expression
	// in the file.
	lines := strings.Split(string(pgf.Src), "\n")
	cursorLine := safetoken.Line(tok, cursor)
	if cursorLine <= 0 || cursorLine > len(lines) {
		return nil, fmt.Errorf("invalid line number")
	}
	if safetoken.StartPosition(fset, expr.Pos()).Line == cursorLine {
		words := strings.Fields(lines[cursorLine-1])
		if len(words) > 0 && words[0] == PACKAGE {
			content := PACKAGE
			// Account for spaces if there are any.
			if len(words) > 1 {
				content += " "
			}

			start := expr.Pos()
			end := token.Pos(int(expr.Pos()) + len(content) + 1)
			// We have verified that we have a valid 'package' keyword as our
			// first expression. Ensure that cursor is in this keyword or
			// otherwise fallback to the general case.
			if cursor >= start && cursor <= end {
				return &Selection{
					content: content,
					cursor:  cursor,
					tokFile: tok,
					start:   start,
					end:     end,
					mapper:  m,
				}, nil
			}
		}
	}

	// If the cursor is after the start of the expression, no package
	// declaration will be valid.
	if cursor > expr.Pos() {
		return nil, fmt.Errorf("cursor after expression")
	}

	// If the cursor is in a comment, don't offer any completions.
	if cursorInComment(tok, cursor, m.Content) {
		return nil, fmt.Errorf("cursor in comment")
	}

	// The surrounding range in this case is the cursor.
	return &Selection{
		content: "",
		tokFile: tok,
		start:   cursor,
		end:     cursor,
		cursor:  cursor,
		mapper:  m,
	}, nil
}

// packageNameCompletions returns name completions for a package clause using
// the current name as prefix.
func (c *gopCompleter) packageNameCompletions(ctx context.Context, fileURI span.URI, name *ast.Ident) error {
	cursor := int(c.pos - name.NamePos)
	if cursor < 0 || cursor > len(name.Name) {
		return errors.New("cursor is not in package name identifier")
	}

	c.completionContext.packageCompletion = true

	prefix := name.Name[:cursor]
	packageSuggestions, err := packageSuggestions(ctx, c.snapshot, fileURI, prefix)
	if err != nil {
		return err
	}

	for _, pkg := range packageSuggestions {
		c.deepState.enqueue(pkg)
	}
	return nil
}
