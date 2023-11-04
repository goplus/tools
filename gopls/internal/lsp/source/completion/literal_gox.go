// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"context"
	"fmt"
	"go/types"
	"strings"

	"golang.org/x/tools/gopls/internal/goxls/typeparams"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/snippet"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/internal/event"
)

// literal generates composite literal, function literal, and make()
// completion items.
func (c *gopCompleter) literal(ctx context.Context, literalType types.Type, imp *importInfo) {
	if !c.opts.literal {
		return
	}

	expType := c.inference.objType

	if c.inference.matchesVariadic(literalType) {
		// Don't offer literal slice candidates for variadic arguments.
		// For example, don't offer "[]interface{}{}" in "fmt.Print(<>)".
		return
	}

	// Avoid literal candidates if the expected type is an empty
	// interface. It isn't very useful to suggest a literal candidate of
	// every possible type.
	if expType != nil && isEmptyInterface(expType) {
		return
	}

	// We handle unnamed literal completions explicitly before searching
	// for candidates. Avoid named-type literal completions for
	// unnamed-type expected type since that results in duplicate
	// candidates. For example, in
	//
	// type mySlice []int
	// var []int = <>
	//
	// don't offer "mySlice{}" since we have already added a candidate
	// of "[]int{}".
	if _, named := literalType.(*types.Named); named && expType != nil {
		if _, named := source.Deref(expType).(*types.Named); !named {
			return
		}
	}

	// Check if an object of type literalType would match our expected type.
	cand := candidate{
		obj: c.fakeObj(literalType),
	}

	switch literalType.Underlying().(type) {
	// These literal types are addressable (e.g. "&[]int{}"), others are
	// not (e.g. can't do "&(func(){})").
	case *types.Struct, *types.Array, *types.Slice, *types.Map:
		cand.addressable = true
	}

	if !c.matchingCandidate(&cand) || cand.convertTo != nil {
		return
	}

	var (
		qf  = c.qf
		sel = enclosingSelector(c.path, c.pos)
	)

	// Don't qualify the type name if we are in a selector expression
	// since the package name is already present.
	if sel != nil {
		qf = func(_ *types.Package) string { return "" }
	}

	snip, typeName := c.typeNameSnippet(literalType, qf)

	// A type name of "[]int" doesn't work very will with the matcher
	// since "[" isn't a valid identifier prefix. Here we strip off the
	// slice (and array) prefix yielding just "int".
	matchName := typeName
	switch t := literalType.(type) {
	case *types.Slice:
		matchName = types.TypeString(t.Elem(), qf)
	case *types.Array:
		matchName = types.TypeString(t.Elem(), qf)
	}

	addlEdits, err := c.importEdits(imp)
	if err != nil {
		event.Error(ctx, "error adding import for literal candidate", err)
		return
	}

	// If prefix matches the type name, client may want a composite literal.
	if score := c.matcher.Score(matchName); score > 0 {
		if cand.hasMod(reference) {
			if sel != nil {
				// If we are in a selector we must place the "&" before the selector.
				// For example, "foo.B<>" must complete to "&foo.Bar{}", not
				// "foo.&Bar{}".
				edits, err := c.editText(sel.Pos(), sel.Pos(), "&")
				if err != nil {
					event.Error(ctx, "error making edit for literal pointer completion", err)
					return
				}
				addlEdits = append(addlEdits, edits...)
			} else {
				// Otherwise we can stick the "&" directly before the type name.
				typeName = "&" + typeName
				snip.PrependText("&")
			}
		}

		switch t := literalType.Underlying().(type) {
		case *types.Struct, *types.Array, *types.Slice, *types.Map:
			c.compositeLiteral(t, snip.Clone(), typeName, float64(score), addlEdits)
		case *types.Signature:
			// Add a literal completion for a signature type that implements
			// an interface. For example, offer "http.HandlerFunc()" when
			// expected type is "http.Handler".
			if expType != nil && types.IsInterface(expType) {
				c.basicLiteral(t, snip.Clone(), typeName, float64(score), addlEdits)
			}
		case *types.Basic:
			// Add a literal completion for basic types that implement our
			// expected interface (e.g. named string type http.Dir
			// implements http.FileSystem), or are identical to our expected
			// type (i.e. yielding a type conversion such as "float64()").
			if expType != nil && (types.IsInterface(expType) || types.Identical(expType, literalType)) {
				c.basicLiteral(t, snip.Clone(), typeName, float64(score), addlEdits)
			}
		}
	}

	// If prefix matches "make", client may want a "make()"
	// invocation. We also include the type name to allow for more
	// flexible fuzzy matching.
	if score := c.matcher.Score("make." + matchName); !cand.hasMod(reference) && score > 0 {
		switch literalType.Underlying().(type) {
		case *types.Slice:
			// The second argument to "make()" for slices is required, so default to "0".
			c.makeCall(snip.Clone(), typeName, "0", float64(score), addlEdits)
		case *types.Map, *types.Chan:
			// Maps and channels don't require the second argument, so omit
			// to keep things simple for now.
			c.makeCall(snip.Clone(), typeName, "", float64(score), addlEdits)
		}
	}

	// If prefix matches "func", client may want a function literal.
	if score := c.matcher.Score("func"); !cand.hasMod(reference) && score > 0 && (expType == nil || !types.IsInterface(expType)) {
		switch t := literalType.Underlying().(type) {
		case *types.Signature:
			c.functionLiteral(ctx, t, float64(score))
		}
	}
}

// functionLiteral adds a function literal completion item for the
// given signature.
func (c *gopCompleter) functionLiteral(ctx context.Context, sig *types.Signature, matchScore float64) {
	snip := &snippet.Builder{}
	snip.WriteText("func(")

	// First we generate names for each param and keep a seen count so
	// we know if we need to uniquify param names. For example,
	// "func(int)" will become "func(i int)", but "func(int, int64)"
	// will become "func(i1 int, i2 int64)".
	var (
		paramNames     = make([]string, sig.Params().Len())
		paramNameCount = make(map[string]int)
		hasTypeParams  bool
	)
	for i := 0; i < sig.Params().Len(); i++ {
		var (
			p    = sig.Params().At(i)
			name = p.Name()
		)

		if tp, _ := p.Type().(*typeparams.TypeParam); tp != nil && !c.typeParamInScope(tp) {
			hasTypeParams = true
		}

		if name == "" {
			// If the param has no name in the signature, guess a name based
			// on the type. Use an empty qualifier to ignore the package.
			// For example, we want to name "http.Request" "r", not "hr".
			typeName, err := source.FormatVarType(ctx, c.snapshot, c.pkg, p,
				func(p *types.Package) string { return "" },
				func(source.PackageName, source.ImportPath, source.PackagePath) string { return "" })
			if err != nil {
				// In general, the only error we should encounter while formatting is
				// context cancellation.
				if ctx.Err() == nil {
					event.Error(ctx, "formatting var type", err)
				}
				return
			}
			name = abbreviateTypeName(typeName)
		}
		paramNames[i] = name
		if name != "_" {
			paramNameCount[name]++
		}
	}

	for n, c := range paramNameCount {
		// Any names we saw more than once will need a unique suffix added
		// on. Reset the count to 1 to act as the suffix for the first
		// name.
		if c >= 2 {
			paramNameCount[n] = 1
		} else {
			delete(paramNameCount, n)
		}
	}

	for i := 0; i < sig.Params().Len(); i++ {
		if hasTypeParams && !c.opts.placeholders {
			// If there are type params in the args then the user must
			// choose the concrete types. If placeholders are disabled just
			// drop them between the parens and let them fill things in.
			snip.WritePlaceholder(nil)
			break
		}

		if i > 0 {
			snip.WriteText(", ")
		}

		var (
			p    = sig.Params().At(i)
			name = paramNames[i]
		)

		// Uniquify names by adding on an incrementing numeric suffix.
		if idx, found := paramNameCount[name]; found {
			paramNameCount[name]++
			name = fmt.Sprintf("%s%d", name, idx)
		}

		if name != p.Name() && c.opts.placeholders {
			// If we didn't use the signature's param name verbatim then we
			// may have chosen a poor name. Give the user a placeholder so
			// they can easily fix the name.
			snip.WritePlaceholder(func(b *snippet.Builder) {
				b.WriteText(name)
			})
		} else {
			snip.WriteText(name)
		}

		// If the following param's type is identical to this one, omit
		// this param's type string. For example, emit "i, j int" instead
		// of "i int, j int".
		if i == sig.Params().Len()-1 || !types.Identical(p.Type(), sig.Params().At(i+1).Type()) {
			snip.WriteText(" ")
			typeStr, err := source.FormatVarType(ctx, c.snapshot, c.pkg, p, c.qf, c.mq)
			if err != nil {
				// In general, the only error we should encounter while formatting is
				// context cancellation.
				if ctx.Err() == nil {
					event.Error(ctx, "formatting var type", err)
				}
				return
			}
			if sig.Variadic() && i == sig.Params().Len()-1 {
				typeStr = strings.Replace(typeStr, "[]", "...", 1)
			}

			if tp, _ := p.Type().(*typeparams.TypeParam); tp != nil && !c.typeParamInScope(tp) {
				snip.WritePlaceholder(func(snip *snippet.Builder) {
					snip.WriteText(typeStr)
				})
			} else {
				snip.WriteText(typeStr)
			}
		}
	}
	snip.WriteText(")")

	results := sig.Results()
	if results.Len() > 0 {
		snip.WriteText(" ")
	}

	resultsNeedParens := results.Len() > 1 ||
		results.Len() == 1 && results.At(0).Name() != ""

	var resultHasTypeParams bool
	for i := 0; i < results.Len(); i++ {
		if tp, _ := results.At(i).Type().(*typeparams.TypeParam); tp != nil && !c.typeParamInScope(tp) {
			resultHasTypeParams = true
		}
	}

	if resultsNeedParens {
		snip.WriteText("(")
	}
	for i := 0; i < results.Len(); i++ {
		if resultHasTypeParams && !c.opts.placeholders {
			// Leave an empty tabstop if placeholders are disabled and there
			// are type args that need specificying.
			snip.WritePlaceholder(nil)
			break
		}

		if i > 0 {
			snip.WriteText(", ")
		}
		r := results.At(i)
		if name := r.Name(); name != "" {
			snip.WriteText(name + " ")
		}

		text, err := source.FormatVarType(ctx, c.snapshot, c.pkg, r, c.qf, c.mq)
		if err != nil {
			// In general, the only error we should encounter while formatting is
			// context cancellation.
			if ctx.Err() == nil {
				event.Error(ctx, "formatting var type", err)
			}
			return
		}
		if tp, _ := r.Type().(*typeparams.TypeParam); tp != nil && !c.typeParamInScope(tp) {
			snip.WritePlaceholder(func(snip *snippet.Builder) {
				snip.WriteText(text)
			})
		} else {
			snip.WriteText(text)
		}
	}
	if resultsNeedParens {
		snip.WriteText(")")
	}

	snip.WriteText(" {")
	snip.WriteFinalTabstop()
	snip.WriteText("}")

	c.items = append(c.items, CompletionItem{
		Label:   "func(...) {}",
		Score:   matchScore * literalCandidateScore,
		Kind:    protocol.VariableCompletion,
		snippet: snip,
	})
}

// compositeLiteral adds a composite literal completion item for the given typeName.
func (c *gopCompleter) compositeLiteral(T types.Type, snip *snippet.Builder, typeName string, matchScore float64, edits []protocol.TextEdit) {
	snip.WriteText("{")
	// Don't put the tab stop inside the composite literal curlies "{}"
	// for structs that have no accessible fields.
	if strct, ok := T.(*types.Struct); !ok || fieldsAccessible(strct, c.pkg.GetTypes()) {
		snip.WriteFinalTabstop()
	}
	snip.WriteText("}")

	nonSnippet := typeName + "{}"

	c.items = append(c.items, CompletionItem{
		Label:               nonSnippet,
		InsertText:          nonSnippet,
		Score:               matchScore * literalCandidateScore,
		Kind:                protocol.VariableCompletion,
		AdditionalTextEdits: edits,
		snippet:             snip,
	})
}

// basicLiteral adds a literal completion item for the given basic
// type name typeName.
func (c *gopCompleter) basicLiteral(T types.Type, snip *snippet.Builder, typeName string, matchScore float64, edits []protocol.TextEdit) {
	// Never give type conversions like "untyped int()".
	if isUntyped(T) {
		return
	}

	snip.WriteText("(")
	snip.WriteFinalTabstop()
	snip.WriteText(")")

	nonSnippet := typeName + "()"

	c.items = append(c.items, CompletionItem{
		Label:               nonSnippet,
		InsertText:          nonSnippet,
		Detail:              T.String(),
		Score:               matchScore * literalCandidateScore,
		Kind:                protocol.VariableCompletion,
		AdditionalTextEdits: edits,
		snippet:             snip,
	})
}

// makeCall adds a completion item for a "make()" call given a specific type.
func (c *gopCompleter) makeCall(snip *snippet.Builder, typeName string, secondArg string, matchScore float64, edits []protocol.TextEdit) {
	// Keep it simple and don't add any placeholders for optional "make()" arguments.

	snip.PrependText("make(")
	if secondArg != "" {
		snip.WriteText(", ")
		snip.WritePlaceholder(func(b *snippet.Builder) {
			if c.opts.placeholders {
				b.WriteText(secondArg)
			}
		})
	}
	snip.WriteText(")")

	var nonSnippet strings.Builder
	nonSnippet.WriteString("make(" + typeName)
	if secondArg != "" {
		nonSnippet.WriteString(", ")
		nonSnippet.WriteString(secondArg)
	}
	nonSnippet.WriteByte(')')

	c.items = append(c.items, CompletionItem{
		Label:               nonSnippet.String(),
		InsertText:          nonSnippet.String(),
		Score:               matchScore * literalCandidateScore,
		Kind:                protocol.FunctionCompletion,
		AdditionalTextEdits: edits,
		snippet:             snip,
	})
}

// Create a snippet for a type name where type params become placeholders.
func (c *gopCompleter) typeNameSnippet(literalType types.Type, qf types.Qualifier) (*snippet.Builder, string) {
	var (
		snip     snippet.Builder
		typeName string
		named, _ = literalType.(*types.Named)
	)

	if named != nil && named.Obj() != nil && typeparams.ForNamed(named).Len() > 0 && !c.fullyInstantiated(named) {
		// We are not "fully instantiated" meaning we have type params that must be specified.
		if pkg := qf(named.Obj().Pkg()); pkg != "" {
			typeName = pkg + "."
		}

		// We do this to get "someType" instead of "someType[T]".
		typeName += named.Obj().Name()
		snip.WriteText(typeName + "[")

		if c.opts.placeholders {
			for i := 0; i < typeparams.ForNamed(named).Len(); i++ {
				if i > 0 {
					snip.WriteText(", ")
				}
				snip.WritePlaceholder(func(snip *snippet.Builder) {
					snip.WriteText(types.TypeString(typeparams.ForNamed(named).At(i), qf))
				})
			}
		} else {
			snip.WritePlaceholder(nil)
		}
		snip.WriteText("]")
		typeName += "[...]"
	} else {
		// We don't have unspecified type params so use default type formatting.
		typeName = types.TypeString(literalType, qf)
		snip.WriteText(typeName)
	}

	return &snip, typeName
}

// fullyInstantiated reports whether all of t's type params have
// specified type args.
func (c *gopCompleter) fullyInstantiated(t *types.Named) bool {
	tps := typeparams.ForNamed(t)
	tas := typeparams.NamedTypeArgs(t)

	if tps.Len() != tas.Len() {
		return false
	}

	for i := 0; i < tas.Len(); i++ {
		switch ta := tas.At(i).(type) {
		case *typeparams.TypeParam:
			// A *TypeParam only counts as specified if it is currently in
			// scope (i.e. we are in a generic definition).
			if !c.typeParamInScope(ta) {
				return false
			}
		case *types.Named:
			if !c.fullyInstantiated(ta) {
				return false
			}
		}
	}
	return true
}

// typeParamInScope returns whether tp's object is in scope at c.pos.
// This tells you whether you are in a generic definition and can
// assume tp has been specified.
func (c *gopCompleter) typeParamInScope(tp *typeparams.TypeParam) bool {
	obj := tp.Obj()
	if obj == nil {
		return false
	}

	scope := c.innermostScope()
	if scope == nil {
		return false
	}

	_, foundObj := scope.LookupParent(obj.Name(), c.pos)
	return obj == foundObj
}