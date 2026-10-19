package tsscan

import (
	"regexp"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// Temporal dispatch candidates.
//
// Temporal workflows deliberately disconnect the lexical call from the code
// that executes: a client starts a workflow by function value, a workflow
// starts children by function value/type, and activity calls go through
// proxyActivities objects. Those connections were invisible to CallRefs, so
// a workflow's flow stopped before almost every activity. The narrow shapes
// below stay textual and high-precision:
//
//   - proxyActivities is recognized only when imported from
//     @temporalio/workflow. Its type argument names the activity contract:
//     Pick<Contract, "method">, a contract type, typeof an imported activity
//     collection, or typeof import("...").
//   - Calls through a proxy object or destructured proxy local resolve to
//     that contract's named method in the contract's file.
//   - client.workflow.start and executeChild/startChild resolve the workflow
//     function passed (or named by typeof) as a call candidate.
//
// String workflow/activity names with no typed binding stay out of scope:
// they are deployment names, not a defensible symbol resolution.

type temporalTarget struct {
	spec    string
	prefix  string // contract/object FQN (or exported name under spec)
	methods map[string]bool
}

func (t temporalTarget) allows(method string) bool {
	return t.methods == nil || t.methods[method]
}

type temporalLocal struct {
	target   temporalTarget
	exported string
}

type temporalBindings struct {
	objects    map[string]temporalTarget
	locals     map[string]temporalLocal
	childFuncs map[string]bool
	proxyFuncs map[string]bool
	declRanges [][2]int // proxy declaration targets, never call sites
	hasClient  bool
}

var quotedIdentRe = regexp.MustCompile(`["']([A-Za-z_$][\w$]*)["']`)

// parseTemporalBindings recognizes proxyActivities declarations in the
// comments-blanked code view (string contents kept, so Pick<…, "method">
// selections are visible).
func parseTemporalBindings(code string, typeImports map[string]importBinding, byFQN map[string]*extract.Symbol) temporalBindings {
	out := temporalBindings{
		objects:    map[string]temporalTarget{},
		locals:     map[string]temporalLocal{},
		childFuncs: map[string]bool{},
		proxyFuncs: map[string]bool{},
	}
	proxyNames := map[string]bool{}
	for local, b := range typeImports {
		if b.spec == "@temporalio/client" {
			out.hasClient = true
		}
		if b.spec != "@temporalio/workflow" {
			continue
		}
		switch b.exported {
		case "proxyActivities":
			proxyNames[local] = true
			out.proxyFuncs[local] = true
		case "executeChild", "startChild":
			out.childFuncs[local] = true
		}
	}
	if len(proxyNames) == 0 {
		return out
	}

	for _, proxyName := range sortedKeys(proxyNames) {
		declRe := regexp.MustCompile(`\b(?:const|let|var)\s+(\{[^}]*\}|[A-Za-z_$][\w$]*)\s*=\s*(?:await\s+)?` + regexp.QuoteMeta(proxyName) + `\s*(<|\()`)
		for _, m := range declRe.FindAllStringSubmatchIndex(code, -1) {
			out.declRanges = append(out.declRanges, [2]int{m[2], m[3]})
			targetText := code[m[2]:m[3]]
			opener := code[m[4]]
			var target temporalTarget
			var ok bool
			if opener == '<' {
				content, _, good := scanAngleArgs(code, m[4])
				if !good {
					continue
				}
				target, ok = temporalTargetFromGeneric(content, typeImports, byFQN)
				if !ok {
					continue
				}
			} else {
				continue // no contract type: cannot name the activity surface
			}
			if strings.HasPrefix(targetText, "{") {
				body := strings.Trim(targetText, "{} \t\r\n")
				for _, item := range strings.Split(body, ",") {
					exported, local, good := destructureItem(item)
					if !good || !target.allows(exported) {
						continue
					}
					out.locals[local] = temporalLocal{target: target, exported: exported}
				}
				continue
			}
			if isPlainIdent(targetText) {
				out.objects[targetText] = target
			}
		}
	}
	return out
}

func (b temporalBindings) inDeclaration(pos int) bool {
	for _, r := range b.declRanges {
		if pos >= r[0] && pos < r[1] {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Insertion order is immaterial (maps keyed by local name), but a stable
	// scan keeps tests and duplicate declarations deterministic.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// scanAngleArgs returns the contents of the balanced <…> opened at lt,
// respecting string literals and nested generics.
func scanAngleArgs(s string, lt int) (string, int, bool) {
	if lt < 0 || lt >= len(s) || s[lt] != '<' {
		return "", -1, false
	}
	depth := 0
	var quote byte
	for i := lt; i < len(s) && i-lt <= 8192; i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return s[lt+1 : i], i, true
			}
		}
	}
	return "", -1, false
}

// splitTopLevelCommas splits on commas outside generics, brackets, braces,
// parens and string literals.
func splitTopLevelCommas(s string) []string {
	var out []string
	start, depth := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '<', '(', '[', '{':
			depth++
		case '>', ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

var (
	typeofImportRe = regexp.MustCompile(`^typeof\s+import\(\s*["']([^"']+)["']\s*\)`)
	typeofIdentRe  = regexp.MustCompile(`^typeof\s+([A-Za-z_$][\w$.]*)`)
	leadingIdentRe = regexp.MustCompile(`^([A-Za-z_$][\w$]*)`)
)

// temporalTargetFromGeneric resolves a proxyActivities type argument to the
// contract it names. ok=false when the argument names nothing resolvable.
func temporalTargetFromGeneric(generic string, typeImports map[string]importBinding, byFQN map[string]*extract.Symbol) (temporalTarget, bool) {
	generic = strings.TrimSpace(generic)
	lead := leadingIdentRe.FindString(generic)
	if lead == "Pick" || lead == "Omit" {
		lt := strings.IndexByte(generic, '<')
		if lt < 0 {
			return temporalTarget{}, false
		}
		inner, _, ok := scanAngleArgs(generic, lt)
		if !ok {
			return temporalTarget{}, false
		}
		parts := splitTopLevelCommas(inner)
		if len(parts) == 0 {
			return temporalTarget{}, false
		}
		target, ok := temporalTypeExprTarget(parts[0], typeImports, byFQN)
		if !ok {
			return temporalTarget{}, false
		}
		if lead == "Pick" && len(parts) > 1 {
			target.methods = map[string]bool{}
			for _, m := range quotedIdentRe.FindAllStringSubmatch(parts[1], -1) {
				target.methods[m[1]] = true
			}
			if len(target.methods) == 0 {
				return temporalTarget{}, false
			}
		}
		return target, true
	}
	return temporalTypeExprTarget(generic, typeImports, byFQN)
}

// temporalTypeExprTarget resolves one type expression: typeof import("…"),
// typeof an imported/local collection, or a contract type name.
func temporalTypeExprTarget(expr string, typeImports map[string]importBinding, byFQN map[string]*extract.Symbol) (temporalTarget, bool) {
	expr = strings.TrimSpace(expr)
	if m := typeofImportRe.FindStringSubmatch(expr); m != nil {
		return temporalTarget{spec: m[1]}, true
	}
	if m := typeofIdentRe.FindStringSubmatch(expr); m != nil {
		return temporalIdentTarget(m[1], typeImports, byFQN)
	}
	name := leadingIdentRe.FindString(expr)
	if name == "" {
		return temporalTarget{}, false
	}
	rest := strings.TrimSpace(expr[len(name):])
	member := ""
	if strings.HasPrefix(rest, ".") {
		member = leadingIdentRe.FindString(rest[1:])
	}
	target, ok := temporalIdentTarget(name, typeImports, byFQN)
	if !ok || member == "" {
		return target, ok
	}
	if target.prefix == "" {
		target.prefix = member
	} else {
		target.prefix += "." + member
	}
	return target, true
}

func temporalIdentTarget(name string, typeImports map[string]importBinding, byFQN map[string]*extract.Symbol) (temporalTarget, bool) {
	if b, ok := typeImports[name]; ok {
		switch b.kind {
		case bindNamespace:
			return temporalTarget{spec: b.spec}, true
		case bindNamed:
			return temporalTarget{spec: b.spec, prefix: b.exported}, true
		}
		return temporalTarget{}, false // default-export contract: not named enough
	}
	if sym := byFQN[name]; sym != nil {
		return temporalTarget{prefix: sym.FQN}, true
	}
	return temporalTarget{}, false
}

type temporalStart struct {
	pos  int
	expr string // identifier or root.member workflow target expression
}

var clientWorkflowStartRe = regexp.MustCompile(`\.workflow\s*\.\s*(?:startWithSignal|signalWithStart|start)\b`)

// clientWorkflowStarts finds client.workflow.start*(target, …) call sites in
// the sanitized view. Only identifier targets are returned; string workflow
// names are deployment names and stay unresolved.
func clientWorkflowStarts(text string) []temporalStart {
	var out []temporalStart
	for _, m := range clientWorkflowStartRe.FindAllStringIndex(text, -1) {
		paren, ok := openParenAfter(text, m[1])
		if !ok {
			continue
		}
		if expr, ok := parseFirstArgExpr(text, paren); ok {
			out = append(out, temporalStart{pos: m[0] + 1, expr: expr})
		}
	}
	return out
}

// parseFirstArgExpr extracts a leading identifier or root.member expression
// after the open paren at text[paren]. It returns false for literals and
// compound expressions.
func parseFirstArgExpr(text string, paren int) (string, bool) {
	i := paren + 1
	for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r') {
		i++
	}
	start := i
	for i < len(text) && (tokChar(text[i]) || text[i] == '.') {
		i++
	}
	if i == start {
		return "", false
	}
	expr := text[start:i]
	parts := strings.Split(expr, ".")
	if len(parts) > 2 {
		return "", false
	}
	for _, p := range parts {
		if !isPlainIdent(p) {
			return "", false
		}
	}
	return expr, true
}

// typeofTargetExpr extracts the target named by a `typeof X` inside a
// generic argument (sanitized view: string contents are already blank).
func typeofTargetExpr(generic string) (string, bool) {
	m := typeofIdentRe.FindStringSubmatch(strings.TrimSpace(generic))
	if m == nil {
		return "", false
	}
	parts := strings.Split(m[1], ".")
	if len(parts) > 2 {
		return "", false
	}
	for _, p := range parts {
		if !isPlainIdent(p) {
			return "", false
		}
	}
	return m[1], true
}

func destructureItem(item string) (exported, local string, ok bool) {
	item = strings.TrimSpace(item)
	if item == "" || strings.HasPrefix(item, "...") {
		return "", "", false
	}
	exported, local = item, item
	if i := strings.IndexByte(item, ':'); i > 0 {
		exported, local = strings.TrimSpace(item[:i]), strings.TrimSpace(item[i+1:])
	}
	if i := strings.IndexByte(local, '='); i > 0 {
		local = strings.TrimSpace(local[:i])
	}
	return exported, local, isPlainIdent(exported) && isPlainIdent(local)
}
