// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typeparams

import (
	"go/types"

	"github.com/goplus/gop/ast"
	"golang.org/x/tools/gopls/internal/goxls/typesutil"
	"golang.org/x/tools/internal/typeparams"
)

// IndexListExpr is an alias for ast.IndexListExpr.
type IndexListExpr = ast.IndexListExpr

// ForTypeSpec returns n.TypeParams.
func ForTypeSpec(n *ast.TypeSpec) *ast.FieldList {
	if n == nil {
		return nil
	}
	return n.TypeParams
}

// ForFuncType returns n.TypeParams.
func ForFuncType(n *ast.FuncType) *ast.FieldList {
	if n == nil {
		return nil
	}
	return n.TypeParams
}

// TypeParam is an alias for types.TypeParam
type TypeParam = typeparams.TypeParam

// TypeParamList is an alias for types.TypeParamList
type TypeParamList = typeparams.TypeParamList

// TypeList is an alias for types.TypeList
type TypeList = typeparams.TypeList

// NewTypeParam calls types.NewTypeParam.
func NewTypeParam(name *types.TypeName, constraint types.Type) *TypeParam {
	return typeparams.NewTypeParam(name, constraint)
}

// SetTypeParamConstraint calls tparam.SetConstraint(constraint).
func SetTypeParamConstraint(tparam *TypeParam, constraint types.Type) {
	typeparams.SetTypeParamConstraint(tparam, constraint)
}

// NewSignatureType calls types.NewSignatureType.
func NewSignatureType(recv *types.Var, recvTypeParams, typeParams []*TypeParam, params, results *types.Tuple, variadic bool) *types.Signature {
	return typeparams.NewSignatureType(recv, recvTypeParams, typeParams, params, results, variadic)
}

// ForSignature returns sig.TypeParams()
func ForSignature(sig *types.Signature) *TypeParamList {
	return typeparams.ForSignature(sig)
}

// RecvTypeParams returns sig.RecvTypeParams().
func RecvTypeParams(sig *types.Signature) *TypeParamList {
	return typeparams.RecvTypeParams(sig)
}

// IsComparable calls iface.IsComparable().
func IsComparable(iface *types.Interface) bool {
	return typeparams.IsComparable(iface)
}

// IsMethodSet calls iface.IsMethodSet().
func IsMethodSet(iface *types.Interface) bool {
	return typeparams.IsMethodSet(iface)
}

// IsImplicit calls iface.IsImplicit().
func IsImplicit(iface *types.Interface) bool {
	return typeparams.IsImplicit(iface)
}

// MarkImplicit calls iface.MarkImplicit().
func MarkImplicit(iface *types.Interface) {
	typeparams.MarkImplicit(iface)
}

// ForNamed extracts the (possibly empty) type parameter object list from
// named.
func ForNamed(named *types.Named) *TypeParamList {
	return typeparams.ForNamed(named)
}

// SetForNamed sets the type params tparams on n. Each tparam must be of
// dynamic type *types.TypeParam.
func SetForNamed(n *types.Named, tparams []*TypeParam) {
	typeparams.SetForNamed(n, tparams)
}

// NamedTypeArgs returns named.TypeArgs().
func NamedTypeArgs(named *types.Named) *TypeList {
	return typeparams.NamedTypeArgs(named)
}

// NamedTypeOrigin returns named.Orig().
func NamedTypeOrigin(named *types.Named) *types.Named {
	return typeparams.NamedTypeOrigin(named)
}

// Term is an alias for types.Term.
type Term = typeparams.Term

// NewTerm calls types.NewTerm.
func NewTerm(tilde bool, typ types.Type) *Term {
	return typeparams.NewTerm(tilde, typ)
}

// Union is an alias for types.Union
type Union = typeparams.Union

// NewUnion calls types.NewUnion.
func NewUnion(terms []*Term) *Union {
	return typeparams.NewUnion(terms)
}

// InitInstanceInfo initializes info to record information about type and
// function instances.
func InitInstanceInfo(info *typesutil.Info) {
	info.Instances = make(map[*ast.Ident]types.Instance)
}

// Instance is an alias for types.Instance.
type Instance = typeparams.Instance

// GetInstances returns info.Instances.
func GetInstances(info *typesutil.Info) map[*ast.Ident]Instance {
	return info.Instances
}

// Context is an alias for types.Context.
type Context = typeparams.Context

// NewContext calls types.NewContext.
func NewContext() *Context {
	return typeparams.NewContext()
}

// Instantiate calls types.Instantiate.
func Instantiate(ctxt *Context, typ types.Type, targs []types.Type, validate bool) (types.Type, error) {
	return typeparams.Instantiate(ctxt, typ, targs, validate)
}
