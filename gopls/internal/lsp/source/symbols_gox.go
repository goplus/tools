// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"fmt"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/token"
	"github.com/goplus/gop/x/typesutil"
	"golang.org/x/tools/gopls/internal/goxls/parserutil"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/internal/event"
)

func GopDocumentSymbols(ctx context.Context, snapshot Snapshot, fh FileHandle) ([]protocol.DocumentSymbol, error) {
	ctx, done := event.Start(ctx, "source.GopDocumentSymbols")
	defer done()

	pgf, err := snapshot.ParseGop(ctx, fh, parserutil.ParseFull)
	if err != nil {
		return nil, fmt.Errorf("getting file for GopDocumentSymbols: %w", err)
	}
	file := pgf.File
	classType, _ := parserutil.GetClassType(file, fh.URI().Filename())
	// Build symbols for file declarations. When encountering a declaration with
	// errors (typically because positions are invalid), we skip the declaration
	// entirely. VS Code fails to show any symbols if one of the top-level
	// symbols is missing position information.
	var symbols []protocol.DocumentSymbol
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Name.Name == "_" {
				continue
			}
			fs, err := gopFuncSymbol(pgf.Mapper, pgf.Tok, decl)
			if err == nil {
				if file.IsClass {
					// class file func to method
					fs.Kind = protocol.Method
					var name = decl.Name.Name
					if decl.Shadow && name == "main" {
						if file.IsProj {
							name = "MainEntry"
						} else {
							name = "Main"
						}
					}
					if !decl.IsClass && decl.Recv != nil && len(decl.Recv.List) > 0 {
						fs.Name = fmt.Sprintf("(%s).%s", typesutil.ExprString(decl.Recv.List[0].Type), fs.Name)
					} else {
						fs.Name = fmt.Sprintf("(*%s).%s", classType, name)
					}
				} else {
					// If function is a method, prepend the type of the method.
					if decl.Recv != nil && len(decl.Recv.List) > 0 {
						fs.Name = fmt.Sprintf("(%s).%s", typesutil.ExprString(decl.Recv.List[0].Type), fs.Name)
					}
				}
				symbols = append(symbols, fs)
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if spec.Name.Name == "_" {
						continue
					}
					ts, err := gopTypeSymbol(pgf.Mapper, pgf.Tok, spec)
					if err == nil {
						symbols = append(symbols, ts)
					}
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						if name.Name == "_" {
							continue
						}
						vs, err := gopVarSymbol(pgf.Mapper, pgf.Tok, spec, name, decl.Tok == token.CONST)
						if err == nil {
							symbols = append(symbols, vs)
						}
					}
				}
			}
		}
	}
	return symbols, nil
}

func gopFuncSymbol(m *protocol.Mapper, tf *token.File, decl *ast.FuncDecl) (protocol.DocumentSymbol, error) {
	s := protocol.DocumentSymbol{
		Name: decl.Name.Name,
		Kind: protocol.Function,
	}
	if decl.Recv != nil {
		s.Kind = protocol.Method
	}
	var err error
	s.Range, err = m.NodeRange(tf, decl)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	s.SelectionRange, err = m.NodeRange(tf, decl.Name)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	s.Detail = typesutil.ExprString(decl.Type)
	return s, nil
}

func gopTypeSymbol(m *protocol.Mapper, tf *token.File, spec *ast.TypeSpec) (protocol.DocumentSymbol, error) {
	s := protocol.DocumentSymbol{
		Name: spec.Name.Name,
	}
	var err error
	s.Range, err = m.NodeRange(tf, spec)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	s.SelectionRange, err = m.NodeRange(tf, spec.Name)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	s.Kind, s.Detail, s.Children = gopTypeDetails(m, tf, spec.Type)
	return s, nil
}

func gopTypeDetails(m *protocol.Mapper, tf *token.File, typExpr ast.Expr) (kind protocol.SymbolKind, detail string, children []protocol.DocumentSymbol) {
	switch typExpr := typExpr.(type) {
	case *ast.StructType:
		kind = protocol.Struct
		children = gopFieldListSymbols(m, tf, typExpr.Fields, protocol.Field)
		if len(children) > 0 {
			detail = "struct{...}"
		} else {
			detail = "struct{}"
		}

		// Find interface methods and embedded types.
	case *ast.InterfaceType:
		kind = protocol.Interface
		children = gopFieldListSymbols(m, tf, typExpr.Methods, protocol.Method)
		if len(children) > 0 {
			detail = "interface{...}"
		} else {
			detail = "interface{}"
		}

	case *ast.FuncType:
		kind = protocol.Function
		detail = typesutil.ExprString(typExpr)

	default:
		kind = protocol.Class // catch-all, for cases where we don't know the kind syntactically
		detail = typesutil.ExprString(typExpr)
	}
	return
}

func gopFieldListSymbols(m *protocol.Mapper, tf *token.File, fields *ast.FieldList, fieldKind protocol.SymbolKind) []protocol.DocumentSymbol {
	if fields == nil {
		return nil
	}

	var symbols []protocol.DocumentSymbol
	for _, field := range fields.List {
		detail, children := "", []protocol.DocumentSymbol(nil)
		if field.Type != nil {
			_, detail, children = gopTypeDetails(m, tf, field.Type)
		}
		if len(field.Names) == 0 { // embedded interface or struct field
			// By default, use the formatted type details as the name of this field.
			// This handles potentially invalid syntax, as well as type embeddings in
			// interfaces.
			child := protocol.DocumentSymbol{
				Name:     detail,
				Kind:     protocol.Field, // consider all embeddings to be fields
				Children: children,
			}

			// If the field is a valid embedding, promote the type name to field
			// name.
			selection := field.Type
			if id := gopEmbeddedIdent(field.Type); id != nil {
				child.Name = id.Name
				child.Detail = detail
				selection = id
			}

			if rng, err := m.NodeRange(tf, field.Type); err == nil {
				child.Range = rng
			}
			if rng, err := m.NodeRange(tf, selection); err == nil {
				child.SelectionRange = rng
			}

			symbols = append(symbols, child)
		} else {
			for _, name := range field.Names {
				child := protocol.DocumentSymbol{
					Name:     name.Name,
					Kind:     fieldKind,
					Detail:   detail,
					Children: children,
				}

				if rng, err := m.NodeRange(tf, field); err == nil {
					child.Range = rng
				}
				if rng, err := m.NodeRange(tf, name); err == nil {
					child.SelectionRange = rng
				}

				symbols = append(symbols, child)
			}
		}

	}
	return symbols
}

func gopVarSymbol(m *protocol.Mapper, tf *token.File, spec *ast.ValueSpec, name *ast.Ident, isConst bool) (protocol.DocumentSymbol, error) {
	s := protocol.DocumentSymbol{
		Name: name.Name,
		Kind: protocol.Variable,
	}
	if isConst {
		s.Kind = protocol.Constant
	}
	var err error
	s.Range, err = m.NodeRange(tf, spec)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	s.SelectionRange, err = m.NodeRange(tf, name)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	if spec.Type != nil { // type may be missing from the syntax
		_, s.Detail, s.Children = gopTypeDetails(m, tf, spec.Type)
	}
	return s, nil
}
