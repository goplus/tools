// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package checker_test

import (
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/goplus/xgo/ast"
	"golang.org/x/tools/gop/analysis"
	"golang.org/x/tools/gop/analysis/analysistest"
	"golang.org/x/tools/gop/analysis/internal/checker"
	"golang.org/x/tools/gop/analysis/passes/inspect"
	"golang.org/x/tools/gop/ast/inspector"
	"golang.org/x/tools/gop/packages"
	"golang.org/x/tools/internal/testenv"
)

func init() {
	packages.SetDebug(packages.DbgFlagAll)
	checker.SetDebug("v")
}

// TestStartFixes make sure modifying the first character
// of the file takes effect.
func TestStartFixes(t *testing.T) {
	testenv.NeedsGoPackages(t)

	files := map[string]string{
		"comment/doc.gop": `/* Package comment */
package comment
`}

	want := `// Package comment
package comment
`

	testdata, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	path := filepath.Join(testdata, "src/comment/doc.gop")
	checker.Fix = true
	checker.Run([]string{"file=" + path}, []analysis.IAnalyzer{commentAnalyzer})

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	got := string(contents)
	if got != want {
		t.Errorf("contents of rewritten file\ngot: %s\nwant: %s", got, want)
	}
}

var commentAnalyzer = &analysis.Analyzer{
	Name:     "comment",
	Requires: []analysis.IAnalyzer{inspect.Analyzer},
	Run:      commentRun,
}

func commentRun(pass *analysis.Pass) (interface{}, error) {
	const (
		from = "/* Package comment */"
		to   = "// Package comment"
	)
	log.Println("==> commentRun start")
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	log.Println("==> commentRun:", inspect)
	inspect.Preorder(nil, func(n ast.Node) {
		if n, ok := n.(*ast.Comment); ok && n.Text == from {
			pass.Report(analysis.Diagnostic{
				Pos: n.Pos(),
				End: n.End(),
				SuggestedFixes: []analysis.SuggestedFix{{
					TextEdits: []analysis.TextEdit{{
						Pos:     n.Pos(),
						End:     n.End(),
						NewText: []byte(to),
					}},
				}},
			})
		}
	})

	return nil, nil
}
