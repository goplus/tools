// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"bytes"
	"container/heap"
	"context"
	"fmt"
	"math/bits"
	"runtime"
	"strings"
	"time"

	goparser "go/parser"

	"github.com/goplus/gop/parser"
	"github.com/goplus/gop/token"
	"github.com/goplus/mod/gopmod"
	"github.com/qiniu/x/log"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/gopls/internal/goxls"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/internal/memoize"
	"golang.org/x/tools/internal/tokeninternal"
)

// startParseGop prepares a parsing pass, creating new promises in the cache for
// any cache misses.
//
// The resulting slice has an entry for every given file handle, though some
// entries may be nil if there was an error reading the file (in which case the
// resulting error will be non-nil).
func (c *parseCache) startParseGop(mod *gopmod.Module, mode parser.Mode, purgeFuncBodies bool, fhs ...source.FileHandle) ([]*memoize.Promise, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Any parsing pass increments the clock, as we'll update access times.
	// (technically, if fhs is empty this isn't necessary, but that's a degenerate case).
	//
	// All entries parsed from a single call get the same access time.
	c.clock++
	walltime := time.Now()

	// Read file data and collect cacheable files.
	var (
		data           = make([][]byte, len(fhs)) // file content for each readable file
		promises       = make([]*memoize.Promise, len(fhs))
		firstReadError error // first error from fh.Read, or nil
	)
	for i, fh := range fhs {
		content, err := fh.Content()
		if err != nil {
			if firstReadError == nil {
				firstReadError = err
			}
			continue
		}
		data[i] = content

		key := parseKey{
			uri:             fh.URI(),
			mode:            goparser.Mode(mode),
			purgeFuncBodies: purgeFuncBodies,
			gopFile:         true,
		}

		if e, ok := c.m[key]; ok {
			if e.hash == fh.FileIdentity().Hash { // cache hit
				e.atime = c.clock
				e.walltime = walltime
				heap.Fix(&c.lru, e.lruIndex)
				promises[i] = e.promise
				continue
			} else {
				// A cache hit, for a different version. Delete it.
				delete(c.m, e.key)
				heap.Remove(&c.lru, e.lruIndex)
			}
		}

		uri := fh.URI()
		// goxls: misuse of parseCache.startParseGop
		if goxls.DbgMisuse && strings.HasSuffix(uri.Filename(), ".go") {
			log.Println("misuse: use parseCache.startParseGop to parse a Go file")
			log.SingleStack()
		}
		promise := memoize.NewPromise("parseCache.parseGop", func(ctx context.Context, _ interface{}) interface{} {
			// Allocate 2*len(content)+parsePadding to allow for re-parsing once
			// inside of parseGoSrc without exceeding the allocated space.
			base, nextBase := c.allocateSpace(2*len(content) + parsePadding)

			pgf, fixes1 := ParseGopSrc(ctx, mod, fileSetWithBase(base), uri, content, mode, purgeFuncBodies)
			file := pgf.Tok
			if file.Base()+file.Size()+1 > nextBase {
				// The parsed file exceeds its allocated space, likely due to multiple
				// passes of src fixing. In this case, we have no choice but to re-do
				// the operation with the correct size.
				//
				// Even though the final successful parse requires only file.Size()
				// bytes of Pos space, we need to accommodate all the missteps to get
				// there, as parseGoSrc will repeat them.
				actual := file.Base() + file.Size() - base // actual size consumed, after re-parsing
				base2, nextBase2 := c.allocateSpace(actual)
				pgf2, fixes2 := ParseGopSrc(ctx, mod, fileSetWithBase(base2), uri, content, mode, purgeFuncBodies)

				// In golang/go#59097 we observed that this panic condition was hit.
				// One bug was found and fixed, but record more information here in
				// case there is still a bug here.
				if end := pgf2.Tok.Base() + pgf2.Tok.Size(); end != nextBase2-1 {
					var errBuf bytes.Buffer
					fmt.Fprintf(&errBuf, "internal error: non-deterministic parsing result:\n")
					fmt.Fprintf(&errBuf, "\t%q (%d-%d) does not span %d-%d\n", uri, pgf2.Tok.Base(), base2, end, nextBase2-1)
					fmt.Fprintf(&errBuf, "\tfirst %q (%d-%d)\n", pgf.URI, pgf.Tok.Base(), pgf.Tok.Base()+pgf.Tok.Size())
					fmt.Fprintf(&errBuf, "\tfirst space: (%d-%d), second space: (%d-%d)\n", base, nextBase, base2, nextBase2)
					fmt.Fprintf(&errBuf, "\tfirst mode: %v, second mode: %v", pgf.Mode, pgf2.Mode)
					fmt.Fprintf(&errBuf, "\tfirst err: %v, second err: %v", pgf.ParseErr, pgf2.ParseErr)
					fmt.Fprintf(&errBuf, "\tfirst fixes: %v, second fixes: %v", fixes1, fixes2)
					panic(errBuf.String())
				}
				pgf = pgf2
			}
			return pgf
		})
		promises[i] = promise

		// add new entry; entries are gc'ed asynchronously
		e := &parseCacheEntry{
			key:      key,
			hash:     fh.FileIdentity().Hash,
			promise:  promise,
			atime:    c.clock,
			walltime: walltime,
		}
		c.m[e.key] = e
		heap.Push(&c.lru, e)
	}

	if len(c.m) != len(c.lru) {
		panic("map and LRU are inconsistent")
	}

	return promises, firstReadError
}

// parseGopFiles returns a ParsedGopFile for each file handle in fhs, in the
// requested parse mode.
//
// For parsed files that already exists in the cache, access time will be
// updated. For others, parseGopFiles will parse and store as many results in the
// cache as space allows.
//
// The token.File for each resulting parsed file will be added to the provided
// FileSet, using the tokeninternal.AddExistingFiles API. Consequently, the
// given fset should only be used in other APIs if its base is >=
// reservedForParsing.
//
// If parseGopFiles returns an error, it still returns a slice,
// but with a nil entry for each file that could not be parsed.
func (c *parseCache) parseGopFiles(ctx context.Context, mod *gopmod.Module, fset *token.FileSet, mode parser.Mode, purgeFuncBodies bool, fhs ...source.FileHandle) ([]*source.ParsedGopFile, error) {
	pgfs := make([]*source.ParsedGopFile, len(fhs))

	// Temporary fall-back for 32-bit systems, where reservedForParsing is too
	// small to be viable. We don't actually support 32-bit systems, so this
	// workaround is only for tests and can be removed when we stop running
	// 32-bit TryBots for gopls.
	if bits.UintSize == 32 {
		for i, fh := range fhs {
			var err error
			pgfs[i], err = parseGopImpl(ctx, mod, fset, fh, mode, purgeFuncBodies)
			if err != nil {
				return pgfs, err
			}
		}
		return pgfs, nil
	}

	promises, firstErr := c.startParseGop(mod, mode, purgeFuncBodies, fhs...)

	// Await all parsing.
	var g errgroup.Group
	g.SetLimit(runtime.GOMAXPROCS(-1)) // parsing is CPU-bound.
	for i, promise := range promises {
		if promise == nil {
			continue
		}
		i := i
		promise := promise
		g.Go(func() error {
			result, err := promise.Get(ctx, nil)
			if err != nil {
				return err
			}
			pgfs[i] = result.(*source.ParsedGopFile)
			return nil
		})
	}

	if err := g.Wait(); err != nil && firstErr == nil {
		firstErr = err
	}

	// Augment the FileSet to map all parsed files.
	var tokenFiles []*token.File
	for _, pgf := range pgfs {
		if pgf == nil {
			continue
		}
		tokenFiles = append(tokenFiles, pgf.Tok)
	}
	tokeninternal.AddExistingFiles(fset, tokenFiles)

	return pgfs, firstErr
}
