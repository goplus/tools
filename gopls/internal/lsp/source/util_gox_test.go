package source

import (
	"github.com/goplus/gop/ast"

	"testing"
)

func TestGoxTestClassType(t *testing.T) {
	type testData struct {
		isClass     bool
		isNormalGox bool
		isProj      bool
		fileName    string
		classType   string
		isGoxTest   bool
	}
	tests :=
		[]*testData{
			{false, false, false, "abc.gop", "", false},
			{false, false, false, "abc_test.gop", "", false},

			{true, true, false, "abc.gox", "abc", false},
			{true, true, false, "Abc.gox", "Abc", false},
			{true, true, false, "abc_demo.gox", "abc", false},
			{true, true, false, "Abc_demo.gox", "Abc", false},

			{true, false, false, "get.yap", "get", false},
			{true, false, false, "get_p_#id.yap", "get_p_id", false},
			{true, false, true, "main.yap", "main", false},

			{true, true, false, "main.gox", "main", false},
			{true, true, false, "main_demo.gox", "main", false},
			{true, true, false, "abc_xtest.gox", "abc", false},
			{true, true, false, "main_xtest.gox", "main", false},

			{true, true, false, "abc_test.gox", "abc", true},
			{true, true, false, "Abc_test.gox", "Abc", true},
			{true, true, false, "main_test.gox", "main", true},

			{true, false, false, "abc_yap.gox", "abc", false},
			{true, false, false, "Abc_yap.gox", "Abc", false},
			{true, false, true, "main_yap.gox", "main", false},

			{true, false, false, "abc_ytest.gox", "abc", true},
			{true, false, false, "Abc_ytest.gox", "Abc", true},
			{true, false, true, "main_ytest.gox", "main", true},
		}
	for _, test := range tests {
		f := &ast.File{IsClass: test.isClass, IsNormalGox: test.isNormalGox, IsProj: test.isProj}
		classType, isGoxTest := GoxTestClassType(f, test.fileName)
		if isGoxTest != test.isGoxTest {
			t.Fatalf("%v check classType isTest want %v, got %v.", test.fileName, test.isGoxTest, isGoxTest)
		}
		if classType != test.classType {
			t.Fatalf("%v getClassType want %v, got %v.", test.fileName, test.classType, classType)
		}
	}
}
