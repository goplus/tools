// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.11 && !go1.23
//go:build go1.23

package a

import (
	"fmt"
	"os"
)

type A struct {
	b int
}

func singleAssignment() {
	v := "s" // want `declared and not used: v`

	s := []int{ // want `declared and not used: s`
		1,
		2,
	}

	a := func(s string) bool { // want `declared and not used: a`
		return false
	}

	if 1 == 1 {
		s := "v" // want `declared and not used: s`
	}

	panic("I should survive")
}

func noOtherStmtsInBlock() {
	v := "s" // want `declared and not used: v`
}

func partOfMultiAssignment() {
	f, err := os.Open("file") // want `declared and not used: f`
	panic(err)
}

func sideEffects(cBool chan bool, cInt chan int) {
	b := <-c            // want `declared and not used: b`
	s := fmt.Sprint("") // want `declared and not used: s`
	a := A{             // want `declared and not used: a`
		b: func() int {
			return 1
		}(),
	}
	c := A{<-cInt}          // want `declared and not used: c`
	d := fInt() + <-cInt    // want `declared and not used: d`
	e := fBool() && <-cBool // want `declared and not used: e`
	f := map[int]int{       // want `declared and not used: f`
		fInt(): <-cInt,
	}
	g := []int{<-cInt}       // want `declared and not used: g`
	h := func(s string) {}   // want `declared and not used: h`
	i := func(s string) {}() // want `declared and not used: i`
}

func commentAbove() {
	// v is a variable
	v := "s" // want `declared and not used: v`
}

func fBool() bool {
	return true
}

func fInt() int {
	return 1
}
