// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stubmethods

import (
	"fmt"
	"go/types"
	"strconv"

	"github.com/goplus/xgo/ast"
	"github.com/goplus/xgo/token"
	"github.com/goplus/xgo/x/typesutil"
	"golang.org/x/tools/gop/analysis"
	"golang.org/x/tools/gop/ast/astutil"
)

// GopDiagnosticForError computes a diagnostic suggesting to implement an
// interface to fix the type checking error defined by (start, end, msg).
//
// If no such fix is possible, the second result is false.
//
// TODO(rfindley): simplify this signature once the stubmethods refactoring is
// no longer wedged into the analysis framework.
func GopDiagnosticForError(fset *token.FileSet, file *ast.File, start, end token.Pos, msg string, info *typesutil.Info) (analysis.Diagnostic, bool) {
	if !MatchesMessage(msg) {
		return analysis.Diagnostic{}, false
	}

	path, _ := astutil.PathEnclosingInterval(file, start, end)
	si := GopGetStubInfo(fset, info, path, start)
	if si == nil {
		return analysis.Diagnostic{}, false
	}
	qf := GopRelativeToFiles(si.Concrete.Obj().Pkg(), file, nil, nil)
	return analysis.Diagnostic{
		Pos:     start,
		End:     end,
		Message: fmt.Sprintf("Implement %s", types.TypeString(si.Interface.Type(), qf)),
	}, true
}

// GetStubInfo determines whether the "missing method error"
// can be used to deduced what the concrete and interface types are.
//
// TODO(adonovan): this function (and its following 5 helpers) tries
// to deduce a pair of (concrete, interface) types that are related by
// an assignment, either explicitly or through a return statement or
// function call. This is essentially what the refactor/satisfy does,
// more generally. Refactor to share logic, after auditing 'satisfy'
// for safety on ill-typed code.
func GopGetStubInfo(fset *token.FileSet, ti *typesutil.Info, path []ast.Node, pos token.Pos) *StubInfo {
	for _, n := range path {
		switch n := n.(type) {
		case *ast.ValueSpec:
			return gopFromValueSpec(fset, ti, n, pos)
		case *ast.ReturnStmt:
			// An error here may not indicate a real error the user should know about, but it may.
			// Therefore, it would be best to log it out for debugging/reporting purposes instead of ignoring
			// it. However, event.Log takes a context which is not passed via the analysis package.
			// TODO(marwan-at-work): properly log this error.
			si, _ := gopFromReturnStmt(fset, ti, pos, path, n)
			return si
		case *ast.AssignStmt:
			return gopFromAssignStmt(fset, ti, n, pos)
		case *ast.CallExpr:
			// Note that some call expressions don't carry the interface type
			// because they don't point to a function or method declaration elsewhere.
			// For eaxmple, "var Interface = (*Concrete)(nil)". In that case, continue
			// this loop to encounter other possibilities such as *ast.ValueSpec or others.
			si := gopFromCallExpr(fset, ti, pos, n)
			if si != nil {
				return si
			}
		}
	}
	return nil
}

// gopFromCallExpr tries to find an *ast.CallExpr's function declaration and
// analyzes a function call's signature against the passed in parameter to deduce
// the concrete and interface types.
func gopFromCallExpr(fset *token.FileSet, ti *typesutil.Info, pos token.Pos, ce *ast.CallExpr) *StubInfo {
	paramIdx := -1
	for i, p := range ce.Args {
		if pos >= p.Pos() && pos <= p.End() {
			paramIdx = i
			break
		}
	}
	if paramIdx == -1 {
		return nil
	}
	p := ce.Args[paramIdx]
	concObj, pointer := gopConcreteType(p, ti)
	if concObj == nil || concObj.Obj().Pkg() == nil {
		return nil
	}
	tv, ok := ti.Types[ce.Fun]
	if !ok {
		return nil
	}
	sig, ok := tv.Type.(*types.Signature)
	if !ok {
		return nil
	}
	var paramType types.Type
	if sig.Variadic() && paramIdx >= sig.Params().Len()-1 {
		v := sig.Params().At(sig.Params().Len() - 1)
		if s, _ := v.Type().(*types.Slice); s != nil {
			paramType = s.Elem()
		}
	} else if paramIdx < sig.Params().Len() {
		paramType = sig.Params().At(paramIdx).Type()
	}
	if paramType == nil {
		return nil // A type error prevents us from determining the param type.
	}
	iface := ifaceObjFromType(paramType)
	if iface == nil {
		return nil
	}
	return &StubInfo{
		Fset:      fset,
		Concrete:  concObj,
		Pointer:   pointer,
		Interface: iface,
	}
}

// gopFromReturnStmt analyzes a "return" statement to extract
// a concrete type that is trying to be returned as an interface type.
//
// For example, func() io.Writer { return myType{} }
// would return StubInfo with the interface being io.Writer and the concrete type being myType{}.
func gopFromReturnStmt(fset *token.FileSet, ti *typesutil.Info, pos token.Pos, path []ast.Node, rs *ast.ReturnStmt) (*StubInfo, error) {
	returnIdx := -1
	for i, r := range rs.Results {
		if pos >= r.Pos() && pos <= r.End() {
			returnIdx = i
		}
	}
	if returnIdx == -1 {
		return nil, fmt.Errorf("pos %d not within return statement bounds: [%d-%d]", pos, rs.Pos(), rs.End())
	}
	concObj, pointer := gopConcreteType(rs.Results[returnIdx], ti)
	if concObj == nil || concObj.Obj().Pkg() == nil {
		return nil, nil
	}
	ef := gopEnclosingFunction(path, ti)
	if ef == nil {
		return nil, fmt.Errorf("could not find the enclosing function of the return statement")
	}
	iface := gopIfaceType(ef.Results.List[returnIdx].Type, ti)
	if iface == nil {
		return nil, nil
	}
	return &StubInfo{
		Fset:      fset,
		Concrete:  concObj,
		Pointer:   pointer,
		Interface: iface,
	}, nil
}

// gopFromValueSpec returns *StubInfo from a variable declaration such as
// var x io.Writer = &T{}
func gopFromValueSpec(fset *token.FileSet, ti *typesutil.Info, vs *ast.ValueSpec, pos token.Pos) *StubInfo {
	var idx int
	for i, vs := range vs.Values {
		if pos >= vs.Pos() && pos <= vs.End() {
			idx = i
			break
		}
	}

	valueNode := vs.Values[idx]
	ifaceNode := vs.Type
	callExp, ok := valueNode.(*ast.CallExpr)
	// if the ValueSpec is `var _ = myInterface(...)`
	// as opposed to `var _ myInterface = ...`
	if ifaceNode == nil && ok && len(callExp.Args) == 1 {
		ifaceNode = callExp.Fun
		valueNode = callExp.Args[0]
	}
	concObj, pointer := gopConcreteType(valueNode, ti)
	if concObj == nil || concObj.Obj().Pkg() == nil {
		return nil
	}
	ifaceObj := gopIfaceType(ifaceNode, ti)
	if ifaceObj == nil {
		return nil
	}
	return &StubInfo{
		Fset:      fset,
		Concrete:  concObj,
		Interface: ifaceObj,
		Pointer:   pointer,
	}
}

// gopFromAssignStmt returns *StubInfo from a variable re-assignment such as
// var x io.Writer
// x = &T{}
func gopFromAssignStmt(fset *token.FileSet, ti *typesutil.Info, as *ast.AssignStmt, pos token.Pos) *StubInfo {
	idx := -1
	var lhs, rhs ast.Expr
	// Given a re-assignment interface conversion error,
	// the compiler error shows up on the right hand side of the expression.
	// For example, x = &T{} where x is io.Writer highlights the error
	// under "&T{}" and not "x".
	for i, hs := range as.Rhs {
		if pos >= hs.Pos() && pos <= hs.End() {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil
	}
	// Technically, this should never happen as
	// we would get a "cannot assign N values to M variables"
	// before we get an interface conversion error. Nonetheless,
	// guard against out of range index errors.
	if idx >= len(as.Lhs) {
		return nil
	}
	lhs, rhs = as.Lhs[idx], as.Rhs[idx]
	ifaceObj := gopIfaceType(lhs, ti)
	if ifaceObj == nil {
		return nil
	}
	concType, pointer := gopConcreteType(rhs, ti)
	if concType == nil || concType.Obj().Pkg() == nil {
		return nil
	}
	return &StubInfo{
		Fset:      fset,
		Concrete:  concType,
		Interface: ifaceObj,
		Pointer:   pointer,
	}
}

// GopRelativeToFiles returns a types.Qualifier that formats package
// names according to the import environments of the files that define
// the concrete type and the interface type. (Only the imports of the
// latter file are provided.)
//
// This is similar to types.RelativeTo except if a file imports the package with a different name,
// then it will use it. And if the file does import the package but it is ignored,
// then it will return the original name. It also prefers package names in importEnv in case
// an import is missing from concFile but is present among importEnv.
//
// Additionally, if missingImport is not nil, the function will be called whenever the concFile
// is presented with a package that is not imported. This is useful so that as types.TypeString is
// formatting a function signature, it is identifying packages that will need to be imported when
// stubbing an interface.
//
// TODO(rfindley): investigate if this can be merged with source.Qualifier.
func GopRelativeToFiles(concPkg *types.Package, concFile *ast.File, ifaceImports []*ast.ImportSpec, missingImport func(name, path string)) types.Qualifier {
	return func(other *types.Package) string {
		if other == concPkg {
			return ""
		}

		// Check if the concrete file already has the given import,
		// if so return the default package name or the renamed import statement.
		for _, imp := range concFile.Imports {
			impPath, _ := strconv.Unquote(imp.Path.Value)
			isIgnored := imp.Name != nil && (imp.Name.Name == "." || imp.Name.Name == "_")
			// TODO(adonovan): this comparison disregards a vendor prefix in 'other'.
			if impPath == other.Path() && !isIgnored {
				importName := other.Name()
				if imp.Name != nil {
					importName = imp.Name.Name
				}
				return importName
			}
		}

		// If the concrete file does not have the import, check if the package
		// is renamed in the interface file and prefer that.
		var importName string
		for _, imp := range ifaceImports {
			impPath, _ := strconv.Unquote(imp.Path.Value)
			isIgnored := imp.Name != nil && (imp.Name.Name == "." || imp.Name.Name == "_")
			// TODO(adonovan): this comparison disregards a vendor prefix in 'other'.
			if impPath == other.Path() && !isIgnored {
				if imp.Name != nil && imp.Name.Name != concPkg.Name() {
					importName = imp.Name.Name
				}
				break
			}
		}

		if missingImport != nil {
			missingImport(importName, other.Path())
		}

		// Up until this point, importName must stay empty when calling missingImport,
		// otherwise we'd end up with `import time "time"` which doesn't look idiomatic.
		if importName == "" {
			importName = other.Name()
		}
		return importName
	}
}

// gopIfaceType will try to extract the types.Object that defines
// the interface given the ast.Expr where the "missing method"
// or "conversion" errors happen.
func gopIfaceType(n ast.Expr, ti *typesutil.Info) *types.TypeName {
	tv, ok := ti.Types[n]
	if !ok {
		return nil
	}
	return ifaceObjFromType(tv.Type)
}

// gopConcreteType tries to extract the *types.Named that defines
// the concrete type given the ast.Expr where the "missing method"
// or "conversion" errors happened. If the concrete type is something
// that cannot have methods defined on it (such as basic types), this
// method will return a nil *types.Named. The second return parameter
// is a boolean that indicates whether the concreteType was defined as a
// pointer or value.
func gopConcreteType(n ast.Expr, ti *typesutil.Info) (*types.Named, bool) {
	tv, ok := ti.Types[n]
	if !ok {
		return nil, false
	}
	typ := tv.Type
	ptr, isPtr := typ.(*types.Pointer)
	if isPtr {
		typ = ptr.Elem()
	}
	named, ok := typ.(*types.Named)
	if !ok {
		return nil, false
	}
	return named, isPtr
}

// gopEnclosingFunction returns the signature and type of the function
// enclosing the given position.
func gopEnclosingFunction(path []ast.Node, info *typesutil.Info) *ast.FuncType {
	for _, node := range path {
		switch t := node.(type) {
		case *ast.FuncDecl:
			if _, ok := info.Defs[t.Name]; ok {
				return t.Type
			}
		case *ast.FuncLit:
			if _, ok := info.Types[t]; ok {
				return t.Type
			}
		}
	}
	return nil
}
