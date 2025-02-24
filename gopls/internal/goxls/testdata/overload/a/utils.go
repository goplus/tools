package a

import "fmt"

const GopPackage = true

type DataInterface[T any] interface {
	Size() int
	Add__0(v ...T)
	Add__1(v DataInterface[T])
	IndexOf__0(v T) int
	IndexOf__1(pos int, v T) int
}

func Demo__0() { //@mark(a_demo__0,"Demo__0")
}

func Demo__1(n int) int { //@mark(a_demo__1,"Demo__1")
	return n * 100
}

func Demo__2(n1 int, n2 int) { //@mark(a_demo__2,"Demo__2")
	fmt.Println(n1, n2)
}

func Demo__3(s ...string) { //@mark(a_demo__3,"Demo__3")
}

type N struct {
}

func (m *N) Add__0(a int) { //@mark(a_add__0,"Add__0")
}

func (m *N) Add__1(a ...string) { //@mark(a_add__1,"Add__1")
}
