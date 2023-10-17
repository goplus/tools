// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package packagesinternal exposes internal-only fields from goxls/packages.
package packagesinternal

import (
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/packagesinternal"
)

var GetForTest = func(p interface{}) string { return "" }
var GetDepsErrors = func(p interface{}) []*PackageError { return nil }

type PackageError = packagesinternal.PackageError

var GetGoCmdRunner = func(config interface{}) *gocommand.Runner { return nil }

var SetGoCmdRunner = func(config interface{}, runner *gocommand.Runner) {}

var TypecheckCgo int
var DepsErrors int // must be set as a LoadMode to call GetDepsErrors
var ForTest int    // must be set as a LoadMode to call GetForTest

var SetModFlag = func(config interface{}, value string) {}
var SetModFile = func(config interface{}, value string) {}
