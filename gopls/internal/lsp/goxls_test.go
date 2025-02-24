package lsp

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/gopls/internal/lsp/cache"
	"golang.org/x/tools/gopls/internal/lsp/debug"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/lsp/tests"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/testenv"
)

// goxls: tests
func TestXLS(t *testing.T) {
	tests.RunTests(t, "../goxls/testdata", true, testXLS)
}

func testXLS(t *testing.T, datum *tests.Data) {
	ctx := tests.Context(t)

	// Setting a debug instance suppresses logging to stderr, but ensures that we
	// still e.g. convert events into runtime/trace/instrumentation.
	//
	// Previously, we called event.SetExporter(nil), which turns off all
	// instrumentation.
	ctx = debug.WithInstance(ctx, "", "off")

	session := cache.NewSession(ctx, cache.New(nil), nil)
	options := source.DefaultOptions().Clone()
	tests.DefaultOptions(options)
	session.SetOptions(options)
	options.SetEnvSlice(datum.Config.Env)
	view, snapshot, release, err := session.NewView(ctx, datum.Config.Dir, span.URIFromPath(datum.Config.Dir), options)
	if err != nil {
		t.Fatal(err)
	}

	defer session.RemoveView(view)

	// Enable type error analyses for tests.
	// TODO(golang/go#38212): Delete this once they are enabled by default.
	tests.EnableAllAnalyzers(options)
	session.SetViewOptions(ctx, view, options)

	// Enable all inlay hints for tests.
	tests.EnableAllInlayHints(options)

	// Only run the -modfile specific tests in module mode with Go 1.14 or above.
	datum.ModfileFlagAvailable = len(snapshot.ModFiles()) > 0 && testenv.Go1Point() >= 14
	release()

	// Open all files for performance reasons, because gopls only
	// keeps active packages (those with open files) in memory.
	//
	// In practice clients will only send document-oriented requests for open
	// files.
	var modifications []source.FileModification
	for _, module := range datum.Exported.Modules {
		for name := range module.Files {
			filename := datum.Exported.File(module.Name, name)
			if filepath.Ext(filename) != ".gop" {
				continue
			}
			content, err := datum.Exported.FileContents(filename)
			if err != nil {
				t.Fatal(err)
			}
			modifications = append(modifications, source.FileModification{
				URI:        span.URIFromPath(filename),
				Action:     source.Open,
				Version:    -1,
				Text:       content,
				LanguageID: "gop",
			})
		}
	}
	for filename, content := range datum.Config.Overlay {
		if filepath.Ext(filename) != ".gop" {
			continue
		}
		modifications = append(modifications, source.FileModification{
			URI:        span.URIFromPath(filename),
			Action:     source.Open,
			Version:    -1,
			Text:       content,
			LanguageID: "gop",
		})
	}
	if err := session.ModifyFiles(ctx, modifications); err != nil {
		t.Fatal(err)
	}
	r := &runner{
		data:     datum,
		ctx:      ctx,
		editRecv: make(chan map[span.URI][]byte, 1),
	}
	r.server = NewServer(session, testClient{runner: r})
	tests.Run(t, r, datum)
}
