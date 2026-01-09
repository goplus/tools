// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"fmt"
	"go/types"

	"github.com/goplus/xgo/ast"
	"github.com/goplus/xgo/token"
	"github.com/goplus/xgo/x/typesutil"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/gopls/internal/bug"
	"golang.org/x/tools/gopls/internal/lsp/analysis/fillstruct"
	"golang.org/x/tools/gopls/internal/lsp/analysis/undeclaredname"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/imports"
)

type (
	gopSingleFileFixFunc func(fset *token.FileSet, start, end token.Pos, src []byte, file *ast.File, pkg *types.Package, info *typesutil.Info) (*analysis.SuggestedFix, error)
)

// gopSuggestedFixes maps a suggested fix command id to its handler.
var gopSuggestedFixes = map[string]SuggestedFixFunc{
	FillStruct:        gopSingleFile(fillstruct.GopSuggestedFix),
	UndeclaredName:    gopSingleFile(undeclaredname.GopSuggestedFix),
	ExtractVariable:   gopSingleFile(gopExtractVariable),
	ExtractFunction:   gopSingleFile(gopExtractFunction),
	ExtractMethod:     gopSingleFile(gopExtractMethod),
	InvertIfCondition: gopSingleFile(gopInvertIfCondition),
	StubMethods:       gopStubSuggestedFixFunc,
	AddEmbedImport:    gopAddEmbedImport,
}

// gopSingleFile calls analyzers that expect inputs for a single file
func gopSingleFile(sf gopSingleFileFixFunc) SuggestedFixFunc {
	return func(ctx context.Context, snapshot Snapshot, fh FileHandle, pRng protocol.Range) (*token.FileSet, *analysis.SuggestedFix, error) {
		pkg, pgf, err := NarrowestPackageForGopFile(ctx, snapshot, fh.URI())
		if err != nil {
			return nil, nil, err
		}
		start, end, err := pgf.RangePos(pRng)
		if err != nil {
			return nil, nil, err
		}
		fix, err := sf(pkg.FileSet(), start, end, pgf.Src, pgf.File, pkg.GetTypes(), pkg.GopTypesInfo())
		return pkg.FileSet(), fix, err
	}
}

// GopApplyFix applies the command's suggested fix to the given file and
// range, returning the resulting edits.
func GopApplyFix(ctx context.Context, fix string, snapshot Snapshot, fh FileHandle, pRng protocol.Range) ([]protocol.TextDocumentEdit, error) {
	handler, ok := gopSuggestedFixes[fix]
	if !ok {
		return nil, fmt.Errorf("no suggested fix function for %s", fix)
	}
	fset, suggestion, err := handler(ctx, snapshot, fh, pRng)
	if err != nil {
		return nil, err
	}
	if suggestion == nil {
		return nil, nil
	}
	editsPerFile := map[span.URI]*protocol.TextDocumentEdit{}
	for _, edit := range suggestion.TextEdits {
		tokFile := fset.File(edit.Pos)
		if tokFile == nil {
			return nil, bug.Errorf("no file for edit position")
		}
		end := edit.End
		if !end.IsValid() {
			end = edit.Pos
		}
		fh, err := snapshot.ReadFile(ctx, span.URIFromPath(tokFile.Name()))
		if err != nil {
			return nil, err
		}
		te, ok := editsPerFile[fh.URI()]
		if !ok {
			te = &protocol.TextDocumentEdit{
				TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
					Version: fh.Version(),
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{
						URI: protocol.URIFromSpanURI(fh.URI()),
					},
				},
			}
			editsPerFile[fh.URI()] = te
		}
		content, err := fh.Content()
		if err != nil {
			return nil, err
		}
		m := protocol.NewMapper(fh.URI(), content)
		rng, err := m.PosRange(tokFile, edit.Pos, end)
		if err != nil {
			return nil, err
		}
		te.Edits = append(te.Edits, protocol.TextEdit{
			Range:   rng,
			NewText: string(edit.NewText),
		})
	}
	var edits []protocol.TextDocumentEdit
	for _, edit := range editsPerFile {
		edits = append(edits, *edit)
	}
	return edits, nil
}

// gopAddEmbedImport adds a missing embed "embed" import with blank name.
func gopAddEmbedImport(ctx context.Context, snapshot Snapshot, fh FileHandle, rng protocol.Range) (*token.FileSet, *analysis.SuggestedFix, error) {
	pkg, pgf, err := NarrowestPackageForGopFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, nil, fmt.Errorf("narrow pkg: %w", err)
	}

	// Like source.AddImport, but with _ as Name and using our pgf.
	protoEdits, err := GopComputeOneImportFixEdits(snapshot, pgf, &imports.ImportFix{
		StmtInfo: imports.ImportInfo{
			ImportPath: "embed",
			Name:       "_",
		},
		FixType: imports.AddImport,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("compute edits: %w", err)
	}

	var edits []analysis.TextEdit
	for _, e := range protoEdits {
		start, end, err := pgf.RangePos(e.Range)
		if err != nil {
			return nil, nil, fmt.Errorf("map range: %w", err)
		}
		edits = append(edits, analysis.TextEdit{
			Pos:     start,
			End:     end,
			NewText: []byte(e.NewText),
		})
	}

	fix := &analysis.SuggestedFix{
		Message:   "Add embed import",
		TextEdits: edits,
	}
	return pkg.FileSet(), fix, nil
}
