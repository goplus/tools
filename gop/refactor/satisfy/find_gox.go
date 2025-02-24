// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package satisfy

import (
	"fmt"
	"go/types"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/token"
	"github.com/goplus/gop/x/typesutil"
	"golang.org/x/tools/gop/ast/astutil"
	"golang.org/x/tools/internal/gop/typeparams"
)

func (f *Finder) gopFind(info *typesutil.Info, files []*ast.File) {
	f.gopInfo = info
	for _, file := range files {
		for _, d := range file.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				if d.Tok == token.VAR { // ignore consts
					for _, spec := range d.Specs {
						f.gopValueSpec(spec.(*ast.ValueSpec))
					}
				}

			case *ast.FuncDecl:
				if d.Body != nil {
					f.sig = info.Defs[d.Name].Type().(*types.Signature)
					f.gopStmt(d.Body)
					f.sig = nil
				}
			}
		}
	}
	f.gopInfo = nil
}

// gopExprN visits an expression in a multi-value context.
func (f *Finder) gopExprN(e ast.Expr) types.Type {
	typ := f.gopInfo.Types[e].Type.(*types.Tuple)
	switch e := e.(type) {
	case *ast.ParenExpr:
		return f.gopExprN(e.X)

	case *ast.CallExpr:
		// x, err := f(args)
		sig := coreType(f.gopExpr(e.Fun)).(*types.Signature)
		f.gopCall(sig, e.Args)

	case *ast.IndexExpr:
		// y, ok := x[i]
		x := f.gopExpr(e.X)
		f.assign(f.gopExpr(e.Index), coreType(x).(*types.Map).Key())

	case *ast.TypeAssertExpr:
		// y, ok := x.(T)
		f.typeAssert(f.gopExpr(e.X), typ.At(0).Type())

	case *ast.UnaryExpr: // must be receive <-
		// y, ok := <-x
		f.gopExpr(e.X)

	default:
		panic(e)
	}
	return typ
}

func (f *Finder) gopCall(sig *types.Signature, args []ast.Expr) {
	if len(args) == 0 {
		return
	}

	// Ellipsis call?  e.g. f(x, y, z...)
	if _, ok := args[len(args)-1].(*ast.Ellipsis); ok {
		for i, arg := range args {
			// The final arg is a slice, and so is the final param.
			f.assign(sig.Params().At(i).Type(), f.gopExpr(arg))
		}
		return
	}

	var argtypes []types.Type

	// Gather the effective actual parameter types.
	if tuple, ok := f.gopInfo.Types[args[0]].Type.(*types.Tuple); ok {
		// f(g()) call where g has multiple results?
		f.gopExpr(args[0])
		// unpack the tuple
		for i := 0; i < tuple.Len(); i++ {
			argtypes = append(argtypes, tuple.At(i).Type())
		}
	} else {
		for _, arg := range args {
			argtypes = append(argtypes, f.gopExpr(arg))
		}
	}

	// Assign the actuals to the formals.
	if !sig.Variadic() {
		for i, argtype := range argtypes {
			f.assign(sig.Params().At(i).Type(), argtype)
		}
	} else {
		// The first n-1 parameters are assigned normally.
		nnormals := sig.Params().Len() - 1
		for i, argtype := range argtypes[:nnormals] {
			f.assign(sig.Params().At(i).Type(), argtype)
		}
		// Remaining args are assigned to elements of varargs slice.
		tElem := sig.Params().At(nnormals).Type().(*types.Slice).Elem()
		for i := nnormals; i < len(argtypes); i++ {
			f.assign(tElem, argtypes[i])
		}
	}
}

// gopBuiltin visits the arguments of a builtin type with signature sig.
func (f *Finder) gopBuiltin(obj *types.Builtin, sig *types.Signature, args []ast.Expr) {
	switch obj.Name() {
	case "make", "new":
		// skip the type operand
		for _, arg := range args[1:] {
			f.gopExpr(arg)
		}

	case "append":
		s := f.gopExpr(args[0])
		if _, ok := args[len(args)-1].(*ast.Ellipsis); ok && len(args) == 2 {
			// append(x, y...)   including append([]byte, "foo"...)
			f.gopExpr(args[1])
		} else {
			// append(x, y, z)
			tElem := coreType(s).(*types.Slice).Elem()
			for _, arg := range args[1:] {
				f.assign(tElem, f.gopExpr(arg))
			}
		}

	case "delete":
		m := f.gopExpr(args[0])
		k := f.gopExpr(args[1])
		f.assign(coreType(m).(*types.Map).Key(), k)

	default:
		// ordinary call
		f.gopCall(sig, args)
	}
}

func (f *Finder) gopValueSpec(spec *ast.ValueSpec) {
	var T types.Type
	if spec.Type != nil {
		T = f.gopInfo.Types[spec.Type].Type
	}
	switch len(spec.Values) {
	case len(spec.Names): // e.g. var x, y = f(), g()
		for _, value := range spec.Values {
			v := f.gopExpr(value)
			if T != nil {
				f.assign(T, v)
			}
		}

	case 1: // e.g. var x, y = f()
		tuple := f.gopExprN(spec.Values[0])
		for i := range spec.Names {
			if T != nil {
				f.assign(T, f.extract(tuple, i))
			}
		}
	}
}

// gopExpr visits a true expression (not a type or defining ident)
// and returns its type.
func (f *Finder) gopExpr(e ast.Expr) types.Type {
	tv := f.gopInfo.Types[e]
	if tv.Value != nil {
		return tv.Type // prune the descent for constants
	}

	// tv.Type may be nil for an ast.Ident.

	switch e := e.(type) {
	case *ast.BadExpr, *ast.BasicLit:
		// no-op

	case *ast.Ident:
		// (referring idents only)
		if obj, ok := f.gopInfo.Uses[e]; ok {
			return obj.Type()
		}
		if e.Name == "_" { // e.g. "for _ = range x"
			return tInvalid
		}
		panic("undefined ident: " + e.Name)

	case *ast.Ellipsis:
		if e.Elt != nil {
			f.gopExpr(e.Elt)
		}

	case *ast.FuncLit:
		saved := f.sig
		f.sig = tv.Type.(*types.Signature)
		f.gopStmt(e.Body)
		f.sig = saved

	case *ast.CompositeLit:
		switch T := coreType(deref(tv.Type)).(type) {
		case *types.Struct:
			for i, elem := range e.Elts {
				if kv, ok := elem.(*ast.KeyValueExpr); ok {
					f.assign(f.gopInfo.Uses[kv.Key.(*ast.Ident)].Type(), f.gopExpr(kv.Value))
				} else {
					f.assign(T.Field(i).Type(), f.gopExpr(elem))
				}
			}

		case *types.Map:
			for _, elem := range e.Elts {
				elem := elem.(*ast.KeyValueExpr)
				f.assign(T.Key(), f.gopExpr(elem.Key))
				f.assign(T.Elem(), f.gopExpr(elem.Value))
			}

		case *types.Array, *types.Slice:
			tElem := T.(interface {
				Elem() types.Type
			}).Elem()
			for _, elem := range e.Elts {
				if kv, ok := elem.(*ast.KeyValueExpr); ok {
					// ignore the key
					f.assign(tElem, f.gopExpr(kv.Value))
				} else {
					f.assign(tElem, f.gopExpr(elem))
				}
			}

		default:
			panic(fmt.Sprintf("unexpected composite literal type %T: %v", tv.Type, tv.Type.String()))
		}

	case *ast.ParenExpr:
		f.gopExpr(e.X)

	case *ast.SelectorExpr:
		if _, ok := f.gopInfo.Selections[e]; ok {
			f.gopExpr(e.X) // selection
		} else {
			return f.gopInfo.Uses[e.Sel].Type() // qualified identifier
		}

	case *ast.IndexExpr:
		if gopInstance(f.gopInfo, e.X) {
			// f[T] or C[T] -- generic instantiation
		} else {
			// x[i] or m[k] -- index or lookup operation
			x := f.gopExpr(e.X)
			i := f.gopExpr(e.Index)
			if ux, ok := coreType(x).(*types.Map); ok {
				f.assign(ux.Key(), i)
			}
		}

	case *typeparams.IndexListExpr:
		// f[X, Y] -- generic instantiation

	case *ast.SliceExpr:
		f.gopExpr(e.X)
		if e.Low != nil {
			f.gopExpr(e.Low)
		}
		if e.High != nil {
			f.gopExpr(e.High)
		}
		if e.Max != nil {
			f.gopExpr(e.Max)
		}

	case *ast.TypeAssertExpr:
		x := f.gopExpr(e.X)
		f.typeAssert(x, f.gopInfo.Types[e.Type].Type)

	case *ast.CallExpr:
		if tvFun := f.gopInfo.Types[e.Fun]; tvFun.IsType() {
			// conversion
			arg0 := f.gopExpr(e.Args[0])
			f.assign(tvFun.Type, arg0)
		} else {
			// function call

			// unsafe call. Treat calls to functions in unsafe like ordinary calls,
			// except that their signature cannot be determined by their func obj.
			// Without this special handling, f.expr(e.Fun) would fail below.
			if s, ok := gopUnparen(e.Fun).(*ast.SelectorExpr); ok {
				if obj, ok := f.gopInfo.Uses[s.Sel].(*types.Builtin); ok && obj.Pkg().Path() == "unsafe" {
					sig := f.gopInfo.Types[e.Fun].Type.(*types.Signature)
					f.gopCall(sig, e.Args)
					return tv.Type
				}
			}

			// builtin call
			if id, ok := gopUnparen(e.Fun).(*ast.Ident); ok {
				if obj, ok := f.gopInfo.Uses[id].(*types.Builtin); ok {
					sig := f.gopInfo.Types[id].Type.(*types.Signature)
					f.gopBuiltin(obj, sig, e.Args)
					return tv.Type
				}
			}

			// ordinary call
			f.gopCall(coreType(f.gopExpr(e.Fun)).(*types.Signature), e.Args)
		}

	case *ast.StarExpr:
		f.gopExpr(e.X)

	case *ast.UnaryExpr:
		f.gopExpr(e.X)

	case *ast.BinaryExpr:
		x := f.gopExpr(e.X)
		y := f.gopExpr(e.Y)
		if e.Op == token.EQL || e.Op == token.NEQ {
			f.compare(x, y)
		}

	case *ast.KeyValueExpr:
		f.gopExpr(e.Key)
		f.gopExpr(e.Value)

	case *ast.ArrayType,
		*ast.StructType,
		*ast.FuncType,
		*ast.InterfaceType,
		*ast.MapType,
		*ast.ChanType:
		panic(e)
	}

	if tv.Type == nil {
		panic(fmt.Sprintf("no type for %T", e))
	}

	return tv.Type
}

func (f *Finder) gopStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.BadStmt,
		*ast.EmptyStmt,
		*ast.BranchStmt:
		// no-op

	case *ast.DeclStmt:
		d := s.Decl.(*ast.GenDecl)
		if d.Tok == token.VAR { // ignore consts
			for _, spec := range d.Specs {
				f.gopValueSpec(spec.(*ast.ValueSpec))
			}
		}

	case *ast.LabeledStmt:
		f.gopStmt(s.Stmt)

	case *ast.ExprStmt:
		f.gopExpr(s.X)

	case *ast.SendStmt:
		ch := f.gopExpr(s.Chan)
		switch t := coreType(ch).(type) {
		case *types.Chan:
			val := f.gopExpr(s.Values[0])
			f.assign(t.Elem(), val)
		case *types.Slice:
			for _, v := range s.Values {
				val := f.gopExpr(v)
				f.assign(t.Elem(), val)
			}
		}
	case *ast.IncDecStmt:
		f.gopExpr(s.X)

	case *ast.AssignStmt:
		switch s.Tok {
		case token.ASSIGN, token.DEFINE:
			// y := x   or   y = x
			var rhsTuple types.Type
			if len(s.Lhs) != len(s.Rhs) {
				rhsTuple = f.gopExprN(s.Rhs[0])
			}
			for i := range s.Lhs {
				var lhs, rhs types.Type
				if rhsTuple == nil {
					rhs = f.gopExpr(s.Rhs[i]) // 1:1 assignment
				} else {
					rhs = f.extract(rhsTuple, i) // n:1 assignment
				}

				if id, ok := s.Lhs[i].(*ast.Ident); ok {
					if id.Name != "_" {
						if obj, ok := f.gopInfo.Defs[id]; ok {
							lhs = obj.Type() // definition
						}
					}
				}
				if lhs == nil {
					lhs = f.gopExpr(s.Lhs[i]) // assignment
				}
				f.assign(lhs, rhs)
			}

		default:
			// y op= x
			f.gopExpr(s.Lhs[0])
			f.gopExpr(s.Rhs[0])
		}

	case *ast.GoStmt:
		f.gopExpr(s.Call)

	case *ast.DeferStmt:
		f.gopExpr(s.Call)

	case *ast.ReturnStmt:
		formals := f.sig.Results()
		switch len(s.Results) {
		case formals.Len(): // 1:1
			for i, result := range s.Results {
				f.assign(formals.At(i).Type(), f.gopExpr(result))
			}

		case 1: // n:1
			tuple := f.gopExprN(s.Results[0])
			for i := 0; i < formals.Len(); i++ {
				f.assign(formals.At(i).Type(), f.extract(tuple, i))
			}
		}

	case *ast.SelectStmt:
		f.gopStmt(s.Body)

	case *ast.BlockStmt:
		for _, s := range s.List {
			f.gopStmt(s)
		}

	case *ast.IfStmt:
		if s.Init != nil {
			f.gopStmt(s.Init)
		}
		f.gopExpr(s.Cond)
		f.gopStmt(s.Body)
		if s.Else != nil {
			f.gopStmt(s.Else)
		}

	case *ast.SwitchStmt:
		if s.Init != nil {
			f.gopStmt(s.Init)
		}
		var tag types.Type = tUntypedBool
		if s.Tag != nil {
			tag = f.gopExpr(s.Tag)
		}
		for _, cc := range s.Body.List {
			cc := cc.(*ast.CaseClause)
			for _, cond := range cc.List {
				f.compare(tag, f.gopInfo.Types[cond].Type)
			}
			for _, s := range cc.Body {
				f.gopStmt(s)
			}
		}

	case *ast.TypeSwitchStmt:
		if s.Init != nil {
			f.gopStmt(s.Init)
		}
		var I types.Type
		switch ass := s.Assign.(type) {
		case *ast.ExprStmt: // x.(type)
			I = f.gopExpr(gopUnparen(ass.X).(*ast.TypeAssertExpr).X)
		case *ast.AssignStmt: // y := x.(type)
			I = f.gopExpr(gopUnparen(ass.Rhs[0]).(*ast.TypeAssertExpr).X)
		}
		for _, cc := range s.Body.List {
			cc := cc.(*ast.CaseClause)
			for _, cond := range cc.List {
				tCase := f.gopInfo.Types[cond].Type
				if tCase != tUntypedNil {
					f.typeAssert(I, tCase)
				}
			}
			for _, s := range cc.Body {
				f.gopStmt(s)
			}
		}

	case *ast.CommClause:
		if s.Comm != nil {
			f.gopStmt(s.Comm)
		}
		for _, s := range s.Body {
			f.gopStmt(s)
		}

	case *ast.ForStmt:
		if s.Init != nil {
			f.gopStmt(s.Init)
		}
		if s.Cond != nil {
			f.gopExpr(s.Cond)
		}
		if s.Post != nil {
			f.gopStmt(s.Post)
		}
		f.gopStmt(s.Body)

	case *ast.RangeStmt:
		x := f.gopExpr(s.X)
		// No conversions are involved when Tok==DEFINE.
		if s.Tok == token.ASSIGN {
			if s.Key != nil {
				k := f.gopExpr(s.Key)
				var xelem types.Type
				// Keys of array, *array, slice, string aren't interesting
				// since the RHS key type is just an int.
				switch ux := coreType(x).(type) {
				case *types.Chan:
					xelem = ux.Elem()
				case *types.Map:
					xelem = ux.Key()
				}
				if xelem != nil {
					f.assign(k, xelem)
				}
			}
			if s.Value != nil {
				val := f.gopExpr(s.Value)
				var xelem types.Type
				// Values of type strings aren't interesting because
				// the RHS value type is just a rune.
				switch ux := coreType(x).(type) {
				case *types.Array:
					xelem = ux.Elem()
				case *types.Map:
					xelem = ux.Elem()
				case *types.Pointer: // *array
					xelem = coreType(deref(ux)).(*types.Array).Elem()
				case *types.Slice:
					xelem = ux.Elem()
				}
				if xelem != nil {
					f.assign(val, xelem)
				}
			}
		}
		f.gopStmt(s.Body)

	default:
		panic(s)
	}
}

// -- Plundered from golang.org/x/tools/go/ssa -----------------

func gopUnparen(e ast.Expr) ast.Expr { return astutil.Unparen(e) }

func gopInstance(info *typesutil.Info, expr ast.Expr) bool {
	var id *ast.Ident
	switch x := expr.(type) {
	case *ast.Ident:
		id = x
	case *ast.SelectorExpr:
		id = x.Sel
	default:
		return false
	}
	_, ok := typeparams.GetInstances(info)[id]
	return ok
}
