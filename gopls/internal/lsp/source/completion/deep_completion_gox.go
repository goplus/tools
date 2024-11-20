// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"context"
	"go/types"
	"strings"
	"time"

	"github.com/qiniu/x/log"
	"golang.org/x/tools/gopls/internal/goxls"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/snippet"
)

const (
	showGopStyle bool = true
)

const (
	CompletionItemTagAlias    protocol.CompletionItemTag = 2
	CompletionItemTagOverload protocol.CompletionItemTag = 3
)

// deepSearch searches a candidate and its subordinate objects for completion
// items if deep completion is enabled and adds the valid candidates to
// completion items.
func (c *gopCompleter) deepSearch(ctx context.Context, start time.Time, deadline *time.Time) {
	defer func() {
		// We can return early before completing the search, so be sure to
		// clear out our queues to not impact any further invocations.
		c.deepState.thisQueue = c.deepState.thisQueue[:0]
		c.deepState.nextQueue = c.deepState.nextQueue[:0]
	}()
	if goxls.DbgCompletion {
		log.Println("gopCompleter.deepSearch: n =", len(c.deepState.nextQueue))
	}

	first := true // always fully process the first set of candidates
	for len(c.deepState.nextQueue) > 0 && (first || deadline == nil || time.Now().Before(*deadline)) {
		first = false
		c.deepState.thisQueue, c.deepState.nextQueue = c.deepState.nextQueue, c.deepState.thisQueue[:0]

	outer:
		for _, cand := range c.deepState.thisQueue {
			obj := cand.obj
			if obj == nil {
				continue
			}

			// At the top level, dedupe by object.
			if len(cand.path) == 0 {
				if c.seen[obj] {
					continue
				}
				c.seen[obj] = true
			}

			// If obj is not accessible because it lives in another package and is
			// not exported, don't treat it as a completion candidate unless it's
			// a package completion candidate.
			if !c.completionContext.packageCompletion &&
				obj.Pkg() != nil && obj.Pkg() != c.pkg.GetTypes() && !obj.Exported() {
				continue
			}

			// If we want a type name, don't offer non-type name candidates.
			// However, do offer package names since they can contain type names,
			// and do offer any candidate without a type since we aren't sure if it
			// is a type name or not (i.e. unimported candidate).
			if c.wantTypeName() && obj.Type() != nil && !isTypeName(obj) && !isPkgName(obj) {
				continue
			}

			// When searching deep, make sure we don't have a cycle in our chain.
			// We don't dedupe by object because we want to allow both "foo.Baz"
			// and "bar.Baz" even though "Baz" is represented the same types.Object
			// in both.
			for _, seenObj := range cand.path {
				if seenObj == obj {
					continue outer
				}
			}

			// skip index overload func
			if fn, ok := obj.(*types.Func); ok && c.skipFunc[funcId(fn)] {
				continue
			}

			c.addCandidate(ctx, &cand)

			c.deepState.candidateCount++
			if c.opts.budget > 0 && c.deepState.candidateCount%100 == 0 {
				spent := float64(time.Since(start)) / float64(c.opts.budget)
				select {
				case <-ctx.Done():
					return
				default:
					// If we are almost out of budgeted time, no further elements
					// should be added to the queue. This ensures remaining time is
					// used for processing current queue.
					if !c.deepState.queueClosed && spent >= 0.85 {
						c.deepState.queueClosed = true
					}
				}
			}

			// if deep search is disabled, don't add any more candidates.
			if !c.deepState.enabled || c.deepState.queueClosed {
				continue
			}

			// Searching members for a type name doesn't make sense.
			if isTypeName(obj) {
				continue
			}
			if obj.Type() == nil {
				continue
			}

			// Don't search embedded fields because they were already included in their
			// parent's fields.
			if v, ok := obj.(*types.Var); ok && v.Embedded() {
				continue
			}

			if sig, ok := obj.Type().Underlying().(*types.Signature); ok {
				// If obj is a function that takes no arguments and returns one
				// value, keep searching across the function call.
				if sig.Params().Len() == 0 && sig.Results().Len() == 1 {
					path := c.deepState.newPath(cand, obj)
					// The result of a function call is not addressable.
					c.methodsAndFields(sig.Results().At(0).Type(), false, cand.imp, func(newCand candidate) {
						newCand.pathInvokeMask = cand.pathInvokeMask | (1 << uint64(len(cand.path)))
						newCand.path = path
						c.deepState.enqueue(newCand)
					})
				}
			}

			path := c.deepState.newPath(cand, obj)
			switch obj := obj.(type) {
			case *types.PkgName:
				c.packageMembers(obj.Imported(), stdScore, cand.imp, func(newCand candidate) {
					newCand.pathInvokeMask = cand.pathInvokeMask
					newCand.path = path
					c.deepState.enqueue(newCand)
				})
			default:
				c.methodsAndFields(obj.Type(), cand.addressable, cand.imp, func(newCand candidate) {
					newCand.pathInvokeMask = cand.pathInvokeMask
					newCand.path = path
					c.deepState.enqueue(newCand)
				})
			}
		}
	}
}

// addCandidate adds a completion candidate to suggestions, without searching
// its members for more candidates.
func (c *gopCompleter) addCandidate(ctx context.Context, cand *candidate) {
	obj := cand.obj
	if c.matchingCandidate(cand) {
		cand.score *= highScore

		if p := c.penalty(cand); p > 0 {
			cand.score *= (1 - p)
		}
	} else if isTypeName(obj) {
		// If obj is a *types.TypeName that didn't otherwise match, check
		// if a literal object of this type makes a good candidate.

		// We only care about named types (i.e. don't want builtin types).
		if _, isNamed := obj.Type().(*types.Named); isNamed {
			c.literal(ctx, obj.Type(), cand.imp)
		}
	}

	// Lower score of method calls so we prefer fields and vars over calls.
	var aliasNoSnip bool
	if cand.hasMod(invoke) {
		if sig, ok := obj.Type().Underlying().(*types.Signature); ok {
			if sig.Params() == nil || (sig.Recv() != nil && sig.Variadic() && sig.Params().Len() == 1) {
				aliasNoSnip = true
			}
			if sig.Recv() != nil {
				cand.score *= 0.9
			}
		}
	}

	// Prefer private objects over public ones.
	if !obj.Exported() && obj.Parent() != types.Universe {
		cand.score *= 1.1
	}

	// Slight penalty for index modifier (e.g. changing "foo" to
	// "foo[]") to curb false positives.
	if cand.hasMod(index) {
		cand.score *= 0.9
	}

	// Favor shallow matches by lowering score according to depth.
	cand.score -= cand.score * c.deepState.scorePenalty(cand)

	if cand.score < 0 {
		cand.score = 0
	}

	var aliasName string
	cand.name, aliasName = gopDeepCandName(cand, c.pkg.GetTypes())
	if item, err := c.item(ctx, *cand); err == nil {
		c.items = append(c.items, item)
		if aliasName != cand.name {
			c.items = append(c.items, cloneAliasItem(item, cand.name, aliasName, 0.0001, aliasNoSnip))
		}
	} else if false && goxls.DbgCompletion {
		log.Println("gopCompleter.addCandidate item:", err)
		log.SingleStack()
	}
}

func cloneAliasItem(item CompletionItem, name string, alias string, score float64, noSnip bool) CompletionItem {
	aliasItem := item
	if showGopStyle {
		item.Tags = append(item.Tags, CompletionItemTagAlias)
		if !item.isOverload {
			aliasItem.Detail = "Go+ alias func\n\n" + aliasItem.Detail
		}
	}
	aliasItem.Label = alias
	aliasItem.InsertText = alias
	if noSnip {
		aliasItem.snippet = nil
	} else {
		var snip snippet.Builder
		snip.Write([]byte(strings.Replace(item.snippet.String(), name, alias, 1)))
		aliasItem.snippet = &snip
	}
	aliasItem.Score += score
	aliasItem.isAlias = true
	return aliasItem
}

// deepCandName produces the full candidate name including any
// ancestor objects. For example, "foo.bar().baz" for candidate "baz".
func gopDeepCandName(cand *candidate, this *types.Package) (name string, alias string) {
	totalLen := len(cand.obj.Name())
	totalLen2 := totalLen
	for i, obj := range cand.path {
		n := len(obj.Name()) + 1
		totalLen += n
		totalLen2 += n
		if cand.pathInvokeMask&(1<<uint16(i)) > 0 {
			totalLen += 2
			if !isFunc(obj) {
				totalLen2 += 2
			}
		}
	}

	var buf strings.Builder
	buf.Grow(totalLen)
	var buf2 strings.Builder
	buf.Grow(totalLen2)

	for i, obj := range cand.path {
		buf.WriteString(obj.Name())
		name, ok := hasGopStyleName(obj, this, nil)
		buf2.WriteString(name)
		if cand.pathInvokeMask&(1<<uint16(i)) > 0 {
			buf.WriteByte('(')
			buf.WriteByte(')')
			if !ok {
				buf2.WriteByte('(')
				buf2.WriteByte(')')
			}
		}
		buf.WriteByte('.')
		buf2.WriteByte('.')
	}

	buf.WriteString(cand.obj.Name())
	buf2.WriteString(gopStyleName(cand.obj, this, cand.lookup))

	return buf.String(), buf2.String()
}

func hasAliasName(name string) (alias string, ok bool) {
	if c := name[0]; c >= 'A' && c <= 'Z' {
		if len(name) > 1 {
			if c1 := name[1]; c1 >= 'A' && c1 <= 'Z' {
				return
			}
		}
		alias, ok = string(rune(c)-('A'-'a'))+name[1:], true
	}
	return
}

func gopStyleName(obj types.Object, this *types.Package, lookup func(pkg *types.Package, name string) *types.Selection) (name string) {
	name, _ = hasGopStyleName(obj, this, lookup)
	return
}

func hasGopStyleName(obj types.Object, this *types.Package, lookup func(pkg *types.Package, name string) *types.Selection) (name string, ok bool) {
	name = obj.Name()
	if isFunc(obj) {
		if pkg := obj.Pkg(); pkg != nil {
			if pkg != this {
				if alias, ok := hasAliasName(name); ok {
					return alias, true
				}
			} else if lookup != nil {
				if alias, ok := hasAliasName(name); ok {
					if lookup(this, alias) == nil {
						return alias, true
					}
				}
			}
		}
	}
	return
}

// penalty reports a score penalty for cand in the range (0, 1).
// For example, a candidate is penalized if it has already been used
// in another switch case statement.
func (c *gopCompleter) penalty(cand *candidate) float64 {
	for _, p := range c.inference.penalized {
		if c.objChainMatches(cand, p.objChain) {
			return p.penalty
		}
	}

	return 0
}

// objChainMatches reports whether cand combined with the surrounding
// object prefix matches chain.
func (c *gopCompleter) objChainMatches(cand *candidate, chain []types.Object) bool {
	// For example, when completing:
	//
	//   foo.ba<>
	//
	// If we are considering the deep candidate "bar.baz", cand is baz,
	// objChain is [foo] and deepChain is [bar]. We would match the
	// chain [foo, bar, baz].
	if len(chain) != len(c.inference.objChain)+len(cand.path)+1 {
		return false
	}

	if chain[len(chain)-1] != cand.obj {
		return false
	}

	for i, o := range c.inference.objChain {
		if chain[i] != o {
			return false
		}
	}

	for i, o := range cand.path {
		if chain[i+len(c.inference.objChain)] != o {
			return false
		}
	}

	return true
}
