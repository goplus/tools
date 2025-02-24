package main

import "fmt"

func Demo__0() { //@Demo__0
}

func Demo__1(n int) int { //@Demo__1
	return n * 100
}

func Demo__2(n1 int, n2 int) { //@Demo__2
	fmt.Println(n1, n2)
}

func Demo__3(s ...string) { //@Demo__3
}

type N struct {
}

func (m *N) Add__0(a int) { //@Add__0
}

func (m *N) Add__1(a ...string) { //@Add__1
}
