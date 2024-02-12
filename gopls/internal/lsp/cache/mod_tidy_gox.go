package cache

import (
	"context"
	"strconv"
	"strings"

	"github.com/goplus/gop/token"
	"github.com/goplus/mod/gopmod"
	"golang.org/x/tools/gopls/internal/goxls/parserutil"
	"golang.org/x/tools/gopls/internal/lsp/source"
)

func parseGopImports(ctx context.Context, mod *gopmod.Module, s *snapshot, files []source.FileHandle, seen map[string]bool) error {
	pgfs, err := s.view.parseCache.parseGopFiles(ctx, mod, token.NewFileSet(), parserutil.ParseHeader, false, files...)
	if err != nil { // e.g. context cancellation
		return err
	}

	for _, pgf := range pgfs {
		for _, spec := range pgf.File.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			seen[path] = true
		}
		if strings.HasSuffix(pgf.URI.Filename(), "_test.gox") {
			seen["github.com/goplus/gop/test"] = true
			seen["testing"] = true
		}
	}
	return nil
}
