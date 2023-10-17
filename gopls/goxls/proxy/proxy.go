// Copyright 2022 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package proxy

import (
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func Main(gopls, goxls string, args ...string) {
	if len(args) > 0 && args[0] == "version" {
		version(gopls, args)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
	}()

	home, err := os.UserHomeDir()
	check(err)

	goplsDir := home + "/.gopls/"
	rotateDir := goplsDir + strconv.FormatInt(time.Now().UnixMicro(), 36)
	if _, e := os.Lstat(goplsDir + "gopls.in"); e == nil {
		err = os.MkdirAll(rotateDir, 0755)
		check(err)
	}

	rotateDir += "/"
	createFile := func(name string) (f *os.File, err error) {
		normal, rotate := goplsDir+name, rotateDir+name
		os.Rename(normal, rotate)
		return os.Create(normal)
	}

	stdinf, err := createFile("gopls.in")
	check(err)
	defer stdinf.Close()

	stdoutf, err := createFile(gopls + ".out")
	check(err)
	defer stdoutf.Close()

	var pwGox io.WriteCloser
	if goxls != "" {
		goxoutf, err := createFile(goxls + ".out")
		check(err)
		defer goxoutf.Close()

		pr, pw, err := os.Pipe()
		check(err)
		pwGox = pw

		go func() {
			cmd := exec.CommandContext(ctx, goxls, args...)
			cmd.Stdin = pr
			cmd.Stdout = goxoutf
			cmd.Stderr = os.Stderr
			err = cmd.Run()
			check(err)
		}()
	}

	pr, pw, err := os.Pipe()
	check(err)
	go func() {
		writers := make([]io.Writer, 2, 3)
		writers[0], writers[1] = pw, stdinf
		if pwGox != nil {
			writers = append(writers, pwGox)
		}
		w := io.MultiWriter(writers...)
		for {
			io.Copy(w, os.Stdin)
		}
	}()

	cmd := exec.CommandContext(ctx, gopls, args...)
	cmd.Stdin = pr
	cmd.Stdout = io.MultiWriter(os.Stdout, stdoutf)
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	check(err)
}

func version(gopls string, args []string) {
	cmd := exec.Command(gopls, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	check(err)
}

func check(err error) {
	if err != nil {
		log.Panicln(err)
	}
}
