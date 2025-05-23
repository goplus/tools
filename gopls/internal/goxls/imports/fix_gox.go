package imports

import (
	"strings"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/token"
)

func findGopPackage(decls []ast.Decl) bool {
	for _, decl := range decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			if decl.Tok == token.CONST {
				for _, sepc := range decl.Specs {
					if vs, ok := sepc.(*ast.ValueSpec); ok {
						for _, name := range vs.Names {
							if name.Name == "GopPackage" {
								return true
							}
						}
					}
				}
			}
		case *ast.OverloadFuncDecl:
			return true
		}
	}
	return false
}

// gopExports export Go+ style func, startLower and overload (GopPackage)
func gopExports(decls []ast.Decl, gopPackage bool) (exports []string) {
	for _, decl := range decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if ast.IsExported(spec.Name.Name) {
						exports = append(exports, spec.Name.Name)
					}
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						if ast.IsExported(name.Name) {
							exports = append(exports, name.Name)
						}
					}
				}
			}
		case *ast.FuncDecl:
			if decl.Recv != nil || decl.IsClass || decl.Operator || decl.Static ||
				!ast.IsExported(decl.Name.Name) {
				continue
			}
			name := decl.Name.Name
			exports = append(exports, name)
			if v, ok := toStartWithLowerCase(name); ok {
				exports = append(exports, v)
			}
			if gopPackage && strings.HasSuffix(name, "__0") {
				name = name[:len(name)-3]
				exports = append(exports, name)
				if v, ok := toStartWithLowerCase(name); ok {
					exports = append(exports, v)
				}
			}
		case *ast.OverloadFuncDecl:
			if decl.Recv != nil || decl.IsClass || decl.Operator ||
				!ast.IsExported(decl.Name.Name) {
				continue
			}
			name := decl.Name.Name
			exports = append(exports, name)
			if v, ok := toStartWithLowerCase(name); ok {
				exports = append(exports, v)
			}
		}
	}
	return exports
}

func toStartWithLowerCase(name string) (string, bool) {
	if c := name[0]; c >= 'A' && c <= 'Z' {
		return string(c+('a'-'A')) + name[1:], true
	}
	return name, false
}
