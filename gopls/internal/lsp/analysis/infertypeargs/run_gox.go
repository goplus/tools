// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package infertypeargs

import (
	"go/types"

	"github.com/goplus/xgo/token"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/gop/ast/inspector"
)

// GopDiagnoseInferableTypeArgs reports diagnostics describing simplifications to type
// arguments overlapping with the provided start and end position.
//
// If start or end is token.NoPos, the corresponding bound is not checked
// (i.e. if both start and end are NoPos, all call expressions are considered).
func GopDiagnoseInferableTypeArgs(fset *token.FileSet, inspect *inspector.Inspector, start, end token.Pos, pkg *types.Package, info *types.Info) []analysis.Diagnostic {
	return nil // goxls: todo
}
