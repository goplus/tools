package d

const GopPackage = true

type Foo struct {
}

const Gopo_Foo_Mul = ".MulInt,.MulFoo"
const Gopo_Mul = "MulInt,MulFloat"

func Add__0(a int, b int) int { //@mark(d_add__0,"Add__0")
	return a + b
}

func Add__1(a string, b string) string { //@mark(d_add__1,"Add__1")
	return a + b
}

func (a *Foo) MulInt(b int) *Foo { //@mark(d_foo_mulInt,"MulInt")
	return a
}

func (a *Foo) MulFoo(b *Foo) *Foo { //@mark(d_foo_mulFoo,"MulFoo")
	return a
}

func MulInt(a int, b int) int { //@mark(d_mulInt,"MulInt")
	return a * b
}

func MulFloat(a float64, b float64) float64 { //@mark(d_mulFloat,"MulFloat")
	return a * b
}
