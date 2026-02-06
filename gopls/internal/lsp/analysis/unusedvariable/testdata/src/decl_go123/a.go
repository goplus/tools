// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.23

package decl

func a() {
	var b, c bool // want `declared and not used: b`
	panic(c)

	if 1 == 1 {
		var s string // want `declared and not used: s`
	}
}

func b() {
	// b is a variable
	var b bool // want `declared and not used: b`
}

func c() {
	var (
		d string

		// some comment for c
		c bool // want `declared and not used: c`
	)

	panic(d)
}
