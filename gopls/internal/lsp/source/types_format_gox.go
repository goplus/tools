// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"go/types"

	"github.com/goplus/xgo/ast"
	"github.com/goplus/xgo/token"
	"golang.org/x/tools/gopls/internal/bug"
	"golang.org/x/tools/internal/gop/typeparams"
)

func gopFormatVarType(
	snapshot Snapshot, srcpkg Package, obj *types.Var, qf types.Qualifier, mq MetadataQualifier,
	pos token.Pos, targetpgf *ParsedGopFile) (string, error) {
	targetMeta := findFileInDeps(snapshot, srcpkg.Metadata(), targetpgf.URI)
	if targetMeta == nil {
		// If we have an object from type-checking, it should exist in a file in
		// the forward transitive closure.
		return "", bug.Errorf("failed to find file %q in deps of %q", targetpgf.URI, srcpkg.Metadata().ID)
	}

	decl, spec, field := gopFindDeclInfo([]*ast.File{targetpgf.File}, pos)

	// We can't handle type parameters correctly, so we fall back on TypeString
	// for parameterized decls.
	if decl, _ := decl.(*ast.FuncDecl); decl != nil {
		if typeparams.ForFuncType(decl.Type).NumFields() > 0 {
			return types.TypeString(obj.Type(), qf), nil // in generic function
		}
		if decl.Recv != nil && len(decl.Recv.List) > 0 {
			if x, _, _, _ := typeparams.UnpackIndexExpr(decl.Recv.List[0].Type); x != nil {
				return types.TypeString(obj.Type(), qf), nil // in method of generic type
			}
		}
	}
	if spec, _ := spec.(*ast.TypeSpec); spec != nil && typeparams.ForTypeSpec(spec).NumFields() > 0 {
		return types.TypeString(obj.Type(), qf), nil // in generic type decl
	}

	if field == nil {
		// TODO(rfindley): we should never reach here from an ordinary var, so
		// should probably return an error here.
		return types.TypeString(obj.Type(), qf), nil
	}
	expr := field.Type

	rq := gopRequalifier(snapshot, targetpgf.File, targetMeta, mq)

	// The type names in the AST may not be correctly qualified.
	// Determine the package name to use based on the package that originated
	// the query and the package in which the type is declared.
	// We then qualify the value by cloning the AST node and editing it.
	expr = gopQualifyTypeExpr(expr, rq)

	// If the request came from a different package than the one in which the
	// types are defined, we may need to modify the qualifiers.
	return GopFormatNodeFile(targetpgf.Tok, expr), nil
}

// gopQualifyTypeExpr clones the type expression expr after re-qualifying type
// names using the given function, which accepts the current syntactic
// qualifier (possibly "" for unqualified idents), and returns a new qualifier
// (again, possibly "" if the identifier should be unqualified).
//
// The resulting expression may be inaccurate: without type-checking we don't
// properly account for "." imported identifiers or builtins.
//
// TODO(rfindley): add many more tests for this function.
func gopQualifyTypeExpr(expr ast.Expr, qf func(string) string) ast.Expr {
	switch expr := expr.(type) {
	case *ast.ArrayType:
		return &ast.ArrayType{
			Lbrack: expr.Lbrack,
			Elt:    gopQualifyTypeExpr(expr.Elt, qf),
			Len:    expr.Len,
		}

	case *ast.BinaryExpr:
		if expr.Op != token.OR {
			return expr
		}
		return &ast.BinaryExpr{
			X:     gopQualifyTypeExpr(expr.X, qf),
			OpPos: expr.OpPos,
			Op:    expr.Op,
			Y:     gopQualifyTypeExpr(expr.Y, qf),
		}

	case *ast.ChanType:
		return &ast.ChanType{
			Arrow: expr.Arrow,
			Begin: expr.Begin,
			Dir:   expr.Dir,
			Value: gopQualifyTypeExpr(expr.Value, qf),
		}

	case *ast.Ellipsis:
		return &ast.Ellipsis{
			Ellipsis: expr.Ellipsis,
			Elt:      gopQualifyTypeExpr(expr.Elt, qf),
		}

	case *ast.FuncType:
		return &ast.FuncType{
			Func:    expr.Func,
			Params:  gopQualifyFieldList(expr.Params, qf),
			Results: gopQualifyFieldList(expr.Results, qf),
		}

	case *ast.Ident:
		// Unqualified type (builtin, package local, or dot-imported).

		// Don't qualify names that look like builtins.
		//
		// Without type-checking this may be inaccurate. It could be made accurate
		// by doing syntactic object resolution for the entire package, but that
		// does not seem worthwhile and we generally want to avoid using
		// ast.Object, which may be inaccurate.
		if obj := types.Universe.Lookup(expr.Name); obj != nil {
			return expr
		}

		newName := qf("")
		if newName != "" {
			return &ast.SelectorExpr{
				X: &ast.Ident{
					NamePos: expr.Pos(),
					Name:    newName,
				},
				Sel: expr,
			}
		}
		return expr

	case *ast.IndexExpr:
		return &ast.IndexExpr{
			X:      gopQualifyTypeExpr(expr.X, qf),
			Lbrack: expr.Lbrack,
			Index:  gopQualifyTypeExpr(expr.Index, qf),
			Rbrack: expr.Rbrack,
		}

	case *typeparams.IndexListExpr:
		indices := make([]ast.Expr, len(expr.Indices))
		for i, idx := range expr.Indices {
			indices[i] = gopQualifyTypeExpr(idx, qf)
		}
		return &typeparams.IndexListExpr{
			X:       gopQualifyTypeExpr(expr.X, qf),
			Lbrack:  expr.Lbrack,
			Indices: indices,
			Rbrack:  expr.Rbrack,
		}

	case *ast.InterfaceType:
		return &ast.InterfaceType{
			Interface:  expr.Interface,
			Methods:    gopQualifyFieldList(expr.Methods, qf),
			Incomplete: expr.Incomplete,
		}

	case *ast.MapType:
		return &ast.MapType{
			Map:   expr.Map,
			Key:   gopQualifyTypeExpr(expr.Key, qf),
			Value: gopQualifyTypeExpr(expr.Value, qf),
		}

	case *ast.ParenExpr:
		return &ast.ParenExpr{
			Lparen: expr.Lparen,
			Rparen: expr.Rparen,
			X:      gopQualifyTypeExpr(expr.X, qf),
		}

	case *ast.SelectorExpr:
		if id, ok := expr.X.(*ast.Ident); ok {
			// qualified type
			newName := qf(id.Name)
			if newName == "" {
				return expr.Sel
			}
			return &ast.SelectorExpr{
				X: &ast.Ident{
					NamePos: id.NamePos,
					Name:    newName,
				},
				Sel: expr.Sel,
			}
		}
		return expr

	case *ast.StarExpr:
		return &ast.StarExpr{
			Star: expr.Star,
			X:    gopQualifyTypeExpr(expr.X, qf),
		}

	case *ast.StructType:
		return &ast.StructType{
			Struct:     expr.Struct,
			Fields:     gopQualifyFieldList(expr.Fields, qf),
			Incomplete: expr.Incomplete,
		}

	default:
		return expr
	}
}

func gopQualifyFieldList(fl *ast.FieldList, qf func(string) string) *ast.FieldList {
	if fl == nil {
		return nil
	}
	if fl.List == nil {
		return &ast.FieldList{
			Closing: fl.Closing,
			Opening: fl.Opening,
		}
	}
	list := make([]*ast.Field, 0, len(fl.List))
	for _, f := range fl.List {
		list = append(list, &ast.Field{
			Comment: f.Comment,
			Doc:     f.Doc,
			Names:   f.Names,
			Tag:     f.Tag,
			Type:    gopQualifyTypeExpr(f.Type, qf),
		})
	}
	return &ast.FieldList{
		Closing: fl.Closing,
		Opening: fl.Opening,
		List:    list,
	}
}
