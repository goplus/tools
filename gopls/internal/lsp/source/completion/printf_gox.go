// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"go/constant"
	"go/types"
	"strings"

	"github.com/goplus/xgo/ast"
	"github.com/goplus/xgo/x/typesutil"
)

// gopPrintfArgKind returns the expected objKind when completing a
// printf-like operand. call is the printf-like function call, and
// argIdx is the index of call.Args being completed.
func gopPrintfArgKind(info *typesutil.Info, call *ast.CallExpr, argIdx int) objKind {
	// Printf-like function name must end in "f".
	fn := gopExprObj(info, call.Fun)
	if fn == nil || !strings.HasSuffix(fn.Name(), "f") {
		return kindAny
	}

	sig, _ := fn.Type().(*types.Signature)
	if sig == nil {
		return kindAny
	}

	// Must be variadic and take at least two params.
	numParams := sig.Params().Len()
	if !sig.Variadic() || numParams < 2 || argIdx < numParams-1 {
		return kindAny
	}

	// Param preceding variadic args must be a (format) string.
	if !types.Identical(sig.Params().At(numParams-2).Type(), types.Typ[types.String]) {
		return kindAny
	}

	// Format string must be a constant.
	strArg := info.Types[call.Args[numParams-2]].Value
	if strArg == nil || strArg.Kind() != constant.String {
		return kindAny
	}

	return formatOperandKind(constant.StringVal(strArg), argIdx-(numParams-1)+1)
}
