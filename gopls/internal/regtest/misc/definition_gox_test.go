package misc

import (
	"testing"

	. "golang.org/x/tools/gopls/internal/lsp/regtest"
)

const anonyOverload = `
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
println add("Hello", "World")
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
	fmt.Println(add__1("Hello", "World"))
}
`

func TestAnonyOverload(t *testing.T) {
	testCases := []defTest{
		{`test.gop`, `println (add)\(100, 7\)`, "def.gop", `func\(a, b int\) int`},
		{`test.gop`, `println (add)\("Hello", "World"\)`, "def.gop", `func\(a, b string\) string`},
	}
	runGoToDefinitionTest(t, anonyOverload, testCases)
}

const overloadMixAnonyAndNamed = `
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
println mul("Hello", "World")
println mul(200.5, 2.3)
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
	fmt.Println(mul__1("Hello", "World"))
	fmt.Println(mulFloat(200.5, 2.3))
}
`

func TestOverloadMixAnonyAndNamed(t *testing.T) {
	testCases := []defTest{
		{`test.gop`, `println (mul)\(100, 7\)`, "def.gop", `func (mulInt)\(a, b int\) int`},
		{`test.gop`, `println (mul)\("Hello", "World"\)`, "def.gop", `func\(a, b string\) string`},
		{`test.gop`, `println (mul)\(200.5, 2.3\)`, "def.gop", `func (mulFloat)\(a, b float64\) float64`},
	}
	runGoToDefinitionTest(t, overloadMixAnonyAndNamed, testCases)
}

const overloadMethod = `
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
var d = a.mul(&foo{})
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
var d = a.mulFoo(&foo{})

func main() {
}
`

func TestOverloadMethod(t *testing.T) {
	mulIntLocReg := `func \(a \*foo\) (mulInt)\(b int\) \*foo`
	mulFooLocReg := `func \(a \*foo\) (mulFoo)\(b \*foo\) \*foo`
	testCases := []defTest{
		{`test.gop`, `var c = a.(mul)\(100\)`, "def.gop", mulIntLocReg},
		{`test.gop`, `var d = a.(mul)\(&foo\{\}\)`, "def.gop", mulFooLocReg},
		{`def.gop`, `\(foo\)\.(mulInt)`, "def.gop", mulIntLocReg},
		{`def.gop`, `\(foo\)\.(mulFoo)`, "def.gop", mulFooLocReg},
	}
	runGoToDefinitionTest(t, overloadMethod, testCases)
}

const overloadFromGo = `
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

n.onKey("hello", func(key string) {
	println("hello world", key)
})

n.onKey([]string{"hello", "world"}, func() {
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
	n.OnKey__1("hello", func(key string) {
		fmt.Println("hello world", key)
	})
	n.OnKey__2([]string{"hello", "world"}, func() {
		fmt.Println("hello world")
	})
}
`

func TestOverloadFromGo(t *testing.T) {
	testCases := []defTest{
		{`test.gop`, `n.(onKey)\("hello", func\(\)`, "def.go", `OnKey__0`},
		{`test.gop`, `n.(onKey)\("hello", func\(key string\)`, "def.go", `OnKey__1`},
		{`test.gop`, `n.(onKey)\(\[\]string\{"hello", "world"\}, func\(\)`, "def.go", `OnKey__2`},
	}
	runGoToDefinitionTest(t, overloadFromGo, testCases)
}

const overloadCrossPkg = `
-- go.mod --
module mod.com

go 1.19
-- lib/lib.gop --
package lib

func Add = (
	func(a, b int) int {
		return a + b
	}
	func(a, b string) string {
		return a + b
	}
)

func MulInt(a, b int) int {
	return a * b
}

func MulFloat(a, b float64) float64 {
	return a * b
}

func Mul = (
	MulInt
	func(x, y string) string {
		return x + y
	}
	MulFloat
)

type Foo struct {
}

func (a *Foo) MulInt(b int) *Foo {
	return a
}

func (a *Foo) MulFoo(b *Foo) *Foo {
	return a
}

func (Foo).Mul = (
	(Foo).MulInt
	(Foo).MulFoo
)
-- lib/gop_autogen.go --
package lib

const GopPackage = true
const _ = true
const Gopo_Mul = "MulInt,,MulFloat"

type Foo struct {
}

const Gopo_Foo_Mul = ".MulInt,.MulFoo"
func Add__0(a int, b int) int {
	return a + b
}
func Add__1(a string, b string) string {
	return a + b
}
func MulInt(a int, b int) int {
	return a * b
}
func Mul__1(x string, y string) string {
	return x + y
}
func MulFloat(a float64, b float64) float64 {
	return a * b
}
func (a *Foo) MulInt(b int) *Foo {
	return a
}
func (a *Foo) MulFoo(b *Foo) *Foo {
	return a
}
-- lib2/lib2.go --
package lib2

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
-- main.gop --
import (
	"mod.com/lib"
	"mod.com/lib2"
)

println lib.Add(100, 7)
println lib.Add("Hello", "World")

println lib.Mul(100, 7)
println lib.Mul("Hello", "World")
println lib.Mul(200.5, 2.3)

var a *lib.Foo
var c = a.Mul(100)
var d = a.Mul(&lib.Foo{})

_ = c
_ = d

n := &lib2.N{}

n.OnKey("hello", func() {
	println("hello world")
})

n.OnKey("hello", func(key string) {
	println("hello world", key)
})

n.OnKey([]string{"hello", "world"}, func() {
	println("hello world")
})
-- gop_autogen.go --
package main

import (
	"fmt"
	"mod.com/lib"
	"mod.com/lib2"
)

const _ = true
func main() {
	fmt.Println(lib.Add__0(100, 7))
	fmt.Println(lib.Add__1("Hello", "World"))
	fmt.Println(lib.MulInt(100, 7))
	fmt.Println(lib.Mul__1("Hello", "World"))
	fmt.Println(lib.MulFloat(200.5, 2.3))
	var a *lib.Foo
	var c = a.MulInt(100)
	var d = a.MulFoo(&lib.Foo{})
	_ = c
	_ = d
	n := &lib2.N{}
	n.OnKey__0("hello", func() {
		fmt.Println("hello world")
	})
	n.OnKey__1("hello", func(key string) {
		fmt.Println("hello world", key)
	})
	n.OnKey__2([]string{"hello", "world"}, func() {
		fmt.Println("hello world")
	})
}
`

// Test cross package 's overload definition
func TestOverloadCrossPkg(t *testing.T) {
	testCases := []defTest{
		// anony overload
		{`main.gop`, `println lib.(Add)\(100, 7\)`, "lib/lib.gop", `func\(a, b int\) int`},
		{`main.gop`, `println lib.(Add)\("Hello", "World"\)`, "lib/lib.gop", `func\(a, b string\) string`},

		// named & anony overload
		{`main.gop`, `println lib.(Mul)\(100, 7\)`, "lib/lib.gop", `func (MulInt)\(a, b int\) int`},
		{`main.gop`, `println lib.(Mul)\("Hello", "World"\)`, "lib/lib.gop", `func\(x, y string\) string`},
		{`main.gop`, `println lib.(Mul)\(200.5, 2.3\)`, "lib/lib.gop", `func (MulFloat)\(a, b float64\) float64`},

		// method
		{`main.gop`, `var c = a.(Mul)\(100\)`, "lib/lib.gop", `func \(a \*Foo\) (MulInt)\(b int\) \*Foo`},
		{`main.gop`, `var d = a.(Mul)\(&lib\.Foo\{\}\)`, "lib/lib.gop", `func \(a \*Foo\) (MulFoo)\(b \*Foo\) \*Foo`},

		// from go
		{`main.gop`, `n.(OnKey)\("hello", func\(\)`, "lib2/lib2.go", `OnKey__0`},
		{`main.gop`, `n.(OnKey)\("hello", func\(key string\)`, "lib2/lib2.go", `OnKey__1`},
		{`main.gop`, `n.(OnKey)\(\[\]string\{"hello", "world"\}, func\(\)`, "lib2/lib2.go", `OnKey__2`},
	}
	runGoToDefinitionTest(t, overloadCrossPkg, testCases)
}

type defTest struct {
	findLocFile string
	findLocReg  string
	wantFile    string
	wantLocReg  string
}

func runGoToDefinitionTest(t *testing.T, files string, testCases []defTest) {
	Run(t, files, func(t *testing.T, env *Env) {
		for _, test := range testCases {
			env.OpenFile(test.findLocFile)
			loc := env.GoToDefinition(env.RegexpSearch(test.findLocFile, test.findLocReg))
			name := env.Sandbox.Workdir.URIToPath(loc.URI)
			if name != test.wantFile {
				t.Errorf("GoToDefinition: got file %q, want %q", name, test.wantFile)
			}
			if want := env.RegexpSearch(test.wantFile, test.wantLocReg); loc != want {
				t.Errorf("GoToDefinition:%v got location %v, want %v", test.findLocReg, loc, want)
			}
		}
	})
}
