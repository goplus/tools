// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package expect

import (
	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/token"
)

func ExtractGop(fset *token.FileSet, file *ast.File) ([]*Note, error) {
	var notes []*Note
	for _, g := range file.Comments {
		for _, c := range g.List {
			text, adjust := getAdjustedNote(c.Text)
			if text == "" {
				continue
			}
			parsed, err := parse(fset, token.Pos(int(c.Pos())+adjust), text)
			if err != nil {
				return nil, err
			}
			notes = append(notes, parsed...)
		}
	}
	return notes, nil
}
