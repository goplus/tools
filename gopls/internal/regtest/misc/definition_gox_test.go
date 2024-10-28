package misc

import (
	"testing"

	. "golang.org/x/tools/gopls/internal/lsp/regtest"
)

const overloadDefinition1 = `
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
println add(100, 7)
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
	fmt.Println(add__0(100, 7))
}
`

func TestOverloadDefinition1(t *testing.T) {
	Run(t, overloadDefinition1, func(t *testing.T, env *Env) {
		env.OpenFile("test.gop")
		loc := env.GoToDefinition(env.RegexpSearch("test.gop", "add"))
		name := env.Sandbox.Workdir.URIToPath(loc.URI)
		if want := "def.gop"; name != want {
			t.Errorf("GoToDefinition: got file %q, want %q", name, want)
		}
		// goxls : match the 'func' position of the corresponding overloaded function
		if want := env.RegexpSearch("def.gop", `func\(a, b int\) int`); loc != want {
			t.Errorf("GoToDefinition: got location %v, want %v", loc, want)
		}
	})
}

const overloadDefinition2 = `
-- go.mod --
module mod.com

go 1.19
-- def.gop --
func mulInt(a, b int) int {
	return a * b
}

func mulFloat(a, b float64) float64 {
	return a * b
}

func mul = (
	mulInt
	func(a, b string) string {
		return a + b
	}
	mulFloat
)
-- test.gop --
println mul(100, 7)
-- gop_autogen.go --
package main

import "fmt"

const _ = true
const Gopo_mul = "mulInt,,mulFloat"
func mulInt(a int, b int) int {
	return a * b
}
func mul__1(a string, b string) string {
	return a + b
}
func mulFloat(a float64, b float64) float64 {
	return a * b
}
func main() {
	fmt.Println(mulInt(100, 7))
}
`

func TestOverloadDefinition2(t *testing.T) {
	Run(t, overloadDefinition2, func(t *testing.T, env *Env) {
		env.OpenFile("test.gop")
		loc := env.GoToDefinition(env.RegexpSearch("test.gop", `println (mul)\(100, 7\)`))
		name := env.Sandbox.Workdir.URIToPath(loc.URI)
		if want := "def.gop"; name != want {
			t.Errorf("GoToDefinition: got file %q, want %q", name, want)
		}
		// goxls: match mulInt
		if want := env.RegexpSearch("def.gop", `func (mulInt)\(a, b int\) int`); loc != want {
			t.Errorf("GoToDefinition: got location %v, want %v", loc, want)
		}
	})
}

const overloadDefinition3 = `
-- go.mod --
module mod.com

go 1.19
-- def.gop --
type foo struct {
}

func (a *foo) mulInt(b int) *foo {
	return a
}

func (a *foo) mulFoo(b *foo) *foo {
	return a
}

func (foo).mul = (
	(foo).mulInt
	(foo).mulFoo
)
-- test.gop --
var a *foo
var c = a.mul(100)
-- gop_autogen.go --
package main

const _ = true

type foo struct {
}

const Gopo_foo_mul = ".mulInt,.mulFoo"
func (a *foo) mulInt(b int) *foo {
	return a
}
func (a *foo) mulFoo(b *foo) *foo {
	return a
}

var a *foo
var c = a.mulInt(100)

func main() {
}
`

func TestOverloadDefinition3(t *testing.T) {
	Run(t, overloadDefinition3, func(t *testing.T, env *Env) {
		env.OpenFile("test.gop")
		loc := env.GoToDefinition(env.RegexpSearch("test.gop", "mul"))
		name := env.Sandbox.Workdir.URIToPath(loc.URI)
		if want := "def.gop"; name != want {
			t.Errorf("GoToDefinition: got file %q, want %q", name, want)
		}
		// goxls: match mulInt
		if want := env.RegexpSearch("def.gop", `func \(a \*foo\) (mulInt)\(b int\) \*foo`); loc != want {
			t.Errorf("GoToDefinition: got location %v, want %v", loc, want)
		}
	})
}

const overloadDefinition4 = `
-- go.mod --
module mod.com

go 1.19
-- def.go --
package main

const GopPackage = true

type N struct {
}

func (m *N) OnKey__0(a string, fn func()) {
	fn()
}

func (m *N) OnKey__1(a string, fn func(key string)) {
	fn(a)
}

func (m *N) OnKey__2(a []string, fn func()) {
	fn()
}
-- test.gop --
n := &N{}

n.onKey("hello", func() {
	println("hello world")
})
-- gop_autogen.go --
package main

import "fmt"

const _ = true
func main() {
	n := &N{}
	n.OnKey__0("hello", func() {
		fmt.Println("hello world")
	})
}
`

func TestOverloadDefinition4(t *testing.T) {
	Run(t, overloadDefinition4, func(t *testing.T, env *Env) {
		env.OpenFile("test.gop")
		loc := env.GoToDefinition(env.RegexpSearch("test.gop", "onKey"))
		name := env.Sandbox.Workdir.URIToPath(loc.URI)
		if want := "def.go"; name != want {
			t.Errorf("GoToDefinition: got file %q, want %q", name, want)
		}
		if want := env.RegexpSearch("def.go", `OnKey__0`); loc != want {
			t.Errorf("GoToDefinition: got location %v, want %v", loc, want)
		}
	})
}
