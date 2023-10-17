// Copyright 2022 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"

	"golang.org/x/tools/gopls/goxls/proxy"
)

const (
	gopls = "gopls.origin"
	goxls = "goxls"
)

func main() {
	proxy.Main(goxls, gopls, os.Args[1:]...)
}
