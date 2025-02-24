package b

import "fmt"

func Demo__0() { //@mark(b_demo__0,"Demo__0")
}

func Demo__1(n int) int { //@mark(b_demo__1,"Demo__1")
	return n * 100
}

func Demo__2(n1 int, n2 int) { //@mark(b_demo__2,"Demo__2")
	fmt.Println(n1, n2)
}

func Demo__3(s ...string) { //@mark(b_demo__3,"Demo__3")
}

type N struct {
}

func (m *N) Add__0(a int) { //@mark(b_add__0,"Add__0")
}

func (m *N) Add__1(a ...string) { //@mark(b_add__1,"Add__1")
}
