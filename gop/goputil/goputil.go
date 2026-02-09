// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goputil

type Kind int

const (
	FileUnknown Kind = iota
	FileGopNormal
	FileGopClass
)

func FileKind(fext string) Kind {
	switch fext {
	case ".gop", ".xgo":
		return FileGopNormal
	case ".spx", ".rdx", ".yap", ".gox", ".gmx":
		return FileGopClass
	}
	return FileUnknown
}

func Exts() string {
	return "gop,xgo,spx,rdx,yap,gox,gmx"
}
