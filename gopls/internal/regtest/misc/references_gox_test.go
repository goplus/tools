package misc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	. "golang.org/x/tools/gopls/internal/lsp/regtest"
)

func TestReferencesOnOverloadMember(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.19
-- def.gop --
func add = (
	func(a, b int) int {
		return a + b
	}
	func(a, b string) string {
		return a + b
	}
)
-- test.gop --
println add(1,2)
println add("Hello", "World")
println add("Bye", "World")
-- gop_autogen.go --
package main

import "fmt"

const _ = true
func add__0(a int, b int) int {
	return a + b
}
func add__1(a string, b string) string {
	return a + b
}
func main() {
	fmt.Println(add__0(1, 2))
	fmt.Println(add__1("Hello", "World"))
	fmt.Println(add__1("Bye", "World"))
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("test.gop")
		loc := env.GoToDefinition(env.RegexpSearch("test.gop", `println (add)\("Hello", "World"\)`))
		refs, err := env.Editor.References(env.Ctx, loc)
		if err != nil {
			t.Fatalf("references on (*s).Error failed: %v", err)
		}
		var buf strings.Builder
		for _, ref := range refs {
			fmt.Fprintf(&buf, "%s %s\n", env.Sandbox.Workdir.URIToPath(ref.URI), ref.Range)
		}
		got := buf.String()
		want := "def.gop 4:1-4:25\n" + // anonymous overload func
			"test.gop 1:8-1:11\n" +
			"test.gop 2:8-2:11\n"
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("unexpected references on (*s).Error (-want +got):\n%s", diff)
		}
	})
}
