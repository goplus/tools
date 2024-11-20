package completion

import (
	"fmt"
	"testing"

	"github.com/goplus/gop/ast"
	"github.com/goplus/gop/parser"
	"github.com/goplus/gop/token"
	"golang.org/x/tools/gop/ast/astutil"
)

func TestAllowCommand(t *testing.T) {
	testdata := []struct {
		src    string
		pos    int
		result bool
	}{
		{`println`, 7, true},
		{`a := add`, 8, false},
		{`onStart => { println }`, 20, true},
		{`println info`, 12, false},
		{`fmt.println`, 11, true},
		{`a := pkg.info.get`, 17, false},
		{`pkg.info.get`, 12, true},
		{`run "get /p/$id", => {
	get "http://foo.com/p/${id}"
	ret 200
}`, 57, true},
		{`run "get /p/$id", => {
	get "http://foo.com/p/${id}"
	ret 200
	json {
		"id": getId,
	}
}`, 83, false},
	}
	for i, data := range testdata {
		r, err := testCheckAllowCommand(data.src, data.pos)
		if err != nil {
			t.Fatalf("check index %v error: %v", i, err)
		}
		if r != data.result {
			t.Fatalf("check index %v result failed. got %v want %v", i, r, data.result)
		}
	}
}

func testCheckAllowCommand(src string, pos int) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.gop", src, 0)
	if err != nil {
		return false, err
	}
	paths, _ := astutil.PathEnclosingInterval(f, token.Pos(pos), token.Pos(pos))
	switch node := paths[0].(type) {
	case *ast.Ident, *ast.SelectorExpr:
	default:
		return false, fmt.Errorf("not found ident or selector expr, got %T", node)
	}
	return checkAllowCommand(paths), nil
}
