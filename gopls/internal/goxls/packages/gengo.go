// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package packages

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/goplus/gop"
	"github.com/goplus/gop/x/gopprojs"
)

var (
	gopInstalled bool
)

const (
	debugGop = false
)

func init() {
	go initGop()
}

func initGop() {
	var b bytes.Buffer
	cmd := exec.Command("gop", "env", "GOPROOT")
	cmd.Stdout = &b
	err := cmd.Run()
	if gopRoot := b.String(); err == nil && gopRoot != "" {
		os.Setenv("GOPROOT", strings.TrimRight(gopRoot, "\n\r"))
		gopInstalled = true
	} else if debugGop {
		log.Panicln("FATAL: gop not installed")
	}
}

func GenGo(pattern ...string) (err error) {
	if !gopInstalled {
		return nil
	}
	projs, err := gopprojs.ParseAll(pattern...)
	if err != nil {
		return
	}
	for _, proj := range projs {
		switch v := proj.(type) {
		case *gopprojs.DirProj:
			_, _, err = gop.GenGo(v.Dir, nil, true)
		case *gopprojs.PkgPathProj:
			if v.Path == "builtin" {
				continue
			}
			_, _, err = gop.GenGoPkgPath("", v.Path, nil, true)
		}
	}
	return
}
