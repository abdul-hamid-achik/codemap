package tsscan

import (
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// componentSample mirrors a typical Next.js page: imports, one enclosing
// component rendering other components, intrinsic elements, member-expression
// components, and TS generics that must NOT be mistaken for JSX.
const componentSample = `import { OverviewDashboard } from "@/components/overview-dashboard";
import PageHeading from "./page-heading";
import * as Card from "../ui/card";
import { motion } from "framer-motion";
import "./globals.css";
export { helper } from "./helpers";

type Props = { items: Array<Item>; lookup: Map<string, Item> };

export default function DashboardPage({ items }: Props) {
  const [state, setState] = useState<State>(initial);
  const pick = <T,>(x: T): T => x;
  const wide = <T extends object>(x: T) => x;
  if (items.length < Batch.size) {
    return <div className="empty" />;
  }
  return (
    <main>
      <PageHeading title="Overview" />
      <Card.Header compact />
      <motion.div animate>
        <OverviewDashboard data={items} />
      </motion.div>
      <OverviewDashboard data={items} />
    </main>
  );
}

const banner = <PageHeading title="top-level" />;
`

func sampleSymbols() []extract.Symbol {
	return []extract.Symbol{
		{Name: "DashboardPage", FQN: "DashboardPage", Kind: extract.KindFunction, StartLine: 10, EndLine: 27},
		{Name: "banner", FQN: "banner", Kind: extract.KindVariable, StartLine: 29, EndLine: 29},
	}
}

func refTo(refs []extract.Reference, to string) *extract.Reference {
	for i := range refs {
		if refs[i].To == to {
			return &refs[i]
		}
	}
	return nil
}

func TestJSXComponentUsage(t *testing.T) {
	refs := JSXRefs("app/dashboard/page.tsx", []byte(componentSample), sampleSymbols())

	for _, want := range []string{"PageHeading", "OverviewDashboard"} {
		r := refTo(refs, want)
		if r == nil {
			t.Fatalf("missing JSX ref to %s (refs=%v)", want, refs)
		}
		if r.From != "DashboardPage" {
			t.Errorf("ref to %s attributed to %q, want DashboardPage", want, r.From)
		}
		if r.Kind != extract.RefCalls {
			t.Errorf("ref to %s kind = %q, want calls", want, r.Kind)
		}
		if !r.Qualified {
			t.Errorf("ref to %s should be Qualified (candidate weight)", want)
		}
	}
	// Deduped: OverviewDashboard rendered twice → one reference.
	count := 0
	for _, r := range refs {
		if r.To == "OverviewDashboard" && r.From == "DashboardPage" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("OverviewDashboard refs = %d, want 1 (deduped)", count)
	}
}

func TestJSXMemberExpressions(t *testing.T) {
	refs := JSXRefs("app/dashboard/page.tsx", []byte(componentSample), sampleSymbols())

	// <Card.Header/> references the object binding Card (a namespace import).
	if r := refTo(refs, "Card"); r == nil {
		t.Errorf("missing ref to Card for <Card.Header/>")
	}
	// <motion.div/> is a component despite the lowercase root: it references motion.
	if r := refTo(refs, "motion"); r == nil {
		t.Errorf("missing ref to motion for <motion.div/>")
	}
}

func TestJSXIntrinsicElementsExcluded(t *testing.T) {
	refs := JSXRefs("app/dashboard/page.tsx", []byte(componentSample), sampleSymbols())
	for _, bad := range []string{"div", "main"} {
		if r := refTo(refs, bad); r != nil {
			t.Errorf("intrinsic element <%s> must not create an edge (got %+v)", bad, *r)
		}
	}
}

func TestJSXGenericsNotMistakenForComponents(t *testing.T) {
	refs := JSXRefs("app/dashboard/page.tsx", []byte(componentSample), sampleSymbols())
	for _, bad := range []string{"T", "State", "Item", "Array", "Map", "Batch", "Props"} {
		if r := refTo(refs, bad); r != nil {
			t.Errorf("generic/type/comparison %q must not create a JSX edge (got %+v)", bad, *r)
		}
	}
}

func TestJSXTopLevelAttributedToFile(t *testing.T) {
	refs := JSXRefs("app/dashboard/page.tsx", []byte(componentSample), sampleSymbols())
	// `banner` is a variable symbol containing line 30, so the innermost
	// enclosure is the banner symbol itself.
	found := false
	for _, r := range refs {
		if r.To == "PageHeading" && r.From == "banner" {
			found = true
		}
	}
	if !found {
		t.Errorf("top-level JSX should attribute to its innermost enclosing symbol; refs=%v", refs)
	}

	// With no symbols at all, attribution falls back to the file path.
	refs = JSXRefs("app/x.tsx", []byte("const el = <Foo/>;\n"), nil)
	if len(refs) != 1 || refs[0].From != "app/x.tsx" {
		t.Errorf("file-level attribution = %+v, want From=app/x.tsx", refs)
	}
}

func TestJSXOnlyInJSXFiles(t *testing.T) {
	// A .ts file cannot contain JSX; Enrich must not scan it for elements
	// (old-style <Foo>value type assertions live there).
	res := &extract.FileResult{Path: "cast.ts", Language: "typescript"}
	Enrich(res, "cast.ts", []byte("const x = <Foo>getValue();\n"))
	for _, r := range res.References {
		if r.To == "Foo" && r.Kind == extract.RefCalls {
			t.Errorf("type assertion in .ts must not create a JSX edge: %+v", r)
		}
	}
}

func TestImports(t *testing.T) {
	imports := Imports([]byte(componentSample))
	want := []string{
		"@/components/overview-dashboard", "./page-heading", "../ui/card",
		"framer-motion", "./globals.css", "./helpers",
	}
	got := map[string]bool{}
	for _, s := range imports {
		got[s] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing import %q (got %v)", w, imports)
		}
	}
}

func TestImportsRequireAndDynamic(t *testing.T) {
	src := []byte("const a = require('./legacy');\nconst b = await import(\"./lazy\");\n")
	imports := Imports(src)
	got := map[string]bool{}
	for _, s := range imports {
		got[s] = true
	}
	if !got["./legacy"] || !got["./lazy"] {
		t.Errorf("require/dynamic import missing: %v", imports)
	}
}

func TestFrameworkRefsAppRouterPage(t *testing.T) {
	src := []byte("export async function generateMetadata() {}\nexport default function DashboardPage() { return null; }\n")
	refs := FrameworkRefs("apps/web/src/app/(product)/dashboard/page.tsx", src)
	want := map[string]bool{"DashboardPage": false, "generateMetadata": false}
	for _, r := range refs {
		if _, ok := want[r.To]; ok {
			want[r.To] = true
			if r.Kind != extract.RefReferences {
				t.Errorf("framework ref kind = %q, want references", r.Kind)
			}
			if r.From != "apps/web/src/app/(product)/dashboard/page.tsx" {
				t.Errorf("framework ref From = %q, want the file path", r.From)
			}
		}
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("missing framework wiring ref for %s (refs=%v)", name, refs)
		}
	}
	// generateStaticParams isn't exported here — must not be wired.
	for _, r := range refs {
		if r.To == "generateStaticParams" {
			t.Errorf("unexported name wired: %+v", r)
		}
	}
}

func TestFrameworkRefsRouteHandlers(t *testing.T) {
	src := []byte("export async function GET(req: Request) {}\nexport const POST = handler;\nfunction helper() {}\n")
	refs := FrameworkRefs("apps/web/src/app/api/evidence/route.ts", src)
	got := map[string]bool{}
	for _, r := range refs {
		got[r.To] = true
	}
	if !got["GET"] || !got["POST"] {
		t.Errorf("route handlers not wired: %v", refs)
	}
	if got["helper"] {
		t.Errorf("non-exported non-verb helper must not be wired")
	}
}

func TestFrameworkRefsDefaultExportIdentifier(t *testing.T) {
	src := []byte("function Layout() { return null; }\nexport default Layout\n")
	refs := FrameworkRefs("src/app/layout.tsx", src)
	if r := refTo(refs, "Layout"); r == nil {
		t.Errorf("export default <ident> not wired: %v", refs)
	}
}

// TestFrameworkRefsWrappedDefaultExport pins the wrapped default-export forms
// (`export default memo(Page)`, forwardRef, and a wrapper chain): the innermost
// identifier is the component the framework actually invokes, so it must be
// wired exactly like a bare `export default Page` — otherwise every memo'd
// App Router page shows up as an orphan.
func TestFrameworkRefsWrappedDefaultExport(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"function Page() { return null; }\nexport default memo(Page)\n", "Page"},
		{"function Input() { return null; }\nexport default forwardRef(Input);\n", "Input"},
		{"function Page() { return null; }\nexport default memo(forwardRef(Page))\n", "Page"},
	}
	for _, c := range cases {
		refs := FrameworkRefs("src/app/page.tsx", []byte(c.src))
		if r := refTo(refs, c.want); r == nil {
			t.Errorf("wrapped default export %q not wired to %s: %v", c.src, c.want, refs)
		}
		// The wrapper itself is never a framework-wired name.
		if r := refTo(refs, "memo"); r != nil {
			t.Errorf("wrapper identifier wired as a component: %+v", r)
		}
	}
}

func TestFrameworkRefsScopedToRouterTrees(t *testing.T) {
	src := []byte("export default function Error() { return null; }\n")
	if refs := FrameworkRefs("packages/ui/src/error.tsx", src); len(refs) != 0 {
		t.Errorf("convention basename outside app/pages trees must not wire: %v", refs)
	}
	if refs := FrameworkRefs("apps/web/src/components/table.tsx", src); len(refs) != 0 {
		t.Errorf("non-convention file must not wire: %v", refs)
	}
}

func TestFrameworkRefsMiddleware(t *testing.T) {
	src := []byte("export function middleware(req: Request) {}\nexport const config = { matcher: ['/'] };\n")
	refs := FrameworkRefs("apps/web/src/middleware.ts", src)
	if r := refTo(refs, "middleware"); r == nil {
		t.Errorf("middleware not wired: %v", refs)
	}
}

func TestEnrichPopulatesAll(t *testing.T) {
	res := &extract.FileResult{
		Path: "app/page.tsx", Language: "typescript",
		Symbols: []extract.Symbol{{Name: "Page", FQN: "Page", Kind: extract.KindFunction, StartLine: 2, EndLine: 4}},
	}
	src := []byte("import Hero from './hero';\nexport default function Page() {\n  return <Hero />;\n}\n")
	Enrich(res, "app/page.tsx", src)

	if len(res.Imports) != 1 || res.Imports[0] != "./hero" {
		t.Errorf("imports = %v, want [./hero]", res.Imports)
	}
	var jsx, wiring bool
	for _, r := range res.References {
		if r.To == "Hero" && r.Kind == extract.RefCalls && r.From == "Page" {
			jsx = true
		}
		if r.To == "Page" && r.Kind == extract.RefReferences && r.From == "app/page.tsx" {
			wiring = true
		}
	}
	if !jsx {
		t.Errorf("missing JSX call ref Page → Hero: %v", res.References)
	}
	if !wiring {
		t.Errorf("missing framework wiring ref file → Page: %v", res.References)
	}
}

// TestJSXCommentedOutExcluded pins the sanitizer: commented-out JSX — both the
// {/* <Old/> */} block idiom and a // line comment — is content, not a render,
// and must never produce a reference (it would keep dead components off the
// orphans list).
func TestJSXCommentedOutExcluded(t *testing.T) {
	src := []byte(`function App() {
  return (
    <div>
      {/* <OldWidget /> */}
      {/*
        <MultiLineOld />
      */}
      {// <LineOld />
      }
      <Live />
    </div>
  );
}
`)
	syms := []extract.Symbol{{Name: "App", FQN: "App", Kind: extract.KindFunction, StartLine: 1, EndLine: 13}}
	refs := JSXRefs("x.tsx", src, syms)
	for _, dead := range []string{"OldWidget", "MultiLineOld", "LineOld"} {
		if refTo(refs, dead) != nil {
			t.Errorf("commented-out <%s/> produced a reference: %v", dead, refs)
		}
	}
	if r := refTo(refs, "Live"); r == nil || r.From != "App" {
		t.Errorf("missing live reference App → Live: %v", refs)
	}
}

// TestJSXStringContentsExcluded: "<Foo/>" inside single/double-quoted strings
// and template literals is data. JSX inside a template ${} interpolation IS
// code and keeps its reference; an apostrophe in JSX prose must not open a
// phantom string that swallows following elements.
func TestJSXStringContentsExcluded(t *testing.T) {
	src := []byte("function App() {\n" +
		"  const a = \"<StrWidget />\";\n" +
		"  const b = '<CharWidget />';\n" +
		"  const c = `<TplWidget />`;\n" +
		"  const d = html`${cond ? <InTpl/> : null}`;\n" +
		"  return <div>Don't stop <Live src=\"https://x.test/a.png\" /><SameLine /></div>;\n" +
		"}\n")
	syms := []extract.Symbol{{Name: "App", FQN: "App", Kind: extract.KindFunction, StartLine: 1, EndLine: 7}}
	refs := JSXRefs("x.tsx", src, syms)
	for _, dead := range []string{"StrWidget", "CharWidget", "TplWidget"} {
		if refTo(refs, dead) != nil {
			t.Errorf("string content <%s/> produced a reference: %v", dead, refs)
		}
	}
	for _, live := range []string{"InTpl", "Live", "SameLine"} {
		if refTo(refs, live) == nil {
			t.Errorf("missing reference App → %s: %v", live, refs)
		}
	}
}

// TestJSXUnderscoreDollarComponents: JSX treats any non-lowercase-leading tag
// as a component lookup — <_Private/> and <$Styled/> included.
func TestJSXUnderscoreDollarComponents(t *testing.T) {
	src := []byte("function App() {\n  return <div><_Private /><$Styled /></div>;\n}\n")
	syms := []extract.Symbol{{Name: "App", FQN: "App", Kind: extract.KindFunction, StartLine: 1, EndLine: 3}}
	refs := JSXRefs("x.tsx", src, syms)
	for _, want := range []string{"_Private", "$Styled"} {
		if refTo(refs, want) == nil {
			t.Errorf("missing component reference App → %s: %v", want, refs)
		}
	}
}

// TestImportsNotInComments: a commented-out import must not become an imports
// edge (the resolver would happily bind it when the target file exists).
func TestImportsNotInComments(t *testing.T) {
	src := []byte(`// import Old from './old';
/* import Older from './older'; */
import Live from './live';
`)
	imports := Imports(src)
	if len(imports) != 1 || imports[0] != "./live" {
		t.Errorf("imports = %v, want [./live]", imports)
	}
}
