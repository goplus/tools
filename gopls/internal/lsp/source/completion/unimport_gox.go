package completion

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	goplsastutil "golang.org/x/tools/gopls/internal/astutil"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/snippet"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/imports"
	"golang.org/x/tools/internal/typeparams"
)

type recheckItem struct {
	CompletionItem
	noSnip bool
}

type recheckOverload struct {
	pkgs  map[source.PackagePath]bool          // gop package
	items map[source.PackagePath][]recheckItem // index overload funcs
	gopo  map[source.PackagePath][]recheckItem // gopo overload funcs
}

func newRecheckOverload() *recheckOverload {
	return &recheckOverload{
		make(map[source.PackagePath]bool),
		make(map[source.PackagePath][]recheckItem),
		make(map[source.PackagePath][]recheckItem),
	}
}

func (recheck *recheckOverload) checkOverload(c *gopCompleter) {
	// check gop package index overload
	for pkg, items := range recheck.items {
		if recheck.pkgs[pkg] {
			names := make(map[string]bool)
			sort.Slice(items, func(i, j int) bool {
				return items[i].Label < items[j].Label
			})
			for _, item := range items {
				id := item.Label[:len(item.Label)-3]
				if !names[id] {
					names[id] = true
					item.isOverload = true
					item.Detail = "Go+ overload func\n\n" + item.Detail
					c.items = append(c.items, cloneAliasItem(item.CompletionItem, item.Label, id, 0, false))
					if alias, ok := hasAliasName(id); ok {
						c.items = append(c.items, cloneAliasItem(item.CompletionItem, item.Label, alias, 0.0001, item.noSnip))
					}
				}
			}
		} else {
			for _, item := range items {
				c.items = append(c.items, item.CompletionItem)
				if alias, ok := hasAliasName(item.Label); ok {
					c.items = append(c.items, cloneAliasItem(item.CompletionItem, item.Label, alias, 0.0001, item.noSnip))
				}
			}
		}
	}
	// check gop package gopo overload
	for pkg, items := range recheck.gopo {
		if recheck.pkgs[pkg] {
			for _, item := range items {
				item.isOverload = true
				item.Detail = "Go+ overload func\n\n" + item.Detail
				c.items = append(c.items, item.CompletionItem)
				if alias, ok := hasAliasName(item.Label); ok {
					c.items = append(c.items, cloneAliasItem(item.CompletionItem, item.Label, alias, 0.0001, item.noSnip))
				}
			}
		}
	}
}

// goxls: quickParse
func (c *gopCompleter) quickParse(ctx context.Context, cMu *sync.Mutex, enough *int32, selName string, relevances map[string]float64, needImport bool, recheck *recheckOverload) func(uri span.URI, m *source.Metadata) error {
	return func(uri span.URI, m *source.Metadata) error {
		if atomic.LoadInt32(enough) != 0 {
			return nil
		}

		fh, err := c.snapshot.ReadFile(ctx, uri)
		if err != nil {
			return err
		}
		content, err := fh.Content()
		if err != nil {
			return err
		}
		path := string(m.PkgPath)
		goForEachPackageMember(content, func(tok token.Token, id *ast.Ident, fn *ast.FuncDecl, gopo bool) {
			if atomic.LoadInt32(enough) != 0 {
				return
			}

			if !id.IsExported() {
				return
			}

			cMu.Lock()
			score := c.matcher.Score(id.Name)
			cMu.Unlock()

			if selName != "_" && score == 0 {
				return // not a match; avoid constructing the completion item below
			}

			// The only detail is the kind and package: `var (from "example.com/foo")`
			// TODO(adonovan): pretty-print FuncDecl.FuncType or TypeSpec.Type?
			// TODO(adonovan): should this score consider the actual c.matcher.Score
			// of the item? How does this compare with the deepState.enqueue path?
			item := CompletionItem{
				Label:      id.Name,
				Detail:     fmt.Sprintf("%s (from %q)", strings.ToLower(tok.String()), m.PkgPath),
				InsertText: id.Name,
				Score:      unimportedScore(relevances[path]),
			}
			switch tok {
			case token.FUNC:
				item.Kind = protocol.FunctionCompletion
			case token.VAR:
				item.Kind = protocol.VariableCompletion
			case token.CONST:
				item.Kind = protocol.ConstantCompletion
			case token.TYPE:
				// Without types, we can't distinguish Class from Interface.
				item.Kind = protocol.ClassCompletion
			}

			if needImport {
				imp := &importInfo{importPath: path}
				if imports.ImportPathToAssumedName(path) != string(m.Name) {
					imp.name = string(m.Name)
				}
				item.AdditionalTextEdits, _ = c.importEdits(imp)
			}

			// For functions, add a parameter snippet.
			if fn != nil {
				var sn snippet.Builder
				sn.WriteText(id.Name)

				paramList := func(open, close string, list *ast.FieldList) {
					if list != nil {
						var cfg printer.Config // slight overkill
						var nparams int
						param := func(name string, typ ast.Expr) {
							if nparams > 0 {
								sn.WriteText(", ")
							}
							nparams++
							if c.opts.placeholders {
								sn.WritePlaceholder(func(b *snippet.Builder) {
									var buf strings.Builder
									buf.WriteString(name)
									buf.WriteByte(' ')
									cfg.Fprint(&buf, token.NewFileSet(), typ)
									b.WriteText(buf.String())
								})
							} else {
								sn.WriteText(name)
							}
						}

						sn.WriteText(open)
						for _, field := range list.List {
							if field.Names != nil {
								for _, name := range field.Names {
									param(name.Name, field.Type)
								}
							} else {
								param("_", field.Type)
							}
						}
						sn.WriteText(close)
					}
				}

				paramList("[", "]", typeparams.ForFuncType(fn.Type))
				paramList("(", ")", fn.Type.Params)

				item.snippet = &sn
			}

			cMu.Lock()
			if tok == token.CONST {
				if id.Name == "GopPackage" {
					recheck.pkgs[m.PkgPath] = true
				}
			}
			if tok == token.FUNC {
				noSnip := len(fn.Type.Params.List) == 0
				if maybleIndexOverload(id.Name) {
					recheck.items[m.PkgPath] = append(recheck.items[m.PkgPath], recheckItem{item, noSnip})
				} else if gopo {
					recheck.gopo[m.PkgPath] = append(recheck.gopo[m.PkgPath], recheckItem{item, noSnip})
				} else {
					c.items = append(c.items, item)
					// goxls func alias
					if alias, ok := hasAliasName(id.Name); ok {
						c.items = append(c.items, cloneAliasItem(item, id.Name, alias, 0.0001, noSnip))
					}
				}
			} else {
				c.items = append(c.items, item)
			}
			if len(c.items) >= unimportedMemberTarget {
				atomic.StoreInt32(enough, 1)
			}
			cMu.Unlock()
		})
		return nil
	}
}

// goForEachPackageMember calls f(tok, id, fnType, gopo) for each package-level
// TYPE/VAR/CONST/FUNC declaration in the Go source file, based on a
// quick partial parse. fn is non-nil only for function declarations.
// The AST position information is garbage.
func goForEachPackageMember(content []byte, f func(tok token.Token, id *ast.Ident, fn *ast.FuncDecl, gopo bool)) {
	purged := goplsastutil.PurgeFuncBodies(content)
	file, _ := parser.ParseFile(token.NewFileSet(), "", purged, 0)
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.ValueSpec: // var/const
					for _, id := range spec.Names {
						if decl.Tok == token.CONST && strings.HasPrefix(id.Name, "Gopo_") {
							if md := checkTypeMethod(id.Name[5:]); md.typ == "" && ast.IsExported(md.name) {
								if fn := foundGopoExportOverload(file, spec); fn != nil {
									f(token.FUNC, ast.NewIdent(md.name), fn, true)
								}
							}
						}
						f(decl.Tok, id, nil, false)
					}
				case *ast.TypeSpec:
					f(decl.Tok, spec.Name, nil, false)
				}
			}
		case *ast.FuncDecl:
			if decl.Recv == nil {
				f(token.FUNC, decl.Name, decl, false)
			}
		}
	}
}

func foundGopoExportOverload(f *ast.File, spec *ast.ValueSpec) (fn *ast.FuncDecl) {
	if lit, ok := spec.Values[0].(*ast.BasicLit); ok {
		if s, err := strconv.Unquote(lit.Value); err == nil {
			for _, v := range strings.Split(s, ",") {
				if obj := f.Scope.Lookup(v); obj != nil && ast.IsExported(obj.Name) {
					if fn, ok := obj.Decl.(*ast.FuncDecl); ok {
						return fn
					}
				}
			}
		}
	}
	return nil
}

type mthd struct {
	typ  string
	name string
}

// Func (no _ func name)
// _Func (with _ func name)
// TypeName_Method (no _ method name)
// _TypeName__Method (with _ method name)
func checkTypeMethod(name string) mthd {
	if pos := strings.IndexByte(name, '_'); pos >= 0 {
		if pos == 0 {
			t := name[1:]
			if pos = strings.Index(t, "__"); pos <= 0 {
				return mthd{"", t} // _Func
			}
			return mthd{t[:pos], t[pos+2:]} // _TypeName__Method
		}
		return mthd{name[:pos], name[pos+1:]} // TypeName_Method
	}
	return mthd{"", name} // Func
}
