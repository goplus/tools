package lsp

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/lsp/cache"
	"golang.org/x/tools/gopls/internal/lsp/debug"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/lsp/tests"
	"golang.org/x/tools/gopls/internal/lsp/tests/compare"
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
	// test signature_overload
	testSignatureHelpOverload(t, r, datum)
}

func testSignatureHelpOverload(t *testing.T, r *runner, datum *tests.Data) {
	// Collect names for the entries that require golden files.
	signatures := make(map[span.Span]*protocol.SignatureHelp)
	collectSignatures := func(spn span.Span, signature string, activeSignature int64, activeParam int64) {
		signatures[spn] = &protocol.SignatureHelp{
			Signatures: []protocol.SignatureInformation{
				{
					Label: signature,
				},
			},
			ActiveSignature: uint32(activeSignature),
			ActiveParameter: uint32(activeParam),
		}
		// Hardcode special case to test the lack of a signature.
		if signature == "" && activeParam == 0 {
			signatures[spn] = nil
		}
	}

	if err := datum.Exported.Expect(map[string]interface{}{
		"signature_overload": collectSignatures,
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("SignatureHelpOverload", func(t *testing.T) {
		t.Helper()
		for spn, expectedSignature := range signatures {
			t.Run(tests.SpanName(spn), func(t *testing.T) {
				t.Helper()
				r.SignatureHelpOverload(t, spn, expectedSignature)
			})
		}
	})
}

func (r *runner) SignatureHelpOverload(t *testing.T, spn span.Span, want *protocol.SignatureHelp) {
	m, err := r.data.Mapper(spn.URI())
	if err != nil {
		t.Fatal(err)
	}
	loc, err := m.SpanLocation(spn)
	if err != nil {
		t.Fatalf("failed for %v: %v", loc, err)
	}
	params := &protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.LocationTextDocumentPositionParams(loc),
	}
	got, err := r.server.SignatureHelp(r.ctx, params)
	if err != nil {
		// Only fail if we got an error we did not expect.
		if want != nil {
			t.Fatal(err)
		}
		return
	}
	if want == nil {
		if got != nil {
			t.Errorf("expected no signature, got %v", got)
		}
		return
	}
	if got == nil {
		t.Fatalf("expected %v, got nil", want)
	}
	if diff := DiffSignaturesOverload(spn, want, got); diff != "" {
		t.Error(diff)
	}
}

func DiffSignaturesOverload(spn span.Span, want, got *protocol.SignatureHelp) string {
	decorate := func(f string, args ...interface{}) string {
		return fmt.Sprintf("invalid signature at %s: %s", spn, fmt.Sprintf(f, args...))
	}
	if want.ActiveSignature != got.ActiveSignature {
		return decorate("wanted active signature of %d, got %d", want.ActiveSignature, int(got.ActiveSignature))
	}
	if want.ActiveParameter != got.ActiveParameter {
		return decorate("wanted active parameter of %d, got %d", want.ActiveParameter, int(got.ActiveParameter))
	}
	g := got.Signatures[got.ActiveSignature]
	w := want.Signatures[0]
	if diff := compare.Text(tests.NormalizeAny(w.Label), tests.NormalizeAny(g.Label)); diff != "" {
		return decorate("mismatched labels:\n%s", diff)
	}
	var paramParts []string
	for _, p := range g.Parameters {
		paramParts = append(paramParts, p.Label)
	}
	paramsStr := strings.Join(paramParts, ", ")
	if !strings.Contains(g.Label, paramsStr) {
		return decorate("expected signature %q to contain params %q", g.Label, paramsStr)
	}
	return ""
}
