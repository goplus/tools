// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goxls

import (
	"github.com/goplus/xgo/cl"
	"github.com/goplus/xgo/x/typesutil"
)

type DbgFlags int

const (
	DbgFlagTypesUtil DbgFlags = 1 << iota
	DbgFlagDisableRecover
	DbgFlagCompletion
	DbgFlagCodeAction
	DbgFlagHover
	DbgFlagHighlight
	DbgFlagDefinition
	DbgFlagCommand
	DbgFlagCodeLens
	DbgFlagImplementation
	DbgFlagRename
	DbgFlagAnalysis

	DbgFlagAnaFillreturns
	DbgFlagAnaAll = DbgFlagAnaFillreturns

	DbgFlagDefault = DbgFlagTypesUtil | DbgFlagCompletion | DbgFlagCodeAction | DbgFlagHover | DbgFlagHighlight |
		DbgFlagDefinition | DbgFlagCommand | DbgFlagCodeLens | DbgFlagImplementation | DbgFlagRename | DbgFlagAnalysis

	DbgFlagAll = DbgFlagDefault | DbgFlagAnaAll | DbgFlagDisableRecover
)

const (
	DbgMisuse = true
)

var (
	DbgCompletion     bool
	DbgCodeAction     bool
	DbgHover          bool
	DbgHighlight      bool
	DbgDefinition     bool
	DbgCommand        bool
	DbgCodeLens       bool
	DbgImplementation bool
	DbgRename         bool
	DbgAnalysis       bool

	DbgAnaFillreturns bool
)

func SetDebug(flags DbgFlags) {
	if (flags & DbgFlagTypesUtil) != 0 {
		typesutil.SetDebug(typesutil.DbgFlagDefault)
	}
	if (flags & DbgFlagDisableRecover) != 0 {
		cl.SetDisableRecover(true)
	}

	DbgCompletion = (flags & DbgFlagCompletion) != 0
	DbgHover = (flags & DbgFlagHover) != 0
	DbgCodeAction = (flags & DbgFlagCodeAction) != 0
	DbgHighlight = (flags & DbgFlagHighlight) != 0
	DbgDefinition = (flags & DbgFlagDefinition) != 0
	DbgCommand = (flags & DbgFlagCommand) != 0
	DbgCodeLens = (flags & DbgFlagCodeLens) != 0
	DbgImplementation = (flags & DbgFlagImplementation) != 0
	DbgRename = (flags & DbgFlagRename) != 0
	DbgAnalysis = (flags & DbgFlagAnalysis) != 0

	DbgAnaFillreturns = (flags & DbgFlagAnaFillreturns) != 0
}
