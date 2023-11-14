// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"
	"fmt"
	"log"

	"github.com/goplus/gop/ast"
	"golang.org/x/tools/gop/ast/inspector"
	"golang.org/x/tools/gopls/internal/bug"
	"golang.org/x/tools/gopls/internal/goxls"
	"golang.org/x/tools/gopls/internal/goxls/parserutil"
	"golang.org/x/tools/gopls/internal/lsp/analysis/fillstruct"
	"golang.org/x/tools/gopls/internal/lsp/analysis/infertypeargs"
	"golang.org/x/tools/gopls/internal/lsp/analysis/stubmethods"
	"golang.org/x/tools/gopls/internal/lsp/command"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/tag"
)

func (s *Server) gopCodeAction(
	ctx context.Context, params *protocol.CodeActionParams,
	uri span.URI, snapshot source.Snapshot, fh source.FileHandle,
	want map[protocol.CodeActionKind]bool) ([]protocol.CodeAction, error) {
	diagnostics := params.Context.Diagnostics

	if goxls.DbgCodeAction {
		log.Println(
			"gopCodeAction:", uri.Filename(), "diagnostics:", len(diagnostics),
			"refactorRewrite:", want[protocol.RefactorRewrite], "orgImports:", want[protocol.SourceOrganizeImports], "goTest:", want[protocol.GoTest])
		defer log.Println("gopCodeAction done:", uri.Filename())
	}

	// Don't suggest fixes for generated files, since they are generally
	// not useful and some editors may apply them automatically on save.
	if source.IsGopGenerated(ctx, snapshot, uri) {
		return nil, nil
	}

	if goxls.DbgCodeAction && len(diagnostics) > 0 {
		log.Println("gopCodeActionsMatchingDiagnostics start:", diagnostics)
	}
	actions, err := s.codeActionsMatchingDiagnostics(ctx, uri, snapshot, diagnostics, want)
	if err != nil {
		return nil, err
	}
	if goxls.DbgCodeAction && len(actions) > 0 {
		log.Println("gopCodeActionsMatchingDiagnostics end:", actions)
	}

	// Only compute quick fixes if there are any diagnostics to fix.
	wantQuickFixes := want[protocol.QuickFix] && len(diagnostics) > 0

	// Code actions requiring syntax information alone.
	if wantQuickFixes || want[protocol.SourceOrganizeImports] || want[protocol.RefactorExtract] {
		pgf, err := snapshot.ParseGop(ctx, fh, parserutil.ParseFull)
		if err != nil {
			return nil, err
		}

		// Process any missing imports and pair them with the diagnostics they
		// fix.
		if wantQuickFixes || want[protocol.SourceOrganizeImports] {
			importEdits, importEditsPerFix, err := source.GopAllImportsFixes(ctx, snapshot, pgf)
			if err != nil {
				event.Error(ctx, "imports fixes", err, tag.File.Of(fh.URI().Filename()))
				importEdits = nil
				importEditsPerFix = nil
			}

			// Separate this into a set of codeActions per diagnostic, where
			// each action is the addition, removal, or renaming of one import.
			if wantQuickFixes {
				for _, importFix := range importEditsPerFix {
					fixed := fixedByImportFix(importFix.Fix, diagnostics)
					if len(fixed) == 0 {
						continue
					}
					actions = append(actions, protocol.CodeAction{
						Title: importFixTitle(importFix.Fix),
						Kind:  protocol.QuickFix,
						Edit: &protocol.WorkspaceEdit{
							DocumentChanges: documentChanges(fh, importFix.Edits),
						},
						Diagnostics: fixed,
					})
				}
			}

			// Send all of the import edits as one code action if the file is
			// being organized.
			if want[protocol.SourceOrganizeImports] && len(importEdits) > 0 {
				actions = append(actions, protocol.CodeAction{
					Title: "Organize Imports",
					Kind:  protocol.SourceOrganizeImports,
					Edit: &protocol.WorkspaceEdit{
						DocumentChanges: documentChanges(fh, importEdits),
					},
				})
			}
		}

		if want[protocol.RefactorExtract] {
			extractions, err := gopRefactorExtract(ctx, snapshot, pgf, params.Range)
			if err != nil {
				return nil, err
			}
			actions = append(actions, extractions...)
		}
	}

	var stubMethodsDiagnostics []protocol.Diagnostic
	if wantQuickFixes && snapshot.View().Options().IsAnalyzerEnabled(stubmethods.Analyzer.Name) {
		for _, pd := range diagnostics {
			if stubmethods.MatchesMessage(pd.Message) {
				stubMethodsDiagnostics = append(stubMethodsDiagnostics, pd)
			}
		}
	}

	// Code actions requiring type information.
	if len(stubMethodsDiagnostics) > 0 || want[protocol.RefactorRewrite] || want[protocol.GoTest] {
		pkg, pgf, err := source.NarrowestPackageForGopFile(ctx, snapshot, fh.URI())
		if err != nil {
			return nil, err
		}
		for _, pd := range stubMethodsDiagnostics {
			start, end, err := pgf.RangePos(pd.Range)
			if err != nil {
				return nil, err
			}
			action, ok, err := func() (_ protocol.CodeAction, _ bool, rerr error) {
				// golang/go#61693: code actions were refactored to run outside of the
				// analysis framework, but as a result they lost their panic recovery.
				//
				// Stubmethods "should never fail"", but put back the panic recovery as a
				// defensive measure.
				defer func() {
					if r := recover(); r != nil {
						rerr = bug.Errorf("stubmethods panicked: %v", r)
					}
				}()
				d, ok := stubmethods.GopDiagnosticForError(pkg.FileSet(), pgf.File, start, end, pd.Message, pkg.GopTypesInfo())
				if !ok {
					return protocol.CodeAction{}, false, nil
				}
				cmd, err := command.NewApplyFixCommand(d.Message, command.ApplyFixArgs{
					URI:   protocol.URIFromSpanURI(pgf.URI),
					Fix:   source.StubMethods,
					Range: pd.Range,
				})
				if err != nil {
					return protocol.CodeAction{}, false, err
				}
				return protocol.CodeAction{
					Title:       d.Message,
					Kind:        protocol.QuickFix,
					Command:     &cmd,
					Diagnostics: []protocol.Diagnostic{pd},
				}, true, nil
			}()
			if err != nil {
				return nil, err
			}
			if ok {
				actions = append(actions, action)
			}
		}

		if want[protocol.RefactorRewrite] {
			rewrites, err := gopRefactorRewrite(ctx, snapshot, pkg, pgf, fh, params.Range)
			if err != nil {
				return nil, err
			}
			actions = append(actions, rewrites...)
		}

		if want[protocol.GoTest] {
			fixes, err := gopTest(ctx, snapshot, pkg, pgf, params.Range)
			if err != nil {
				return nil, err
			}
			actions = append(actions, fixes...)
		}
	}

	return actions, nil
}

func gopRefactorExtract(ctx context.Context, snapshot source.Snapshot, pgf *source.ParsedGopFile, rng protocol.Range) ([]protocol.CodeAction, error) {
	if rng.Start == rng.End {
		return nil, nil
	}

	start, end, err := pgf.RangePos(rng)
	if err != nil {
		return nil, err
	}
	puri := protocol.URIFromSpanURI(pgf.URI)
	var commands []protocol.Command
	if _, ok, methodOk, _ := source.GopCanExtractFunction(pgf.Tok, start, end, pgf.Src, pgf.File); ok {
		cmd, err := command.NewApplyFixCommand("Extract function", command.ApplyFixArgs{
			URI:   puri,
			Fix:   source.ExtractFunction,
			Range: rng,
		})
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
		if methodOk {
			cmd, err := command.NewApplyFixCommand("Extract method", command.ApplyFixArgs{
				URI:   puri,
				Fix:   source.ExtractMethod,
				Range: rng,
			})
			if err != nil {
				return nil, err
			}
			commands = append(commands, cmd)
		}
	}
	if _, _, ok, _ := source.GopCanExtractVariable(start, end, pgf.File); ok {
		cmd, err := command.NewApplyFixCommand("Extract variable", command.ApplyFixArgs{
			URI:   puri,
			Fix:   source.ExtractVariable,
			Range: rng,
		})
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}
	var actions []protocol.CodeAction
	for i := range commands {
		actions = append(actions, protocol.CodeAction{
			Title:   commands[i].Title,
			Kind:    protocol.RefactorExtract,
			Command: &commands[i],
		})
	}
	return actions, nil
}

func gopRefactorRewrite(ctx context.Context, snapshot source.Snapshot, pkg source.Package, pgf *source.ParsedGopFile, fh source.FileHandle, rng protocol.Range) (_ []protocol.CodeAction, rerr error) {
	// golang/go#61693: code actions were refactored to run outside of the
	// analysis framework, but as a result they lost their panic recovery.
	//
	// These code actions should never fail, but put back the panic recovery as a
	// defensive measure.
	defer func() {
		if r := recover(); r != nil {
			rerr = bug.Errorf("refactor.rewrite code actions panicked: %v", r)
		}
	}()
	start, end, err := pgf.RangePos(rng)
	if err != nil {
		return nil, err
	}

	var commands []protocol.Command
	if _, ok, _ := source.GopCanInvertIfCondition(pgf.File, start, end); ok {
		cmd, err := command.NewApplyFixCommand("Invert if condition", command.ApplyFixArgs{
			URI:   protocol.URIFromSpanURI(pgf.URI),
			Fix:   source.InvertIfCondition,
			Range: rng,
		})
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}

	// N.B.: an inspector only pays for itself after ~5 passes, which means we're
	// currently not getting a good deal on this inspection.
	//
	// TODO: Consider removing the inspection after convenienceAnalyzers are removed.
	inspect := inspector.New([]*ast.File{pgf.File})
	if snapshot.View().Options().IsAnalyzerEnabled(fillstruct.Analyzer.Name) {
		for _, d := range fillstruct.GopDiagnoseFillableStructs(inspect, start, end, pkg.GetTypes(), pkg.GopTypesInfo()) {
			rng, err := pgf.Mapper.PosRange(pgf.Tok, d.Pos, d.End)
			log.Println("!!!!!!!!!!!!!!!!!!!!!!!!!Gox NewApplyFixCommand,rng:", rng, "pgf.URI:", pgf.URI)

			if err != nil {
				return nil, err
			}
			cmd, err := command.NewApplyFixCommand(d.Message, command.ApplyFixArgs{
				URI:   protocol.URIFromSpanURI(pgf.URI),
				Fix:   source.FillStruct,
				Range: rng,
			})
			if err != nil {
				return nil, err
			}
			commands = append(commands, cmd)
		}
	}

	var actions []protocol.CodeAction
	for i := range commands {
		actions = append(actions, protocol.CodeAction{
			Title:   commands[i].Title,
			Kind:    protocol.RefactorRewrite,
			Command: &commands[i],
		})
	}

	if snapshot.View().Options().IsAnalyzerEnabled(infertypeargs.Analyzer.Name) {
		for _, d := range infertypeargs.GopDiagnoseInferableTypeArgs(pkg.FileSet(), inspect, start, end, pkg.GetTypes(), pkg.GetTypesInfo()) {
			if len(d.SuggestedFixes) != 1 {
				panic(fmt.Sprintf("unexpected number of suggested fixes from infertypeargs: %v", len(d.SuggestedFixes)))
			}
			fix := d.SuggestedFixes[0]
			var edits []protocol.TextEdit
			for _, analysisEdit := range fix.TextEdits {
				rng, err := pgf.Mapper.PosRange(pgf.Tok, analysisEdit.Pos, analysisEdit.End)
				if err != nil {
					return nil, err
				}
				edits = append(edits, protocol.TextEdit{
					Range:   rng,
					NewText: string(analysisEdit.NewText),
				})
			}
			actions = append(actions, protocol.CodeAction{
				Title: "Simplify type arguments",
				Kind:  protocol.RefactorRewrite,
				Edit: &protocol.WorkspaceEdit{
					DocumentChanges: documentChanges(fh, edits),
				},
			})
		}
	}

	return actions, nil
}

func gopTest(ctx context.Context, snapshot source.Snapshot, pkg source.Package, pgf *source.ParsedGopFile, rng protocol.Range) ([]protocol.CodeAction, error) {
	fns, err := source.GopTestsAndBenchmarks(ctx, snapshot, pkg, pgf)
	if err != nil {
		return nil, err
	}

	var tests, benchmarks []string
	for _, fn := range fns.Tests {
		if !protocol.Intersect(fn.Rng, rng) {
			continue
		}
		tests = append(tests, fn.Name)
	}
	for _, fn := range fns.Benchmarks {
		if !protocol.Intersect(fn.Rng, rng) {
			continue
		}
		benchmarks = append(benchmarks, fn.Name)
	}

	if len(tests) == 0 && len(benchmarks) == 0 {
		return nil, nil
	}

	cmd, err := command.NewTestCommand("Run tests and benchmarks", protocol.URIFromSpanURI(pgf.URI), tests, benchmarks)
	if err != nil {
		return nil, err
	}
	return []protocol.CodeAction{{
		Title:   cmd.Title,
		Kind:    protocol.GoTest,
		Command: &cmd,
	}}, nil
}
