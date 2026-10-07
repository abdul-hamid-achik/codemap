package sittersrc

import (
	"sort"
	"strings"
	"testing"
)

const pyRefsFixture = `import os
import pkg.sub as sub
import pkg.deep.mod
from .helpers import util, other as alias
from . import sibling


def top():
    local()
    util()
    alias()
    sub.run()
    pkg.deep.mod.go()
    sibling.call()
    os.getcwd()
    print("x")
    Widget()
    Widget.build()


def local():
    pass


def shadowed(local):
    local()


def rebound():
    util = make()
    util()


def outer():
    def inner():
        pass
    inner()
    fn = lambda top: top()
    [local() for local in items]


class Widget:
    def __init__(self):
        self.render()
        self.missing()

    def render(self):
        def closure():
            self.render()
        closure()

    @classmethod
    def build(cls):
        cls.render()

    @staticmethod
    def static(self):
        self.render()


local()
`

func TestPythonCallRefs(t *testing.T) {
	e, _ := New("python")
	res, err := e.ExtractFile("app/main.py", []byte(pyRefsFixture))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res.References {
		target := "same:" + r.To
		if r.ImportSpec != "" {
			target = "import:" + r.ImportSpec + ":" + r.To
		} else if r.ToFile != "app/main.py" {
			t.Errorf("same-file candidate scoped to %q", r.ToFile)
		}
		got = append(got, r.From+" -> "+target)
	}
	sort.Strings(got)
	want := []string{
		"Widget.__init__ -> same:Widget.render",
		"Widget.build -> same:Widget.render",
		"Widget.render.closure -> same:Widget.render",
		"Widget.render -> same:Widget.render.closure",
		"app/main.py -> same:local",
		"outer -> same:outer.inner",
		"top -> import:.helpers:other",
		"top -> import:.helpers:util",
		"top -> import:.sibling:call",
		"top -> import:pkg.deep.mod:go",
		"top -> import:pkg.deep:mod.go",
		"top -> import:pkg.sub:run",
		"top -> import:pkg:deep.mod.go",
		"top -> import:.:sibling.call",
		"top -> import:os:getcwd", // dropped by the indexer: os is no project file
		"top -> same:Widget",
		"top -> same:Widget.build",
		"top -> same:local",
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("python call refs:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, r := range res.References {
		if r.From == "top" && r.FromLine != 8 {
			t.Errorf("top's FromLine = %d, want 8 (its def line)", r.FromLine)
		}
	}

	imports := strings.Join(res.Imports, ",")
	for _, spec := range []string{"os", "pkg.sub", "pkg.deep.mod", ".helpers", ".helpers.util", ".helpers.other", ".", ".sibling"} {
		if !strings.Contains(","+imports+",", ","+spec+",") {
			t.Errorf("imports %v missing %q", res.Imports, spec)
		}
	}
}
