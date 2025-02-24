// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"fmt"
	"go/types"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/token"

	"golang.org/x/tools/gop/ast/astutil"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/internal/event"
)

func GopSignatureHelp(ctx context.Context, snapshot Snapshot, fh FileHandle, position protocol.Position) ([]protocol.SignatureInformation, int, int, error) {
	ctx, done := event.Start(ctx, "source.GopSignatureHelp")
	defer done()

	// We need full type-checking here, as we must type-check function bodies in
	// order to provide signature help at the requested position.
	pkg, pgf, err := NarrowestPackageForGopFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, 0, 0, fmt.Errorf("getting file for SignatureHelp: %w", err)
	}
	pos, err := pgf.PositionPos(position)
	if err != nil {
		return nil, 0, 0, err
	}
	start, err := pgf.PositionPos(protocol.Position{0, 0})
	if err != nil {
		return nil, 0, 0, err
	}
	var offset = int(pos - start)
	npos := pos
	if len(pgf.File.Code) >= offset {
		if pgf.File.Code[offset] == '\n' {
			offset--
			npos--
		}
		for offset > 0 {
			if pgf.File.Code[offset] != ' ' {
				break
			}
			offset--
			npos--
		}
	}

	// Find a call expression surrounding the query position.
	var callExpr *ast.CallExpr
	path, _ := astutil.PathEnclosingInterval(pgf.File, npos, npos)
	if path == nil {
		return nil, 0, 0, fmt.Errorf("cannot find node enclosing position")
	}
	var cmdObj types.Object
	var cmdIdent *ast.Ident
FindCall:
	for i, node := range path {
		switch node := node.(type) {
		case *ast.Ident:
			if i == 0 && pos >= node.End() {
				if o := pkg.GopTypesInfo().ObjectOf(node); o != nil {
					if _, ok := o.Type().(*types.Signature); ok {
						cmdObj, cmdIdent = o, node
					}
				}
			}
		case *ast.CallExpr:
			if node.IsCommand() {
				if pos >= node.Fun.End() && npos <= node.NoParenEnd {
					callExpr = node
					break FindCall
				}
			} else if pos >= node.Lparen && pos <= node.Rparen {
				callExpr = node
				break FindCall
			}
		case *ast.FuncLit, *ast.FuncType, *ast.LambdaExpr, *ast.LambdaExpr2:
			// The user is within an anonymous function,
			// which may be the parameter to the *ast.CallExpr.
			// Don't show signature help in this case.
			return nil, 0, 0, fmt.Errorf("no signature help within a function declaration")
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				return nil, 0, 0, fmt.Errorf("no signature help within a string literal")
			}
		}
	}
	if (callExpr == nil || callExpr.Fun == nil) && cmdObj == nil {
		return nil, 0, 0, fmt.Errorf("cannot find an enclosing function")
	}

	qf := GopQualifier(pgf.File, pkg.GetTypes(), pkg.GopTypesInfo())

	// Get the object representing the function, if available.
	// There is no object in certain cases such as calling a function returned by
	// a function (e.g. "foo()()").
	var obj types.Object
	var ident *ast.Ident
	if callExpr != nil {
		switch t := callExpr.Fun.(type) {
		case *ast.Ident:
			ident = t
		case *ast.SelectorExpr:
			ident = t.Sel
		}
		obj = pkg.GopTypesInfo().ObjectOf(ident)
	} else {
		ident = cmdIdent
		obj = cmdObj
	}

	// Handle builtin functions separately.
	if obj, ok := obj.(*types.Builtin); ok {
		return gopBuiltinSignature(ctx, snapshot, callExpr, obj.Name(), pos)
	}

	// Get the type information for the function being called.
	var sigType types.Type
	if callExpr != nil {
		sigType = pkg.GopTypesInfo().TypeOf(callExpr.Fun)
	} else {
		sigType = cmdObj.Type()
	}
	if sigType == nil {
		return nil, 0, 0, fmt.Errorf("cannot get type for Fun %[1]T (%[1]v)", callExpr.Fun)
	}

	sig, _ := sigType.Underlying().(*types.Signature)
	if sig == nil {
		return nil, 0, 0, fmt.Errorf("cannot find signature for Fun %[1]T (%[1]v)", callExpr.Fun)
	}

	activeParam := gopActiveParameter(callExpr, sig.Params().Len(), sig.Variadic(), pos)

	_, overloads := pkg.GopTypesInfo().OverloadOf(ident)

	var (
		name    string
		comment *ast.CommentGroup
	)
	if obj != nil {
		d, err := HoverDocForObject(ctx, snapshot, pkg.FileSet(), obj)
		if err != nil && overloads == nil {
			return nil, 0, 0, err
		}
		name = obj.Name()
		comment = d
	} else {
		name = "func"
	}
	mq := MetadataQualifierForGopFile(snapshot, pgf.File, pkg.Metadata())

	makeInfo := func(name string, sig *types.Signature) (*protocol.SignatureInformation, error) {
		s, err := NewSignature(ctx, snapshot, pkg, sig, comment, qf, mq)
		if err != nil {
			return nil, err
		}
		paramInfo := make([]protocol.ParameterInformation, 0, len(s.params))
		for _, p := range s.params {
			paramInfo = append(paramInfo, protocol.ParameterInformation{Label: p})
		}
		return &protocol.SignatureInformation{
			Label:         name + s.Format(),
			Documentation: stringToSigInfoDocumentation(s.doc, snapshot.View().Options()),
			Parameters:    paramInfo,
		}, nil
	}

	if overloads != nil {
		activeSignature := 0
		infos := make([]protocol.SignatureInformation, len(overloads))
		for i, o := range overloads {
			if o.Name() == obj.Name() {
				activeSignature = i
			}
			info, err := makeInfo(o.Name(), o.Type().(*types.Signature))
			if err != nil {
				return nil, 0, 0, nil
			}
			infos[i] = *info
		}
		return infos, activeSignature, activeParam, nil
	}
	info, err := makeInfo(name, sig)
	if err != nil {
		return nil, 0, 0, err
	}
	return []protocol.SignatureInformation{*info}, 0, activeParam, nil
}

func gopBuiltinSignature(ctx context.Context, snapshot Snapshot, callExpr *ast.CallExpr, name string, pos token.Pos) ([]protocol.SignatureInformation, int, int, error) {
	sig, err := NewBuiltinSignature(ctx, snapshot, name)
	if err != nil {
		return nil, 0, 0, err
	}
	paramInfo := make([]protocol.ParameterInformation, 0, len(sig.params))
	for _, p := range sig.params {
		paramInfo = append(paramInfo, protocol.ParameterInformation{Label: p})
	}
	activeParam := gopActiveParameter(callExpr, len(sig.params), sig.variadic, pos)
	return []protocol.SignatureInformation{{
		Label:         sig.name + sig.Format(),
		Documentation: stringToSigInfoDocumentation(sig.doc, snapshot.View().Options()),
		Parameters:    paramInfo,
	}}, 0, activeParam, nil
}

func gopActiveParameter(callExpr *ast.CallExpr, numParams int, variadic bool, pos token.Pos) (activeParam int) {
	if callExpr == nil || len(callExpr.Args) == 0 {
		return 0
	}
	// First, check if the position is even in the range of the arguments.
	start, end := callExpr.Lparen, callExpr.Rparen
	if callExpr.IsCommand() {
		start, end = callExpr.Fun.Pos(), callExpr.NoParenEnd
	}
	if !(start <= pos && pos <= end) {
		return 0
	}
	for _, expr := range callExpr.Args {
		if start == token.NoPos {
			start = expr.Pos()
		}
		end = expr.End()
		if start <= pos && pos <= end {
			break
		}
		// Don't advance the active parameter for the last parameter of a variadic function.
		if !variadic || activeParam < numParams-1 {
			activeParam++
		}
		start = expr.Pos() + 1 // to account for commas
	}
	return activeParam
}
