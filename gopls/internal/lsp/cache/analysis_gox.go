// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/gopls/internal/goxls/parserutil"
	"golang.org/x/tools/gopls/internal/lsp/source"
)

func gopParsed(an *analysisNode, ctx context.Context) ([]*source.ParsedGopFile, error) {
	if len(an.gopFiles) == 0 {
		return nil, nil
	}
	// Parse only the "compiled" Go+ files.
	// Do the computation in parallel.
	parsed := make([]*source.ParsedGopFile, len(an.gopFiles))
	{
		var group errgroup.Group
		group.SetLimit(4) // not too much: run itself is already called in parallel
		for i, fh := range an.gopFiles {
			i, fh := i, fh
			group.Go(func() error {
				// Call parseGopImpl directly, not the caching wrapper,
				// as cached ASTs require the global FileSet.
				// ast.Object resolution is unfortunately an implied part of the
				// go/analysis contract.
				pgf, err := parseGopImpl(ctx, an.m.GopMod_(), an.fset, fh, parserutil.ParseFull&^parserutil.SkipObjectResolution, false)
				parsed[i] = pgf
				return err
			})
		}
		if err := group.Wait(); err != nil {
			return nil, err // cancelled, or catastrophic error (e.g. missing file)
		}
	}
	return parsed, nil
}
