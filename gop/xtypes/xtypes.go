// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xtypes

import (
	"go/types"
)

// ----------------------------------------------------------------------------

// Go+ overload extended types
type OverloadType interface {
	At(i int) types.Object
	Len() int
}

// Go+ subst extended types
type SubstType interface {
	Obj() types.Object
}

// ----------------------------------------------------------------------------
