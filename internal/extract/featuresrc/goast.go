package featuresrc

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// GoOptions configures DetectGo.
type GoOptions struct {
	// RootName names a `package main` found at the project root (the module
	// or project name); other programs are named after their directory.
	RootName string
}

var (
	grpcRegisterRe = regexp.MustCompile(`^Register(\w+)Server$`)
	httpPatternRe  = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) +(/\S*)$`)
)

var httpVerbSel = map[string]string{
	"Get": "GET", "Post": "POST", "Put": "PUT", "Patch": "PATCH", "Delete": "DELETE",
	"Head": "HEAD", "Options": "OPTIONS",
	"GET": "GET", "POST": "POST", "PUT": "PUT", "PATCH": "PATCH", "DELETE": "DELETE",
	"HEAD": "HEAD", "OPTIONS": "OPTIONS",
}

var httpGenericSel = map[string]bool{"Handle": true, "HandleFunc": true, "Any": true, "Method": true, "MethodFunc": true}

type goFile struct {
	path    string
	fset    *token.FileSet
	file    *ast.File
	imports map[string]string // local name -> import path
}

type walkCtx struct {
	fn       string // enclosing function key ("" at package level)
	recvName string
	recvType string
}

func (c walkCtx) key(name string) string {
	if c.fn == "" {
		return name
	}
	return c.fn + "." + name
}

type cmdInfo struct {
	file      *goFile
	line      int
	name      string
	short     string
	long      string
	hidden    bool
	hasRun    bool
	handler   *HandlerRef
	framework string
	parentLit *ast.CompositeLit
	parent    int
}

type childRef struct {
	ident string
	call  string
	lit   *ast.CompositeLit
}

type addCall struct {
	ctx      walkCtx
	parent   string
	children []childRef
}

type retRef struct {
	fn    string
	ident string
	lit   *ast.CompositeLit
}

type goDetector struct {
	opts   GoOptions
	files  []*goFile
	consts map[string]string

	cmds     []*cmdInfo
	litIdx   map[*ast.CompositeLit]int
	pending  map[*ast.CompositeLit]string // command literal -> binding key
	elided   map[*ast.CompositeLit]string // elided slice element literal -> framework
	parentOf map[*ast.CompositeLit]*ast.CompositeLit
	binding  map[string]int
	alias    map[string]string
	rets     []retRef
	fnRet    map[string]int
	adds     []addCall

	toolDefs    map[string]ast.Expr
	groupPrefix map[string]string

	out   []Registration
	progs []Registration
	errs  []string
}

// DetectGo analyses the non-test .go files of ONE directory (package) and
// returns the registrations found plus per-file parse errors.
func DetectGo(files []SourceFile, opts GoOptions) ([]Registration, []string) {
	d := &goDetector{
		opts: opts, consts: map[string]string{},
		litIdx: map[*ast.CompositeLit]int{}, pending: map[*ast.CompositeLit]string{},
		elided: map[*ast.CompositeLit]string{}, parentOf: map[*ast.CompositeLit]*ast.CompositeLit{},
		binding: map[string]int{}, alias: map[string]string{}, fnRet: map[string]int{},
		toolDefs: map[string]ast.Expr{}, groupPrefix: map[string]string{},
	}
	sorted := append([]SourceFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	for _, sf := range sorted {
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, sf.Path, sf.Src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil || af == nil {
			d.errs = append(d.errs, fmt.Sprintf("%s: go parse failed", sf.Path))
			continue
		}
		gf := &goFile{path: sf.Path, fset: fset, file: af, imports: map[string]string{}}
		for _, imp := range af.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			name := importName(p)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			if name == "_" || name == "." {
				continue
			}
			gf.imports[name] = p
		}
		d.files = append(d.files, gf)
	}
	d.collectConsts()
	for _, f := range d.files {
		d.walkFile(f)
	}
	d.resolveTree()
	out := append(d.out, d.progs...)
	return out, d.errs
}

// importName derives a package's default local name from its import path,
// skipping major-version suffixes (".../cli/v2" -> "cli").
func importName(p string) string {
	parts := strings.Split(p, "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && regexp.MustCompile(`^v\d+$`).MatchString(last) {
		last = parts[len(parts)-2]
	}
	last = strings.TrimPrefix(last, "go-")
	return strings.ReplaceAll(last, "-", "_")
}

func (d *goDetector) collectConsts() {
	for pass := 0; pass < 2; pass++ {
		for _, f := range d.files {
			for _, decl := range f.file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Names) != len(vs.Values) {
						continue
					}
					for i, n := range vs.Names {
						if s, ok := d.evalString(vs.Values[i]); ok {
							d.consts[n.Name] = s
						}
					}
				}
			}
		}
	}
}

func (d *goDetector) evalString(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return d.evalString(x.X)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		l, ok1 := d.evalString(x.X)
		r, ok2 := d.evalString(x.Y)
		return l + r, ok1 && ok2
	case *ast.Ident:
		s, ok := d.consts[x.Name]
		return s, ok
	case *ast.CallExpr:
		return d.evalSprintf(x)
	}
	return "", false
}

var fmtVerbRe = regexp.MustCompile(`%%|%[-+# 0-9.*]*[a-zA-Z]`)

// evalSprintf renders fmt.Sprintf("literal %d", args...) descriptions: the
// format text is kept and each verb is replaced by its argument when that is a
// resolvable string constant, otherwise by an ellipsis.
func (d *goDetector) evalSprintf(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sprintf" || len(call.Args) == 0 {
		return "", false
	}
	if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "fmt" {
		return "", false
	}
	format, ok := d.evalString(call.Args[0])
	if !ok {
		return "", false
	}
	argi := 1
	return fmtVerbRe.ReplaceAllStringFunc(format, func(verb string) string {
		if verb == "%%" {
			return "%"
		}
		if argi < len(call.Args) {
			a := call.Args[argi]
			argi++
			if s, ok := d.evalString(a); ok {
				return s
			}
		}
		return "…"
	}), true
}

func fieldMap(lit *ast.CompositeLit) map[string]ast.Expr {
	m := map[string]ast.Expr{}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok {
			m[id.Name] = kv.Value
		}
	}
	return m
}

func unwrapLit(e ast.Expr) *ast.CompositeLit {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op != token.AND {
				return nil
			}
			e = x.X
		case *ast.CompositeLit:
			return x
		default:
			return nil
		}
	}
}

// cmdFramework reports the CLI framework for a composite type, "" if none.
func (f *goFile) cmdFramework(t ast.Expr) string {
	sel, ok := t.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Command" {
		return ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	switch p := f.imports[id.Name]; {
	case strings.Contains(p, "spf13/cobra"):
		return "cobra"
	case strings.Contains(p, "urfave/cli"):
		return "urfave-cli"
	}
	return ""
}

func (f *goFile) line(p token.Pos) int { return f.fset.Position(p).Line }

func (d *goDetector) walkFile(f *goFile) {
	for _, decl := range f.file.Decls {
		switch dd := decl.(type) {
		case *ast.GenDecl:
			d.walk(f, dd, walkCtx{})
		case *ast.FuncDecl:
			ctx := walkCtx{fn: dd.Name.Name}
			if dd.Recv != nil && len(dd.Recv.List) > 0 {
				r := dd.Recv.List[0]
				ctx.recvType = recvTypeName(r.Type)
				if len(r.Names) > 0 {
					ctx.recvName = r.Names[0].Name
				}
				ctx.fn = ctx.recvType + "." + dd.Name.Name
			}
			if dd.Recv == nil && dd.Name.Name == "main" && f.file.Name.Name == "main" && dd.Type.Params.NumFields() == 0 {
				d.addProgram(f, dd)
			}
			if dd.Body != nil {
				d.walk(f, dd.Body, ctx)
			}
		}
	}
}

func recvTypeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return recvTypeName(x.X)
	case *ast.IndexListExpr:
		return recvTypeName(x.X)
	}
	return ""
}

func (d *goDetector) addProgram(f *goFile, fd *ast.FuncDecl) {
	dir := path.Dir(f.path)
	label := path.Base(dir)
	if dir == "." || dir == "" {
		label = d.opts.RootName
		if label == "" {
			label = "main"
		}
	}
	reg := Registration{
		Kind: KindProgram, Label: label, Invocation: label, Framework: "go", Detector: DetectorGoAST,
		Confidence: Confirmed, File: f.path, Line: f.line(fd.Pos()),
		Handler: &HandlerRef{Name: "main", File: f.path},
	}
	if f.file.Doc != nil {
		if doc := packageDocSummary(f.file.Doc.Text()); doc != "" {
			reg.Description, reg.DescSource = doc, DescDocstring
		}
	}
	d.progs = append(d.progs, reg)
}

func (d *goDetector) walk(f *goFile, root ast.Node, ctx walkCtx) {
	var stack []ast.Node
	funcPrefix := map[*ast.FuncLit]string{}
	prefixFor := func() string {
		for i := len(stack) - 1; i >= 0; i-- {
			if fl, ok := stack[i].(*ast.FuncLit); ok {
				if p, ok := funcPrefix[fl]; ok {
					return p
				}
			}
		}
		return ""
	}
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}
		stack = append(stack, n)
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) == len(x.Rhs) {
				for i, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						d.bind(f, ctx, id.Name, x.Rhs[i], prefixFor())
					}
				}
			}
		case *ast.ValueSpec:
			if len(x.Names) == len(x.Values) {
				for i, id := range x.Names {
					d.bind(f, ctx, id.Name, x.Values[i], prefixFor())
				}
			}
		case *ast.ReturnStmt:
			for _, r := range x.Results {
				if id, ok := r.(*ast.Ident); ok {
					d.rets = append(d.rets, retRef{fn: ctx.fn, ident: id.Name})
				} else if lit := unwrapLit(r); lit != nil {
					d.rets = append(d.rets, retRef{fn: ctx.fn, lit: lit})
				}
			}
		case *ast.CompositeLit:
			d.visitLit(f, ctx, x)
		case *ast.CallExpr:
			d.visitCall(f, ctx, x, prefixFor(), funcPrefix)
		}
		return true
	})
}

// bind records name = value for command literals, constructor aliases, tool
// definitions and router group prefixes.
func (d *goDetector) bind(f *goFile, ctx walkCtx, name string, value ast.Expr, prefix string) {
	if lit := unwrapLit(value); lit != nil {
		if f.cmdFramework(lit.Type) != "" || d.isElidedCmd(lit) {
			d.pending[lit] = ctx.key(name)
		}
		if isToolLit(lit) {
			d.toolDefs[ctx.key(name)] = lit
		}
		return
	}
	call, ok := value.(*ast.CallExpr)
	if !ok {
		return
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if ctx.fn == "" && len(call.Args) == 0 {
			d.alias[name] = fun.Name
		}
	case *ast.SelectorExpr:
		switch fun.Sel.Name {
		case "NewTool":
			d.toolDefs[ctx.key(name)] = call
		case "Group", "Route", "With":
			if fun.Sel.Name == "Group" && len(call.Args) >= 1 {
				if p, ok := d.evalString(call.Args[0]); ok && strings.HasPrefix(p, "/") {
					base := prefix
					if id, ok := fun.X.(*ast.Ident); ok {
						base += d.groupPrefix[ctx.key(id.Name)]
					}
					d.groupPrefix[ctx.key(name)] = base + p
				}
			}
		}
	}
}

func (d *goDetector) isElidedCmd(lit *ast.CompositeLit) bool {
	_, ok := d.elided[lit]
	return ok
}

func isToolLit(lit *ast.CompositeLit) bool {
	switch t := lit.Type.(type) {
	case *ast.SelectorExpr:
		return t.Sel.Name == "Tool"
	case *ast.Ident:
		return t.Name == "Tool"
	}
	return false
}

func (d *goDetector) visitLit(f *goFile, ctx walkCtx, lit *ast.CompositeLit) {
	// Slices of commands: mark elided element literals.
	if at, ok := lit.Type.(*ast.ArrayType); ok {
		elt := at.Elt
		if st, ok := elt.(*ast.StarExpr); ok {
			elt = st.X
		}
		if fw := f.cmdFramework(elt); fw != "" {
			for _, e := range lit.Elts {
				if el, ok := e.(*ast.CompositeLit); ok && el.Type == nil {
					d.elided[el] = fw
				}
			}
		}
		return
	}
	// urfave App literal: acts as the (non-feature) root command so its
	// Commands become top-level commands of the binary.
	if sel, ok := lit.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "App" {
		if id, ok := sel.X.(*ast.Ident); ok && strings.Contains(f.imports[id.Name], "urfave/cli") {
			fields := fieldMap(lit)
			name, _ := d.evalString(fields["Name"])
			idx := len(d.cmds)
			d.cmds = append(d.cmds, &cmdInfo{file: f, line: f.line(lit.Pos()), name: name, framework: "urfave-cli", parent: -1})
			d.litIdx[lit] = idx
			if sub := unwrapLit(fields["Commands"]); sub != nil {
				for _, e := range sub.Elts {
					if cl := unwrapLit(e); cl != nil {
						d.parentOf[cl] = lit
					}
				}
			}
		}
		return
	}
	fw := f.cmdFramework(lit.Type)
	if fw == "" {
		fw = d.elided[lit]
	}
	if fw == "" {
		return
	}
	fields := fieldMap(lit)
	info := &cmdInfo{file: f, line: f.line(lit.Pos()), framework: fw, parent: -1, parentLit: d.parentOf[lit]}
	if fw == "cobra" {
		if use, ok := d.evalString(fields["Use"]); ok {
			if w := strings.Fields(use); len(w) > 0 {
				info.name = w[0]
			}
		}
		info.short, _ = d.evalString(fields["Short"])
		info.long, _ = d.evalString(fields["Long"])
		for _, key := range []string{"RunE", "Run"} {
			if v, ok := fields[key]; ok {
				info.hasRun = true
				if info.handler == nil {
					info.handler, _ = d.handlerOf(f, ctx, v)
				}
			}
		}
	} else {
		info.name, _ = d.evalString(fields["Name"])
		info.short, _ = d.evalString(fields["Usage"])
		info.long, _ = d.evalString(fields["Description"])
		if v, ok := fields["Action"]; ok {
			info.hasRun = true
			info.handler, _ = d.handlerOf(f, ctx, v)
		}
		for _, key := range []string{"Subcommands", "Commands"} {
			if sub := unwrapLit(fields[key]); sub != nil {
				for _, e := range sub.Elts {
					if cl := unwrapLit(e); cl != nil {
						d.parentOf[cl] = lit
					}
				}
			}
		}
	}
	if id, ok := fields["Hidden"].(*ast.Ident); ok && id.Name == "true" {
		info.hidden = true
	}
	idx := len(d.cmds)
	d.cmds = append(d.cmds, info)
	d.litIdx[lit] = idx
	if key, ok := d.pending[lit]; ok {
		d.binding[key] = idx
	}
}

// handlerOf unwraps wrappers (calls, conversions, parens) down to the first
// identifier or selector naming a function. The bool is true for inline func
// literals (handler present but anonymous).
func (d *goDetector) handlerOf(f *goFile, ctx walkCtx, e ast.Expr) (*HandlerRef, bool) {
	switch x := e.(type) {
	case nil:
		return nil, false
	case *ast.ParenExpr:
		return d.handlerOf(f, ctx, x.X)
	case *ast.FuncLit:
		return nil, true
	case *ast.Ident:
		if x.Name == "nil" || x.Name == "true" || x.Name == "false" {
			return nil, false
		}
		return &HandlerRef{Name: x.Name}, false
	case *ast.SelectorExpr:
		ref := &HandlerRef{Name: x.Sel.Name}
		if id, ok := x.X.(*ast.Ident); ok {
			switch {
			case ctx.recvName != "" && id.Name == ctx.recvName:
				ref.Recv = ctx.recvType
			case f.imports[id.Name] != "":
				ref.Qualifier = id.Name
			}
		}
		return ref, false
	case *ast.CallExpr:
		inline := false
		for _, a := range x.Args {
			h, il := d.handlerOf(f, ctx, a)
			if h != nil {
				return h, false
			}
			inline = inline || il
		}
		return nil, inline
	}
	return nil, false
}

func (d *goDetector) visitCall(f *goFile, ctx walkCtx, call *ast.CallExpr, prefix string, funcPrefix map[*ast.FuncLit]string) {
	var selName string
	var selX ast.Expr
	var identName string
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		selName, selX = fun.Sel.Name, fun.X
	case *ast.Ident:
		identName = fun.Name
	}
	name := selName
	if name == "" {
		name = identName
	}
	switch {
	case selName == "AddCommand":
		d.recordAdd(ctx, selX, call)
	case name == "AddTool":
		d.visitAddTool(f, ctx, call)
	case grpcRegisterRe.MatchString(name) && len(call.Args) == 2:
		d.visitGRPC(f, ctx, name, call)
	case selName != "":
		d.visitHTTP(f, ctx, call, selName, selX, prefix, funcPrefix)
	}
}

func (d *goDetector) recordAdd(ctx walkCtx, recv ast.Expr, call *ast.CallExpr) {
	id, ok := recv.(*ast.Ident)
	if !ok {
		return
	}
	ac := addCall{ctx: ctx, parent: id.Name}
	for _, a := range call.Args {
		switch x := a.(type) {
		case *ast.Ident:
			ac.children = append(ac.children, childRef{ident: x.Name})
		case *ast.CallExpr:
			if fn, ok := x.Fun.(*ast.Ident); ok {
				ac.children = append(ac.children, childRef{call: fn.Name})
			}
		default:
			if lit := unwrapLit(a); lit != nil {
				ac.children = append(ac.children, childRef{lit: lit})
			}
		}
	}
	d.adds = append(d.adds, ac)
}

func (d *goDetector) visitAddTool(f *goFile, ctx walkCtx, call *ast.CallExpr) {
	var toolExpr ast.Expr
	toolArg := -1
	for i, a := range call.Args {
		if lit := unwrapLit(a); lit != nil && isToolLit(lit) {
			toolExpr, toolArg = lit, i
			break
		}
		if c, ok := a.(*ast.CallExpr); ok {
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewTool" {
				toolExpr, toolArg = c, i
				break
			}
		}
		if id, ok := a.(*ast.Ident); ok {
			if def, ok := d.toolDefs[ctx.key(id.Name)]; ok {
				toolExpr, toolArg = def, i
				break
			}
		}
	}
	if toolExpr == nil {
		return
	}
	var label, desc, framework string
	framework = "mcp"
	switch t := toolExpr.(type) {
	case *ast.CompositeLit:
		fields := fieldMap(t)
		label, _ = d.evalString(fields["Name"])
		desc, _ = d.evalString(fields["Description"])
		if sel, ok := t.Type.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				framework = mcpFramework(f.imports[id.Name])
			}
		}
	case *ast.CallExpr:
		if len(t.Args) > 0 {
			label, _ = d.evalString(t.Args[0])
		}
		if sel, ok := t.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				framework = mcpFramework(f.imports[id.Name])
			}
		}
		for _, a := range t.Args[min(1, len(t.Args)):] {
			if c, ok := a.(*ast.CallExpr); ok {
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "WithDescription" && len(c.Args) > 0 {
					desc, _ = d.evalString(c.Args[0])
				}
			}
		}
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return
	}
	reg := Registration{
		Kind: KindRPCTool, Label: label, Description: summarizeDesc(desc), Framework: framework,
		Detector: DetectorGoAST, Confidence: Confirmed, File: f.path, Line: f.line(call.Pos()),
	}
	if reg.Description != "" {
		reg.DescSource = DescRegistration
	}
	for i := len(call.Args) - 1; i >= 0; i-- {
		if i == toolArg {
			continue
		}
		if h, _ := d.handlerOf(f, ctx, call.Args[i]); h != nil {
			reg.Handler = h
			break
		}
	}
	d.out = append(d.out, reg)
}

func mcpFramework(importPath string) string {
	switch {
	case strings.Contains(importPath, "mark3labs"):
		return "mcp-go"
	case strings.Contains(importPath, "modelcontextprotocol/go-sdk"):
		return "mcp-go-sdk"
	}
	return "mcp"
}

func (d *goDetector) visitGRPC(f *goFile, ctx walkCtx, fn string, call *ast.CallExpr) {
	m := grpcRegisterRe.FindStringSubmatch(fn)
	if m == nil {
		return
	}
	reg := Registration{
		Kind: KindRPCTool, Label: m[1], Framework: "grpc", Detector: DetectorGoAST,
		Confidence: Confirmed, File: f.path, Line: f.line(call.Pos()),
	}
	switch x := call.Args[1].(type) {
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok {
			switch {
			case strings.HasPrefix(id.Name, "New") && len(id.Name) > 3:
				reg.Handler = &HandlerRef{Name: id.Name[3:], IsType: true}
			case strings.HasPrefix(id.Name, "new") && len(id.Name) > 3:
				name := id.Name[3:]
				reg.Handler = &HandlerRef{Name: strings.ToLower(name[:1]) + name[1:], IsType: true}
			}
		}
	default:
		if lit := unwrapLit(x); lit != nil {
			switch t := lit.Type.(type) {
			case *ast.Ident:
				reg.Handler = &HandlerRef{Name: t.Name, IsType: true}
			case *ast.SelectorExpr:
				reg.Handler = &HandlerRef{Name: t.Sel.Name, IsType: true}
			}
		}
	}
	d.out = append(d.out, reg)
}

func (d *goDetector) visitHTTP(f *goFile, ctx walkCtx, call *ast.CallExpr, sel string, x ast.Expr, prefix string, funcPrefix map[*ast.FuncLit]string) {
	// Nested router prefixes: r.Route("/api", func(r chi.Router) { ... }).
	if (sel == "Route" || sel == "Group" || sel == "Mount") && len(call.Args) >= 2 {
		if p, ok := d.evalString(call.Args[0]); ok && strings.HasPrefix(p, "/") {
			for _, a := range call.Args[1:] {
				if fl, ok := a.(*ast.FuncLit); ok {
					base := prefix
					if id, ok := x.(*ast.Ident); ok {
						base += d.groupPrefix[ctx.key(id.Name)]
					}
					funcPrefix[fl] = base + p
				}
			}
		}
		return
	}
	verb, isVerb := httpVerbSel[sel]
	if !isVerb && !httpGenericSel[sel] {
		return
	}
	method := verb
	pathIdx := -1
	if sel == "Method" || sel == "MethodFunc" {
		if len(call.Args) < 3 {
			return
		}
		m, ok := d.evalString(call.Args[0])
		if !ok {
			return
		}
		method, pathIdx = strings.ToUpper(m), 1
	} else {
		for i, a := range call.Args {
			s, ok := d.evalString(a)
			if !ok {
				continue
			}
			if strings.HasPrefix(s, "/") || httpPatternRe.MatchString(s) {
				pathIdx = i
			}
			break // only the FIRST string argument can be the route
		}
	}
	if pathIdx < 0 || pathIdx >= len(call.Args)-1 {
		return // a route registration always carries a handler after the path
	}
	route, ok := d.evalString(call.Args[pathIdx])
	if !ok {
		return
	}
	if m := httpPatternRe.FindStringSubmatch(route); m != nil {
		method, route = m[1], m[2]
	}
	if !strings.HasPrefix(route, "/") {
		return
	}
	if method == "" {
		method = "ANY"
	}
	base := prefix
	if id, ok := x.(*ast.Ident); ok {
		base += d.groupPrefix[ctx.key(id.Name)]
	}
	full := strings.ReplaceAll(base+route, "//", "/")
	reg := Registration{
		Kind: KindHTTPRoute, Label: method + " " + full, Invocation: method + " " + full,
		Framework: "go-http", Detector: DetectorGoAST, Confidence: Confirmed, ConfirmedIfHandler: true,
		File: f.path, Line: f.line(call.Pos()),
	}
	for i := len(call.Args) - 1; i > pathIdx; i-- {
		if h, _ := d.handlerOf(f, ctx, call.Args[i]); h != nil {
			reg.Handler = h
			break
		}
	}
	d.out = append(d.out, reg)
}

// resolveCmd maps an AddCommand argument to a command index (-1 if unknown).
func (d *goDetector) resolveCmd(ctx walkCtx, name string) int {
	if idx, ok := d.binding[ctx.key(name)]; ok {
		return idx
	}
	if idx, ok := d.binding[name]; ok {
		return idx
	}
	if fn, ok := d.alias[name]; ok {
		if idx, ok := d.fnRet[fn]; ok {
			return idx
		}
	}
	return -1
}

func (d *goDetector) resolveTree() {
	for _, r := range d.rets {
		if _, done := d.fnRet[r.fn]; done || r.fn == "" {
			continue
		}
		if r.lit != nil {
			if idx, ok := d.litIdx[r.lit]; ok {
				d.fnRet[r.fn] = idx
			}
			continue
		}
		if idx, ok := d.binding[r.fn+"."+r.ident]; ok {
			d.fnRet[r.fn] = idx
		}
	}
	for _, ac := range d.adds {
		parent := d.resolveCmd(ac.ctx, ac.parent)
		if parent < 0 {
			continue
		}
		for _, ch := range ac.children {
			idx := -1
			switch {
			case ch.lit != nil:
				if i, ok := d.litIdx[ch.lit]; ok {
					idx = i
				}
			case ch.call != "":
				if i, ok := d.fnRet[ch.call]; ok {
					idx = i
				}
			default:
				idx = d.resolveCmd(ac.ctx, ch.ident)
			}
			if idx >= 0 && idx != parent && d.cmds[idx].parent < 0 {
				d.cmds[idx].parent = parent
			}
		}
	}
	litToIdx := d.litIdx
	for _, c := range d.cmds {
		if c.parent < 0 && c.parentLit != nil {
			if i, ok := litToIdx[c.parentLit]; ok {
				c.parent = i
			}
		}
	}
	// Break any parent cycle conservatively.
	for i := range d.cmds {
		seen := map[int]bool{}
		for j := i; j >= 0; j = d.cmds[j].parent {
			if seen[j] {
				d.cmds[j].parent = -1
				break
			}
			seen[j] = true
		}
	}
	hasChildren := make([]bool, len(d.cmds))
	for _, c := range d.cmds {
		if c.parent >= 0 {
			hasChildren[c.parent] = true
		}
	}
	isRoot := func(i int) bool { return d.cmds[i].parent < 0 && hasChildren[i] }
	// Program descriptions fall back to the root command's Short.
	var rootShort string
	roots := 0
	for i := range d.cmds {
		if isRoot(i) {
			roots++
			rootShort = d.cmds[i].short
		}
	}
	if roots == 1 && rootShort != "" {
		for i := range d.progs {
			d.progs[i].Description, d.progs[i].DescSource = summarizeDesc(rootShort), DescRegistration
		}
	}
	labelOf := func(i int) (label, binary string) {
		var names []string
		j := i
		for d.cmds[j].parent >= 0 {
			names = append([]string{d.cmds[j].name}, names...)
			j = d.cmds[j].parent
		}
		if isRoot(j) {
			binary = d.cmds[j].name
		} else {
			names = append([]string{d.cmds[j].name}, names...)
			binary = ""
		}
		return strings.Join(names, " "), binary
	}
	for i, c := range d.cmds {
		if c.name == "" || isRoot(i) {
			continue
		}
		if !c.hasRun && !hasChildren[i] && c.parent < 0 {
			continue // an unattached literal with nothing to run is not a feature
		}
		label, binary := labelOf(i)
		reg := Registration{
			Kind: KindCLICommand, Label: label, Framework: c.framework, Detector: DetectorGoAST,
			Confidence: Confirmed, File: c.file.path, Line: c.line, Hidden: c.hidden, Handler: c.handler,
		}
		if binary != "" {
			reg.Invocation = binary + " " + label
		}
		desc := c.short
		if desc == "" {
			desc = c.long
		}
		if desc = summarizeDesc(desc); desc != "" {
			reg.Description, reg.DescSource = desc, DescRegistration
		}
		if p := c.parent; p >= 0 && !isRoot(p) {
			pl, _ := labelOf(p)
			reg.Parent = pl
		}
		d.out = append(d.out, reg)
	}
}

var packageDocLeadRe = regexp.MustCompile(`^(?:Command|Package)\s+\S+\s+(?:is\s+|provides\s+|implements\s+|runs\s+)?`)

// packageDocSummary reduces a package doc comment to its first sentence,
// dropping the conventional "Command x is ..." lead-in.
func packageDocSummary(doc string) string {
	doc = strings.TrimSpace(doc)
	if i := strings.Index(doc, "\n\n"); i >= 0 {
		doc = doc[:i]
	}
	doc = strings.Join(strings.Fields(doc), " ")
	if i := strings.Index(doc, ". "); i >= 0 {
		doc = doc[:i+1]
	}
	if stripped := packageDocLeadRe.ReplaceAllString(doc, ""); stripped != doc && stripped != "" {
		r := []rune(stripped)
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		doc = string(r)
	}
	return summarizeTo(doc, 240)
}

// summarizeDesc keeps the first non-empty line of a registration description.
func summarizeDesc(s string) string {
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return summarizeTo(line, 400)
		}
	}
	return ""
}

func summarizeTo(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}
