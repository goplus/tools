// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"go/types"
	"strings"

	"github.com/goplus/xgo/ast"
	"golang.org/x/tools/gopls/internal/lsp/source"
)

// some function definitions in test files can be completed
// So far, TestFoo(t *testing.T), TestMain(m *testing.M)
// BenchmarkFoo(b *testing.B), FuzzFoo(f *testing.F)

// path[0] is known to be *ast.Ident
func gopDefinition(path []ast.Node, obj types.Object, pgf *source.ParsedGopFile) ([]CompletionItem, *Selection) {
	if _, ok := obj.(*types.Func); !ok {
		return nil, nil // not a function at all
	}
	if !strings.HasSuffix(pgf.URI.Filename(), "_test.go") {
		return nil, nil // not a test file
	}

	name := path[0].(*ast.Ident).Name
	if len(name) == 0 {
		// can't happen
		return nil, nil
	}
	start := path[0].Pos()
	end := path[0].End()
	sel := &Selection{
		content: "",
		cursor:  start,
		tokFile: pgf.Tok,
		start:   start,
		end:     end,
		mapper:  pgf.Mapper,
	}
	var ans []CompletionItem
	var hasParens bool
	n, ok := path[1].(*ast.FuncDecl)
	if !ok {
		return nil, nil // can't happen
	}
	if n.Recv != nil {
		return nil, nil // a method, not a function
	}
	t := n.Type.Params
	if t.Closing != t.Opening {
		hasParens = true
	}

	// Always suggest TestMain, if possible
	if strings.HasPrefix("TestMain", name) {
		if hasParens {
			ans = append(ans, defItem("TestMain", obj))
		} else {
			ans = append(ans, defItem("TestMain(m *testing.M)", obj))
		}
	}

	// If a snippet is possible, suggest it
	if strings.HasPrefix("Test", name) {
		if hasParens {
			ans = append(ans, defItem("Test", obj))
		} else {
			ans = append(ans, defSnippet("Test", "(t *testing.T)", obj))
		}
		return ans, sel
	} else if strings.HasPrefix("Benchmark", name) {
		if hasParens {
			ans = append(ans, defItem("Benchmark", obj))
		} else {
			ans = append(ans, defSnippet("Benchmark", "(b *testing.B)", obj))
		}
		return ans, sel
	} else if strings.HasPrefix("Fuzz", name) {
		if hasParens {
			ans = append(ans, defItem("Fuzz", obj))
		} else {
			ans = append(ans, defSnippet("Fuzz", "(f *testing.F)", obj))
		}
		return ans, sel
	}

	// Fill in the argument for what the user has already typed
	if got := defMatches(name, "Test", path, "(t *testing.T)"); got != "" {
		ans = append(ans, defItem(got, obj))
	} else if got := defMatches(name, "Benchmark", path, "(b *testing.B)"); got != "" {
		ans = append(ans, defItem(got, obj))
	} else if got := defMatches(name, "Fuzz", path, "(f *testing.F)"); got != "" {
		ans = append(ans, defItem(got, obj))
	}
	return ans, sel
}
