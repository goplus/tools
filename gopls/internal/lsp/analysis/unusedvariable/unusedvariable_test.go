// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unusedvariable_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/gopls/internal/lsp/analysis/unusedvariable"
	"golang.org/x/tools/internal/testenv"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()

	declDir := "decl"
	assignDir := "assign"
	if testenv.Go1Point() >= 23 {
		declDir = "decl_go123"
		assignDir = "assign_go123"
	}

	t.Run("decl", func(t *testing.T) {
		analysistest.RunWithSuggestedFixes(t, testdata, unusedvariable.Analyzer, declDir)
	})

	t.Run("assign", func(t *testing.T) {
		analysistest.RunWithSuggestedFixes(t, testdata, unusedvariable.Analyzer, assignDir)
	})
}
