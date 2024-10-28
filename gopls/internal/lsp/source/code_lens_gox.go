// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"go/types"
	"log"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/parser"
	"golang.org/x/tools/gopls/internal/goxls"
	"golang.org/x/tools/gopls/internal/goxls/parserutil"
	"golang.org/x/tools/gopls/internal/lsp/command"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/span"
)

// GopLensFuncs returns the supported lensFuncs for Go+ files.
func GopLensFuncs() map[command.Command]LensFunc {
	return map[command.Command]LensFunc{
		command.Generate:      gopGenerateCodeLens,
		command.Test:          gopRunTestCodeLens,
		command.GCDetails:     gopToggleDetailsCodeLens,
		command.RunGopCommand: gopCommandCodeLens,
	}
}

func gopRunTestCodeLens(ctx context.Context, snapshot Snapshot, fh FileHandle) ([]protocol.CodeLens, error) {
	var codeLens []protocol.CodeLens

	pkg, pgf, err := NarrowestPackageForGopFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	fns, err := GopTestsAndBenchmarks(ctx, snapshot, pkg, pgf)
	if err != nil {
		return nil, err
	}
	if goxls.DbgCodeLens {
		log.Println("GopTestsAndBenchmarks: tests -", len(fns.Tests), "benchmarks -", len(fns.Benchmarks))
	}
	puri := protocol.URIFromSpanURI(fh.URI())
	for _, fn := range fns.Tests {
		cmd, err := command.NewTestCommand("run test", puri, []string{fn.Name}, nil)
		if err != nil {
			return nil, err
		}
		rng := protocol.Range{Start: fn.Rng.Start, End: fn.Rng.Start}
		codeLens = append(codeLens, protocol.CodeLens{Range: rng, Command: &cmd})
	}

	for _, fn := range fns.Benchmarks {
		cmd, err := command.NewTestCommand("run benchmark", puri, nil, []string{fn.Name})
		if err != nil {
			return nil, err
		}
		rng := protocol.Range{Start: fn.Rng.Start, End: fn.Rng.Start}
		codeLens = append(codeLens, protocol.CodeLens{Range: rng, Command: &cmd})
	}

	if len(fns.Benchmarks) > 0 {
		pgf, err := snapshot.ParseGop(ctx, fh, parserutil.ParseFull)
		if err != nil {
			return nil, err
		}
		// add a code lens to the top of the file which runs all benchmarks in the file
		rng, err := pgf.PosRange(pgf.File.Package, pgf.File.Package)
		if err != nil {
			return nil, err
		}
		var benches []string
		for _, fn := range fns.Benchmarks {
			benches = append(benches, fn.Name)
		}
		cmd, err := command.NewTestCommand("run file benchmarks", puri, nil, benches)
		if err != nil {
			return nil, err
		}
		codeLens = append(codeLens, protocol.CodeLens{Range: rng, Command: &cmd})
	}
	return codeLens, nil
}

func GopTestsAndBenchmarks(ctx context.Context, snapshot Snapshot, pkg Package, pgf *ParsedGopFile) (testFns, error) {
	var out testFns

	if !strings.HasSuffix(pgf.URI.Filename(), "_test.go") {
		return out, nil
	}

	for _, d := range pgf.File.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}

		rng, err := pgf.NodeRange(fn)
		if err != nil {
			return out, err
		}

		if gopMatchTestFunc(fn, pkg, testRe, "T") {
			out.Tests = append(out.Tests, testFn{fn.Name.Name, rng})
		}

		if gopMatchTestFunc(fn, pkg, benchmarkRe, "B") {
			out.Benchmarks = append(out.Benchmarks, testFn{fn.Name.Name, rng})
		}
	}

	return out, nil
}

func gopMatchTestFunc(fn *ast.FuncDecl, pkg Package, nameRe *regexp.Regexp, paramID string) bool {
	// Make sure that the function name matches a test function.
	if !nameRe.MatchString(fn.Name.Name) {
		return false
	}
	info := pkg.GopTypesInfo()
	if info == nil {
		return false
	}
	obj := info.ObjectOf(fn.Name)
	if obj == nil {
		return false
	}
	sig, ok := obj.Type().(*types.Signature)
	if !ok {
		return false
	}
	// Test functions should have only one parameter.
	if sig.Params().Len() != 1 {
		return false
	}

	// Check the type of the only parameter
	paramTyp, ok := sig.Params().At(0).Type().(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := paramTyp.Elem().(*types.Named)
	if !ok {
		return false
	}
	namedObj := named.Obj()
	if namedObj.Pkg().Path() != "testing" {
		return false
	}
	return namedObj.Id() == paramID
}

func gopGenerateCodeLens(ctx context.Context, snapshot Snapshot, fh FileHandle) ([]protocol.CodeLens, error) {
	return nil, nil // goxls: todo
}

func gopToggleDetailsCodeLens(ctx context.Context, snapshot Snapshot, fh FileHandle) ([]protocol.CodeLens, error) {
	pgf, err := snapshot.ParseGop(ctx, fh, parserutil.ParseFull)
	if err != nil {
		return nil, err
	}
	if !pgf.File.Package.IsValid() {
		// Without a package name we have nowhere to put the codelens, so give up.
		return nil, nil
	}
	rng, err := pgf.PosRange(pgf.File.Package, pgf.File.Package)
	if err != nil {
		return nil, err
	}
	puri := protocol.URIFromSpanURI(fh.URI())
	cmd, err := command.NewGCDetailsCommand("Toggle gc annotation details", puri)
	if err != nil {
		return nil, err
	}
	return []protocol.CodeLens{{Range: rng, Command: &cmd}}, nil
}

func gopCommandCodeLens(ctx context.Context, snapshot Snapshot, fh FileHandle) ([]protocol.CodeLens, error) {
	filename := fh.URI().Filename()
	if strings.HasSuffix(filename, "_test.go") || strings.HasSuffix(filename, "_test.gop") {
		return nil, nil
	}
	pgf, err := snapshot.ParseGop(ctx, fh, parser.PackageClauseOnly)
	if err != nil {
		return nil, err
	}
	if classType, isGoxTest := GoxTestClassType(pgf.File, filename); isGoxTest {
		return goxTestCodeLens(pgf, classType)
	}
	if pgf.File.Name.Name == "main" {
		rng, err := pgf.PosRange(pgf.File.Pos(), pgf.File.Pos())
		if err != nil {
			return nil, err
		}
		dir := protocol.URIFromSpanURI(span.URIFromPath(filepath.Dir(fh.URI().Filename())))
		args := command.RunGopCommandArgs{URI: dir, Command: "run"}
		cmd, err := command.NewRunGopCommandCommand("run main package", args)
		if err != nil {
			return nil, err
		}
		return []protocol.CodeLens{
			{Range: rng, Command: &cmd},
		}, nil
	}
	return nil, nil
}

func goxTestCodeLens(pgf *ParsedGopFile, classType string) ([]protocol.CodeLens, error) {
	rng, err := pgf.PosRange(pgf.File.Pos(), pgf.File.Pos())
	if err != nil {
		return nil, err
	}
	codelens := []protocol.CodeLens{
		{Range: rng, Command: &protocol.Command{
			Title:   "run test package",
			Command: "gop.test.package",
		}},
	}
	if pgf.File.IsProj && classType == "main" { //goxls:proj gox test
		return codelens, nil
	}
	args, err := command.MarshalArgs(
		map[string]string{
			"functionName": "Test_" + classType,
		},
	)
	if err != nil {
		return nil, err
	}
	codelens = append(codelens, protocol.CodeLens{
		Range: rng,
		Command: &protocol.Command{
			Title:     "run file tests",
			Command:   "gop.test.cursor",
			Arguments: args,
		},
	})
	return codelens, nil
}
