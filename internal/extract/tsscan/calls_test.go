package tsscan

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

func fn(name string, start, end int) extract.Symbol {
	return extract.Symbol{Name: name, FQN: name, Kind: extract.KindFunction, StartLine: start, EndLine: end}
}

func sub(parent, name, kind string, start, end int) extract.Symbol {
	return extract.Symbol{Name: name, FQN: parent + "." + name, Kind: kind, StartLine: start, EndLine: end}
}

func cls(name string, start, end int) extract.Symbol {
	return extract.Symbol{Name: name, FQN: name, Kind: extract.KindClass, StartLine: start, EndLine: end}
}

// renderCalls flattens call references to "from>target" for compact asserts.
// Same-file targets render as "same:FQN"; imports as "spec:To" (or
// "spec:default.To" for a default export).
func renderCalls(refs []extract.Reference) []string {
	var out []string
	for _, r := range refs {
		if r.Kind != extract.RefCalls {
			continue
		}
		var target string
		switch {
		case r.ImportSpec != "" && r.DefaultExport:
			target = fmt.Sprintf("%s:default.%s", r.ImportSpec, r.To)
		case r.ImportSpec != "":
			target = fmt.Sprintf("%s:%s", r.ImportSpec, r.To)
		default:
			target = "same:" + r.To
		}
		out = append(out, r.From+">"+target)
	}
	sort.Strings(out)
	return out
}

func TestCallRefs(t *testing.T) {
	const path = "src/app.ts"
	tests := []struct {
		name string
		path string
		src  string
		syms []extract.Symbol
		want []string
	}{
		{
			name: "same-file bare call, await and new",
			src: lines(
				"function helper() {}",    // 1
				"class Widget {}",         // 2
				"async function main() {", // 3
				"  helper();",             // 4
				"  await helper();",       // 5
				"  return new Widget();",  // 6
				"}"),                      // 7
			syms: []extract.Symbol{fn("helper", 1, 1), cls("Widget", 2, 2), fn("main", 3, 7)},
			want: []string{"main>same:Widget", "main>same:helper"},
		},
		{
			name: "arrow-function variable target and generic call",
			src: lines(
				"const pick = (x: number) => x;", // 1
				"function main() {",              // 2
				"  return pick<number>(1);",      // 3
				"}"),                             // 4
			syms: []extract.Symbol{{Name: "pick", FQN: "pick", Kind: extract.KindVariable, StartLine: 1, EndLine: 1}, fn("main", 2, 4)},
			want: []string{"main>same:pick"},
		},
		{
			name: "module-level call is attributed to the file",
			src: lines(
				"function boot() {}", // 1
				"boot();"),           // 2
			syms: []extract.Symbol{fn("boot", 1, 1)},
			want: []string{path + ">same:boot"},
		},
		{
			name: "declarations are not calls",
			src: lines(
				"function helper(x: number): void {}",   // 1
				"class Svc {",                           // 2
				"  helper(x: number): string {",         // 3
				"    return 'x';",                       // 4
				"  }",                                   // 5
				"  private async helper2() {}",          // 6
				"}",                                     // 7
				"interface Api {",                       // 8
				"  helper(a: number): void;",            // 9
				"}",                                     // 10
				"const o = { helper() { return 1; } };", // 11
				"function declared(): void {",           // 12
				"  return;",                             // 13
				"}"),                                    // 14
			syms: []extract.Symbol{fn("helper", 1, 1), cls("Svc", 2, 7), sub("Svc", "helper", extract.KindMethod, 3, 5), fn("declared", 12, 14)},
			want: nil,
		},
		{
			name: "overload signature and generator are declarations",
			src: lines(
				"function helper(a: string): string;",    // 1
				"function helper(a: number): number;",    // 2
				"function* gen() {}",                     // 3
				"function helper(a: any) { return a; }"), // 4
			syms: []extract.Symbol{fn("helper", 4, 4), fn("gen", 3, 3)},
			want: nil,
		},
		{
			name: "comments strings and template text are not calls",
			src: lines(
				"function helper() {}",             // 1
				"function main() {",                // 2
				"  // helper();",                   // 3
				"  /* helper(); */",                // 4
				"  const s = 'helper()';",          // 5
				"  const t = `call helper() now`;", // 6
				"  const u = \"helper(\";",         // 7
				"}"),                               // 8
			syms: []extract.Symbol{fn("helper", 1, 1), fn("main", 2, 8)},
			want: nil,
		},
		{
			name: "template interpolation bodies are code",
			src: lines(
				"function helper() { return 1; }", // 1
				"function main() {",               // 2
				"  return `v=${helper()}`;",       // 3
				"}"),                              // 4
			syms: []extract.Symbol{fn("helper", 1, 1), fn("main", 2, 4)},
			want: []string{"main>same:helper"},
		},
		{
			name: "member calls on unknown objects are out of scope",
			src: lines(
				"function helper() {}",      // 1
				"function main(obj: any) {", // 2
				"  obj.helper();",           // 3
				"  obj.a.helper();",         // 4
				"  obj?.helper();",          // 5
				"  console.log(helper);",    // 6
				"}"),                        // 7
			syms: []extract.Symbol{fn("helper", 1, 1), fn("main", 2, 7)},
			want: nil,
		},
		{
			name: "comparison is not a generic call",
			src: lines(
				"function size() { return 1; }", // 1
				"function main(n: number) {",    // 2
				"  return size < n && n > (2);", // 3
				"}"),                            // 4
			syms: []extract.Symbol{fn("size", 1, 1), fn("main", 2, 4)},
			want: nil,
		},
		{
			name: "nested helper is only visible inside its parent",
			src: lines(
				"function outer() {",    // 1
				"  function inner() {}", // 2
				"  inner();",            // 3
				"}",                     // 4
				"function other() {",    // 5
				"  inner();",            // 6
				"}"),                    // 7
			syms: []extract.Symbol{fn("outer", 1, 4), sub("outer", "inner", extract.KindFunction, 2, 2), fn("other", 5, 7)},
			want: []string{"outer>same:outer.inner"},
		},
		{
			name: "class members are not reachable by a bare name",
			src: lines(
				"class Svc {",           // 1
				"  run() { return 1; }", // 2
				"}",                     // 3
				"function main() {",     // 4
				"  run();",              // 5
				"}"),                    // 6
			syms: []extract.Symbol{cls("Svc", 1, 3), sub("Svc", "run", extract.KindMethod, 2, 2), fn("main", 4, 6)},
			want: nil,
		},
		{
			name: "this.method resolves within the same class only",
			src: lines(
				"class A {",                            // 1
				"  one() { this.two(); this.nope(); }", // 2
				"  two() {}",                           // 3
				"}",                                    // 4
				"class B {",                            // 5
				"  go() { this.two(); }",               // 6
				"}"),                                   // 7
			syms: []extract.Symbol{
				cls("A", 1, 4), sub("A", "one", extract.KindMethod, 2, 2), sub("A", "two", extract.KindMethod, 3, 3),
				cls("B", 5, 7), sub("B", "go", extract.KindMethod, 6, 6),
			},
			want: []string{"A.one>same:A.two"},
		},
		{
			name: "Owner.member resolves a same-file static member",
			src: lines(
				"class Util {",                  // 1
				"  static make() { return 1; }", // 2
				"}",                             // 3
				"function main() {",             // 4
				"  Util.make();",                // 5
				"  Util.missing();",             // 6
				"}"),                            // 7
			syms: []extract.Symbol{cls("Util", 1, 3), sub("Util", "make", extract.KindMethod, 2, 2), fn("main", 4, 7)},
			want: []string{"main>same:Util.make"},
		},
		{
			name: "named, aliased and default imports",
			src: lines(
				"import def, { a, b as bee, type T } from './x';", // 1
				"import type { U } from './types';",               // 2
				"function main() {",                               // 3
				"  a();",                                          // 4
				"  bee();",                                        // 5
				"  def();",                                        // 6
				"  new a();",                                      // 7
				"}"),                                              // 8
			syms: []extract.Symbol{fn("main", 3, 8)},
			want: []string{"main>./x:a", "main>./x:b", "main>./x:default."},
		},
		{
			name: "namespace and default-object member calls",
			src: lines(
				"import * as ns from '@/lib/ns';", // 1
				"import api from './api';",        // 2
				"import { Svc } from './svc';",    // 3
				"function main() {",               // 4
				"  ns.run();",                     // 5
				"  api.get();",                    // 6
				"  Svc.create();",                 // 7
				"  ns.deep.run();",                // 8
				"}"),                              // 9
			syms: []extract.Symbol{fn("main", 4, 9)},
			want: []string{"main>./api:default.get", "main>./svc:Svc.create", "main>@/lib/ns:run"},
		},
		{
			name: "require bindings",
			path: "src/app.js",
			src: lines(
				"const { a, b: bee } = require('./x');", // 1
				"const lib = require('./lib');",         // 2
				"function main() {",                     // 3
				"  a();",                                // 4
				"  bee();",                              // 5
				"  lib.run();",                          // 6
				"}"),                                    // 7
			syms: []extract.Symbol{fn("main", 3, 7)},
			want: []string{"main>./lib:run", "main>./x:a", "main>./x:b"},
		},
		{
			name: "node builtins and type-only imports yield nothing",
			src: lines(
				"import { readFile } from 'node:fs';", // 1
				"import fs from 'node:fs/promises';",  // 2
				"function main() {",                   // 3
				"  readFile();",                       // 4
				"  fs.stat();",                        // 5
				"}"),                                  // 6
			syms: []extract.Symbol{fn("main", 3, 6)},
			want: nil,
		},
		{
			name: "a same-file symbol shadows an import of the same name inside it",
			src: lines(
				"import { run } from './x';", // 1
				"function main() {",          // 2
				"  function run() {}",        // 3
				"  run();",                   // 4
				"}"),                         // 5
			syms: []extract.Symbol{fn("main", 2, 5), sub("main", "run", extract.KindFunction, 3, 3)},
			want: []string{"main>same:main.run"},
		},
		{
			name: "JSX elements are not call candidates but expressions inside are",
			path: "src/Page.tsx",
			src: lines(
				"function format(x: number) { return x; }", // 1
				"function Page() {",                        // 2
				"  return <Item label={format(1)} />;",     // 3
				"}"),                                       // 4
			syms: []extract.Symbol{fn("format", 1, 1), fn("Page", 2, 4)},
			want: []string{"Page>same:format"},
		},
		{
			name: "documented false positive: a parameter shadowing a same-file function",
			src: lines(
				"function cb() {}",               // 1
				"function run(cb: () => void) {", // 2
				"  cb();",                        // 3
				"}"),                             // 4
			syms: []extract.Symbol{fn("cb", 1, 1), fn("run", 2, 4)},
			want: []string{"run>same:cb"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.path
			if p == "" {
				p = path
			}
			got := renderCalls(CallRefs(p, []byte(tc.src), tc.syms))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("calls = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCallRefsFieldContract(t *testing.T) {
	src := lines("function helper() {}", "function main() { helper(); }")
	refs := CallRefs("a.ts", []byte(src), []extract.Symbol{fn("helper", 1, 1), fn("main", 2, 2)})
	if len(refs) != 1 {
		t.Fatalf("refs = %+v", refs)
	}
	r := refs[0]
	if r.From != "main" || r.FromFile != "a.ts" || r.ToFile != "a.ts" || r.To != "helper" ||
		r.Kind != extract.RefCalls || !r.Qualified || r.Line != 2 || r.FromLine != 2 || r.ToLine != 1 {
		t.Errorf("unexpected reference contract: %+v", r)
	}
}

func TestEnrichAddsCallRefsAlongsideJSX(t *testing.T) {
	src := lines("function fmt() {}", "export function Page() { fmt(); return <Item />; }")
	res := &extract.FileResult{Symbols: []extract.Symbol{fn("fmt", 1, 1), fn("Page", 2, 2)}}
	Enrich(res, "Page.tsx", []byte(src))
	var calls, jsx int
	for _, r := range res.References {
		if r.Kind == extract.RefCalls && r.To == "fmt" {
			calls++
		}
		if r.Kind == extract.RefCalls && r.To == "Item" {
			jsx++
		}
	}
	if calls != 1 || jsx != 1 {
		t.Errorf("call refs = %d, jsx refs = %d, want 1 and 1 (%+v)", calls, jsx, res.References)
	}
}

func TestDefaultExportName(t *testing.T) {
	for src, want := range map[string]string{
		"export default function Foo() {}":      "Foo",
		"const Foo = 1;\nexport default Foo;\n": "Foo",
		"export default memo(Foo);":             "Foo",
		"export default () => 1;":               "",
		"// export default Nope\n":              "",
	} {
		if got := DefaultExportName([]byte(src)); got != want {
			t.Errorf("DefaultExportName(%q) = %q, want %q", src, got, want)
		}
	}
}

// BenchmarkCallRefs pins the single-pass cost on a 2.5k-line file with many
// candidate names.
func BenchmarkCallRefs(b *testing.B) {
	var sb strings.Builder
	var syms []extract.Symbol
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&sb, "function f%d(x: number) {\n  const v = f%d(x) + g(x);\n  if (v > 1) { return v; }\n  return v + 1;\n}\n", i, (i+1)%500)
		syms = append(syms, fn(fmt.Sprintf("f%d", i), i*5+1, i*5+5))
	}
	src := []byte(sb.String())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CallRefs("big.ts", src, syms)
	}
}
