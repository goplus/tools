// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package langserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/goplus/gop/x/langserver"
)

const (
	Enable = true
)

var (
	ls langserver.Client
)

func Initialize() {
	if Enable {
		go Get()
	}
}

func Shutdown() {
	if Enable {
		Get().Close()
	}
}

var (
	onceInit sync.Once
)

func lookupCmd(cmd string) string {
	if bin, err := exec.LookPath(cmd); err == nil {
		return bin
	}
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		if bin, err := exec.LookPath(filepath.Join(gobin, cmd)); err == nil {
			return bin
		}
	}
	if data, err := exec.Command("go", "env", "GOPATH").Output(); err == nil {
		gopath := strings.TrimSpace(string(data))
		for _, path := range filepath.SplitList(gopath) {
			if bin, err := exec.LookPath(filepath.Join(path, "bin", cmd)); err == nil {
				return bin
			}
		}
	}
	return cmd
}

func Get() langserver.Client {
	onceInit.Do(func() {
		cmd := lookupCmd("gop")
		ls = langserver.ServeAndDial(nil, cmd, "serve", "-v")
	})
	return ls
}

func GenGo(ctx context.Context, pattern ...string) error {
	if Enable {
		return Get().GenGo(ctx, pattern...)
	}
	return langserver.GenGo(pattern...)
}

func Changed(ctx context.Context, files ...string) error {
	if Enable {
		return Get().Changed(ctx, files...)
	}
	return nil
}
