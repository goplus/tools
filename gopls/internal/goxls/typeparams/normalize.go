// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typeparams

import (
	"go/types"

	"golang.org/x/tools/internal/typeparams"
)

var ErrEmptyTypeSet = typeparams.ErrEmptyTypeSet

// StructuralTerms returns a slice of terms representing the normalized
// structural type restrictions of a type parameter, if any.
//
// Structural type restrictions of a type parameter are created via
// non-interface types embedded in its constraint interface (directly, or via a
// chain of interface embeddings). For example, in the declaration
//
//	type T[P interface{~int; m()}] int
//
// the structural restriction of the type parameter P is ~int.
//
// With interface embedding and unions, the specification of structural type
// restrictions may be arbitrarily complex. For example, consider the
// following:
//
//	type A interface{ ~string|~[]byte }
//
//	type B interface{ int|string }
//
//	type C interface { ~string|~int }
//
//	type T[P interface{ A|B; C }] int
//
// In this example, the structural type restriction of P is ~string|int: A|B
// expands to ~string|~[]byte|int|string, which reduces to ~string|~[]byte|int,
// which when intersected with C (~string|~int) yields ~string|int.
//
// StructuralTerms computes these expansions and reductions, producing a
// "normalized" form of the embeddings. A structural restriction is normalized
// if it is a single union containing no interface terms, and is minimal in the
// sense that removing any term changes the set of types satisfying the
// constraint. It is left as a proof for the reader that, modulo sorting, there
// is exactly one such normalized form.
//
// Because the minimal representation always takes this form, StructuralTerms
// returns a slice of tilde terms corresponding to the terms of the union in
// the normalized structural restriction. An error is returned if the
// constraint interface is invalid, exceeds complexity bounds, or has an empty
// type set. In the latter case, StructuralTerms returns ErrEmptyTypeSet.
//
// StructuralTerms makes no guarantees about the order of terms, except that it
// is deterministic.
func StructuralTerms(tparam *TypeParam) ([]*Term, error) {
	return typeparams.StructuralTerms(tparam)
}

// InterfaceTermSet computes the normalized terms for a constraint interface,
// returning an error if the term set cannot be computed or is empty. In the
// latter case, the error will be ErrEmptyTypeSet.
//
// See the documentation of StructuralTerms for more information on
// normalization.
func InterfaceTermSet(iface *types.Interface) ([]*Term, error) {
	return typeparams.InterfaceTermSet(iface)
}

// UnionTermSet computes the normalized terms for a union, returning an error
// if the term set cannot be computed or is empty. In the latter case, the
// error will be ErrEmptyTypeSet.
//
// See the documentation of StructuralTerms for more information on
// normalization.
func UnionTermSet(union *Union) ([]*Term, error) {
	return typeparams.UnionTermSet(union)
}
