//go:build go1.23
// +build go1.23

package bad

// TODO(matloob): uncomment this and remove the space between the // and the @diag
// once the changes that produce the new go list error are submitted.
import _ "golang.org/lsptests/assign/internal/secret" //@diag("_", "go list", "use of internal package golang.org/lsptests/assign/internal/secret not allowed", "error"),diag("\"golang.org/lsptests/assign/internal/secret\"", "compiler", "could not import golang.org/lsptests/assign/internal/secret \\(no required module provides package golang.org/lsptests/assign/internal/secret; to add it:\\n\\txgo get golang.org/lsptests/assign/internal/secret\\)", "error")

func stuff() { //@item(stuff, "stuff", "func()", "func")
	x := "heeeeyyyy"
	random2(x) //@diag("x", "compiler", "cannot use x \\(variable of type string\\) as int value in argument to random2", "error")
	random2(1) //@complete("dom", random, random2, random3)
	y := 3     //@diag("y", "compiler", "declared and not used: y", "error")
}

type bob struct { //@item(bob, "bob", "struct{...}", "struct")
	x int
}
