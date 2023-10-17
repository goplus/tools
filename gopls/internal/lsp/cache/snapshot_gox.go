// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"golang.org/x/tools/gopls/internal/goxls/goputil"
	"golang.org/x/tools/gopls/internal/lsp/source"
)

func checkGopFile(fh source.FileHandle, fext string) bool {
	return goputil.FileKind(fext) != 0
}

func gopExtensions() string {
	return goputil.Exts()
}
