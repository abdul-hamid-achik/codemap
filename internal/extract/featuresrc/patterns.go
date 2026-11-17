package featuresrc

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
)

const maxPatternLines = 20000

var (
	jsExpressRe   = regexp.MustCompile("\\b(app|router|server|api|r|routes?)\\.(get|post|put|patch|delete|all|head|options)\\(\\s*['\"`](/[^'\"`]*)['\"`]")
	jsCommandRe   = regexp.MustCompile(`\.command\(\s*['"]([\w:-]+)([^'"]*)['"]\s*(?:,\s*['"]([^'"]+)['"])?`)
	jsArgSyntaxRe = regexp.MustCompile(`^(?:\s+[<\[][^'"]*)?\s*$`)
	jsDescRe      = regexp.MustCompile(`\.description\(\s*['"]([^'"]+)['"]`)
	jsActionRe    = regexp.MustCompile(`\.action\(\s*(?:async\s+)?([A-Za-z_$][\w$.]*)\s*\)`)
	jsIdentArgRe  = regexp.MustCompile(`^[A-Za-z_$][\w$.]*$`)
	nextVerbRe    = regexp.MustCompile(`export\s+(?:async\s+)?(?:function\s+|const\s+|let\s+)(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)
	nextDefaultFn = regexp.MustCompile(`export\s+default\s+(?:async\s+)?(?:function|class)\s*\*?\s*([A-Za-z_$][\w$]*)`)
	nextDefaultID = regexp.MustCompile(`(?m)^export\s+default\s+([A-Za-z_$][\w$]*)\s*;?\s*$`)
	nextDefaultWr = regexp.MustCompile(`export\s+default\s+[A-Za-z_$][\w$.]*\(\s*([A-Za-z_$][\w$]*)\s*[,)]`)

	jsUseRe = regexp.MustCompile("\\b([A-Za-z_$][\\w$]*)\\.use\\(\\s*['\"`](/[^'\"`]*)['\"`]\\s*,\\s*(?:require\\(\\s*['\"]([^'\"]+)['\"]\\s*\\)|([A-Za-z_$][\\w$]*)\\s*[,)])")

	pyAPIRouterRe = regexp.MustCompile(`^\s*(\w+)\s*=\s*APIRouter\s*\(([^)]*)`)
	pyIncludeRe   = regexp.MustCompile(`\.include_router\(\s*(\w+)([^)]*)\)`)
	pyPrefixKw    = regexp.MustCompile(`prefix\s*=\s*['"]([^'"]*)['"]`)

	rubyRouteRe = regexp.MustCompile(`^\s*(get|post|put|patch|delete|head|options)\s+['"]([^'"]+)['"]\s+do\b`)
	rubyDescRe  = regexp.MustCompile(`^\s*desc\s+['"]([^'"]+)['"]`)
	rakeTaskRe  = regexp.MustCompile(`^\s*task\s+[:']?([\w][\w:.-]*)`)

	luaCmdRe = regexp.MustCompile(`(?:vim\.api\.)?nvim_create_user_command\(\s*['"]([\w!]+)['"]\s*,\s*(function\b|[^,\s]+)?`)

	pyRouteRe   = regexp.MustCompile(`^\s*@(\w+)\.(get|post|put|patch|delete|head|options|route|api_route)\(\s*(?:path\s*=\s*)?['"]([^'"]+)['"](.*)$`)
	pyMethodsRe = regexp.MustCompile(`methods\s*=\s*[\[(]([^\])]*)[\])]`)
	pyQuotedRe  = regexp.MustCompile(`['"]([A-Za-z]+)['"]`)
	pyCommandRe = regexp.MustCompile(`^\s*@(?:\w+\.)?command\b(?:\(([^)]*)\))?`)
	pyDefRe     = regexp.MustCompile(`^\s*(?:async\s+)?def\s+(\w+)`)
	pyMainRe    = regexp.MustCompile(`^if\s+__name__\s*==\s*['"]__main__['"]\s*:`)
	pyCallRe    = regexp.MustCompile(`^\s+(?:sys\.exit\(\s*)?(?:asyncio\.run\(\s*)?(\w+)\(`)
	djangoRe    = regexp.MustCompile(`\bpath\(\s*['"]([^'"]*)['"]\s*,\s*([\w.]+)`)
	pyStringArg = regexp.MustCompile(`^\s*['"]([^'"]+)['"]`)
	pyNameKw    = regexp.MustCompile(`name\s*=\s*['"]([^'"]+)['"]`)
)

// IsIgnoredPath reports paths whose contents are never product features:
// dependency trees, generated output and test fixtures.
func IsIgnoredPath(p string) bool {
	for _, seg := range strings.Split(strings.ReplaceAll(p, `\`, "/"), "/") {
		switch seg {
		case "node_modules", "vendor", "testdata", "fixtures", "__fixtures__", "__tests__", "__mocks__",
			".venv", "venv", "site-packages", "dist", "build", ".next", ".git":
			return true
		}
	}
	return false
}

func splitLines(src []byte) []string {
	lines := strings.Split(string(src), "\n")
	if len(lines) > maxPatternLines {
		lines = lines[:maxPatternLines]
	}
	return lines
}

func isCommentLine(l string) bool {
	t := strings.TrimSpace(l)
	return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "#")
}

// DetectPattern runs the regex detectors appropriate for the file extension
// (TS/JS, Python, Ruby, Lua). All results are candidates; GDScript is
// recognized but yields no claims (Godot lifecycle hooks such as _ready are
// not features and no honest detector exists for tool scripts).
func DetectPattern(rel string, src []byte) []Registration {
	base := strings.ToLower(path.Base(rel))
	if base == "rakefile" {
		return detectRuby(rel, src)
	}
	switch strings.ToLower(path.Ext(rel)) {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts":
		return detectJS(rel, src)
	case ".py":
		return detectPython(rel, src)
	case ".rb", ".rake":
		return detectRuby(rel, src)
	case ".lua":
		return detectLua(rel, src)
	case ".gd":
		return detectGDScript(rel, src)
	}
	return nil
}

func jsLeadingComment(lines []string, i int) string {
	if i <= 0 {
		return ""
	}
	prev := strings.TrimSpace(lines[i-1])
	if strings.HasPrefix(prev, "//") {
		return summarize(strings.TrimLeft(strings.TrimPrefix(prev, "//"), "/ "))
	}
	return ""
}

func detectJS(rel string, src []byte) []Registration {
	if !hasAny(src, "get(", "post(", "put(", "patch(", "delete(", "all(", "head(", "options(", ".command(") {
		return nil
	}
	lines := splitLines(src)
	mounts := sameFileMounts(lines)
	var out []Registration
	for i, l := range lines {
		if isCommentLine(l) {
			continue
		}
		if m := jsExpressRe.FindStringSubmatchIndex(l); m != nil {
			verb := strings.ToUpper(l[m[4]:m[5]])
			route := joinRoute(mounts[l[m[2]:m[3]]], l[m[6]:m[7]])
			if verb == "ALL" {
				verb = "ANY"
			}
			rest := l[m[1]:]
			if !handlerFollows(rest) {
				continue
			}
			reg := Registration{
				Kind: KindHTTPRoute, Label: verb + " " + route, Invocation: verb + " " + route,
				Framework: "express", Detector: DetectorPattern, Confidence: Candidate,
				File: rel, Line: i + 1, Handler: jsHandlerFromRest(rest),
			}
			if c := jsLeadingComment(lines, i); c != "" {
				reg.Description, reg.DescSource = c, DescDocstring
			}
			out = append(out, reg)
			continue
		}
		if m := jsCommandRe.FindStringSubmatch(l); m != nil && jsArgSyntaxRe.MatchString(m[2]) {
			reg := Registration{
				Kind: KindCLICommand, Label: m[1], Framework: "commander", Detector: DetectorPattern,
				Confidence: Candidate, File: rel, Line: i + 1,
			}
			// A bare `.command('x')` also appears in UI/help text and unrelated
			// APIs; require a registration companion (description, action, or
			// yargs' description argument) before reporting it.
			evidence := false
			if m[3] != "" {
				reg.Description, reg.DescSource = summarize(m[3]), DescRegistration
				reg.Framework = "yargs"
				evidence = true
			}
			for j := i; j < len(lines) && j <= i+8; j++ {
				if j > i && jsCommandRe.MatchString(lines[j]) {
					break
				}
				if strings.Contains(lines[j], ".action(") {
					evidence = true
				}
				if reg.Description == "" && j <= i+5 {
					if dm := jsDescRe.FindStringSubmatch(lines[j]); dm != nil {
						reg.Description, reg.DescSource = summarize(dm[1]), DescRegistration
						evidence = true
					}
				}
				if reg.Handler == nil {
					if am := jsActionRe.FindStringSubmatch(lines[j]); am != nil {
						reg.Handler = &HandlerRef{Name: lastDotted(am[1])}
					}
				}
			}
			if evidence {
				out = append(out, reg)
			}
		}
	}
	return out
}

// joinRoute concatenates a mount prefix and a route into a clean path.
func joinRoute(prefix, route string) string {
	full := strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(route, "/")
	full = strings.ReplaceAll(full, "//", "/")
	if full == "" {
		full = "/"
	}
	return full
}

// sameFileMounts returns router variable -> mount prefix for `x.use('/p', y)`
// chains within one file, resolved to a fixpoint (app.use('/a', r1);
// r1.use('/b', r2)). A variable mounted under two different prefixes is
// dropped (no claim).
func sameFileMounts(lines []string) map[string]string {
	type mount struct{ recv, tgt, p string }
	var ms []mount
	for _, l := range lines {
		if isCommentLine(l) {
			continue
		}
		for _, m := range jsUseRe.FindAllStringSubmatch(l, -1) {
			if m[4] != "" { // plain variable target; require(...) is cross-file
				ms = append(ms, mount{recv: m[1], tgt: m[4], p: m[2]})
			}
		}
	}
	pref := map[string]string{}
	drop := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, m := range ms {
			if drop[m.tgt] {
				continue
			}
			np := joinRoute(pref[m.recv], m.p)
			if old, ok := pref[m.tgt]; ok {
				if old != np {
					delete(pref, m.tgt)
					drop[m.tgt] = true
					changed = true
				}
				continue
			}
			pref[m.tgt] = np
			changed = true
		}
	}
	return pref
}

// DetectJSSet runs the JS/TS pattern detectors over a batch of files and
// resolves Express router mounts across files: `app.use('/prefix',
// require('./mod'))` prefixes every express route registered in the required
// module. Same-file mounts (`app.use('/prefix', router)`) are resolved by
// DetectPattern itself. Modules mounted under several different prefixes keep
// their router-relative path (no claim).
func DetectJSSet(files []SourceFile) []Registration {
	sorted := append([]SourceFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	byPath := map[string]SourceFile{}
	for _, sf := range sorted {
		byPath[sf.Path] = sf
	}
	// Cross-file require mounts: target file -> prefix.
	target := map[string]string{}
	drop := map[string]bool{}
	for _, sf := range sorted {
		for _, l := range splitLines(sf.Src) {
			if isCommentLine(l) {
				continue
			}
			for _, m := range jsUseRe.FindAllStringSubmatch(l, -1) {
				if m[3] == "" {
					continue
				}
				t := resolveRequire(sf.Path, m[3], byPath)
				if t == "" || drop[t] {
					continue
				}
				if old, ok := target[t]; ok {
					if old != m[2] {
						delete(target, t)
						drop[t] = true
					}
					continue
				}
				target[t] = m[2]
			}
		}
	}
	var out []Registration
	for _, sf := range sorted {
		regs := DetectPattern(sf.Path, sf.Src)
		if p, ok := target[sf.Path]; ok {
			for i := range regs {
				r := &regs[i]
				if r.Framework != "express" || r.Kind != KindHTTPRoute {
					continue
				}
				verb, route, _ := strings.Cut(r.Label, " ")
				route = joinRoute(p, route)
				r.Label, r.Invocation = verb+" "+route, verb+" "+route
			}
		}
		out = append(out, regs...)
	}
	return out
}

// resolveRequire resolves a relative require() path against the importing
// file and the set of scanned files; "" when nothing or several files match.
func resolveRequire(from, req string, files map[string]SourceFile) string {
	if !strings.HasPrefix(req, "./") && !strings.HasPrefix(req, "../") {
		return ""
	}
	base := strings.TrimSuffix(path.Join(path.Dir(from), req), "/")
	if _, ok := files[base]; ok {
		return base
	}
	var hits []string
	for _, ext := range []string{".js", ".ts", ".tsx", ".jsx", ".mjs", ".cjs", ".mts", ".cts"} {
		for _, cand := range []string{base + ext, path.Join(base, "index"+ext)} {
			if _, ok := files[cand]; ok {
				hits = append(hits, cand)
			}
		}
	}
	if len(hits) != 1 {
		return ""
	}
	return hits[0]
}

func hasAny(src []byte, subs ...string) bool {
	s := string(src)
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func lastDotted(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// handlerFollows reports whether the text after a route path plausibly
// carries a handler argument (and is not, say, an HTTP client call with an
// options object or no further arguments).
func handlerFollows(rest string) bool {
	t := strings.TrimSpace(rest)
	if !strings.HasPrefix(t, ",") {
		return false
	}
	t = strings.TrimSpace(t[1:])
	if t == "" {
		return true // handler continues on the next line
	}
	switch t[0] {
	case '{', '[', '\'', '"', '`', ')':
		return false
	}
	if t[0] >= '0' && t[0] <= '9' {
		return false
	}
	return true
}

func jsHandlerFromRest(rest string) *HandlerRef {
	if strings.Contains(rest, "=>") || strings.Contains(rest, "function") {
		return nil
	}
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ","))
	t = strings.TrimRight(t, " ;")
	t = strings.TrimSuffix(t, ")")
	var last string
	for _, part := range strings.Split(t, ",") {
		part = strings.TrimSpace(part)
		if jsIdentArgRe.MatchString(part) {
			last = part
		}
	}
	if last == "" {
		return nil
	}
	return &HandlerRef{Name: lastDotted(last)}
}

func pyDocstring(lines []string, defIdx int) string {
	end := -1
	for j := defIdx; j < len(lines) && j <= defIdx+8; j++ {
		if strings.HasSuffix(strings.TrimSpace(lines[j]), ":") {
			end = j
			break
		}
	}
	if end < 0 {
		return ""
	}
	for k := end + 1; k < len(lines) && k <= end+3; k++ {
		if strings.TrimSpace(lines[k]) == "" {
			continue
		}
		if doc, ok := pyStringLine(lines[k]); ok {
			return doc
		}
		return ""
	}
	return ""
}

func pyStringLine(l string) (string, bool) {
	t := strings.TrimSpace(l)
	for _, q := range []string{`"""`, `'''`, `r"""`, `"`, `'`} {
		if strings.HasPrefix(t, q) {
			body := strings.TrimPrefix(t, q)
			body = strings.TrimSpace(strings.TrimSuffix(body, strings.TrimPrefix(q, "r")))
			return summarize(body), true
		}
	}
	return "", false
}

func nextDef(lines []string, from int) (string, int) {
	for j := from; j < len(lines) && j <= from+5; j++ {
		if m := pyDefRe.FindStringSubmatch(lines[j]); m != nil {
			return m[1], j
		}
		if t := strings.TrimSpace(lines[j]); strings.HasPrefix(t, "class ") {
			break
		}
	}
	return "", -1
}

func detectPython(rel string, src []byte) []Registration {
	text := string(src)
	isURLs := path.Base(rel) == "urls.py"
	if !strings.Contains(text, "@") && !strings.Contains(text, "__main__") &&
		!strings.Contains(text, "APIRouter") && !strings.Contains(text, "include_router") && !isURLs {
		return nil
	}
	lines := splitLines(src)
	routerPrefix := pyRouterPrefix(lines)
	var out []Registration
	for i, l := range lines {
		if m := pyRouteRe.FindStringSubmatch(l); m != nil {
			fn, defIdx := nextDef(lines, i+1)
			route := m[3]
			if !strings.HasPrefix(route, "/") {
				route = "/" + route
			}
			if p, ok := routerPrefix[m[1]]; ok {
				route = joinRoute(p, route)
			}
			methods := []string{strings.ToUpper(m[2])}
			if m[2] == "route" || m[2] == "api_route" {
				methods = []string{"ANY"}
				if mm := pyMethodsRe.FindStringSubmatch(m[4]); mm != nil {
					var ms []string
					for _, q := range pyQuotedRe.FindAllStringSubmatch(mm[1], -1) {
						ms = append(ms, strings.ToUpper(q[1]))
					}
					if len(ms) > 0 {
						methods = ms
					}
				}
			}
			for _, method := range methods {
				reg := Registration{
					Kind: KindHTTPRoute, Label: method + " " + route, Invocation: method + " " + route,
					Framework: "python-http", Detector: DetectorPattern, Confidence: Candidate,
					File: rel, Line: i + 1,
				}
				if fn != "" {
					reg.Handler = &HandlerRef{Name: fn, SameFileOnly: true}
					if doc := pyDocstring(lines, defIdx); doc != "" {
						reg.Description, reg.DescSource = doc, DescDocstring
					}
				}
				out = append(out, reg)
			}
			continue
		}
		if m := pyCommandRe.FindStringSubmatch(l); m != nil {
			fn, defIdx := nextDef(lines, i+1)
			if fn == "" {
				continue
			}
			label := strings.ReplaceAll(fn, "_", "-")
			if sm := pyStringArg.FindStringSubmatch(m[1]); sm != nil {
				label = sm[1]
			} else if nm := pyNameKw.FindStringSubmatch(m[1]); nm != nil {
				label = nm[1]
			}
			reg := Registration{
				Kind: KindCLICommand, Label: label, Framework: "python-cli", Detector: DetectorPattern,
				Confidence: Candidate, File: rel, Line: i + 1, Handler: &HandlerRef{Name: fn, SameFileOnly: true},
			}
			if doc := pyDocstring(lines, defIdx); doc != "" {
				reg.Description, reg.DescSource = doc, DescDocstring
			}
			out = append(out, reg)
			continue
		}
		if isURLs {
			if m := djangoRe.FindStringSubmatch(l); m != nil && !strings.HasPrefix(strings.TrimSpace(l), "#") {
				handler := strings.TrimSuffix(m[2], ".as_view")
				if handler == "include" || handler == "" {
					continue
				}
				route := "/" + strings.TrimPrefix(m[1], "/")
				out = append(out, Registration{
					Kind: KindHTTPRoute, Label: "ANY " + route, Invocation: "ANY " + route,
					Framework: "django", Detector: DetectorPattern, Confidence: Candidate,
					File: rel, Line: i + 1, Handler: &HandlerRef{Name: lastDotted(handler)},
				})
			}
			continue
		}
		if pyMainRe.MatchString(l) {
			label := strings.TrimSuffix(path.Base(rel), ".py")
			if label == "__main__" {
				label = path.Base(path.Dir(rel))
				if label == "." || label == "" {
					label = "__main__"
				}
			}
			reg := Registration{
				Kind: KindProgram, Label: label, Invocation: label, Framework: "python",
				Detector: DetectorPattern, Confidence: Candidate, File: rel, Line: i + 1,
			}
			for j := i + 1; j < len(lines) && j <= i+6; j++ {
				if cm := pyCallRe.FindStringSubmatch(lines[j]); cm != nil {
					reg.Handler = &HandlerRef{Name: cm[1]}
					break
				}
			}
			if doc := pyModuleDoc(lines); doc != "" {
				reg.Description, reg.DescSource = doc, DescDocstring
			}
			out = append(out, reg)
		}
	}
	return out
}

// pyRouterPrefix maps router variable -> path prefix from APIRouter(prefix=)
// declarations and include_router(router, prefix=) calls within one file.
func pyRouterPrefix(lines []string) map[string]string {
	pref := map[string]string{}
	for _, l := range lines {
		if isCommentLine(l) {
			continue
		}
		if m := pyAPIRouterRe.FindStringSubmatch(l); m != nil {
			if pm := pyPrefixKw.FindStringSubmatch(m[2]); pm != nil && pm[1] != "" {
				pref[m[1]] = "/" + strings.Trim(pm[1], "/")
			}
			continue
		}
		if m := pyIncludeRe.FindStringSubmatch(l); m != nil {
			p := pref[m[1]]
			if pm := pyPrefixKw.FindStringSubmatch(m[2]); pm != nil && pm[1] != "" {
				p = joinRoute(p, "/"+strings.Trim(pm[1], "/"))
			}
			if p != "" {
				pref[m[1]] = p
			}
		}
	}
	return pref
}

// detectRuby detects Sinatra-style HTTP routes (`get '/x' do ... end`) in any
// Ruby file and rake tasks (`task :name`) in Rakefiles and *.rake. Handlers
// are blocks, so no handler reference is claimed. Rails route files and
// gem-internal DSLs are deliberately not detected.
func detectRuby(rel string, src []byte) []Registration {
	base := path.Base(rel)
	isRake := base == "Rakefile" || strings.HasSuffix(rel, ".rake")
	if !isRake && !hasAny(src, " do\n", " do ") {
		return nil
	}
	lines := splitLines(src)
	var out []Registration
	lastDesc, lastDescLine := "", -10
	for i, l := range lines {
		if isCommentLine(l) {
			continue
		}
		if m := rubyRouteRe.FindStringSubmatch(l); m != nil {
			route := m[2]
			if !strings.HasPrefix(route, "/") {
				route = "/" + route
			}
			verb := strings.ToUpper(m[1])
			reg := Registration{
				Kind: KindHTTPRoute, Label: verb + " " + route, Invocation: verb + " " + route,
				Framework: "sinatra", Detector: DetectorPattern, Confidence: Candidate,
				File: rel, Line: i + 1,
			}
			if c := rubyComment(lines, i); c != "" {
				reg.Description, reg.DescSource = c, DescDocstring
			}
			out = append(out, reg)
			continue
		}
		if isRake {
			if m := rubyDescRe.FindStringSubmatch(l); m != nil {
				lastDesc, lastDescLine = summarize(m[1]), i
				continue
			}
			if m := rakeTaskRe.FindStringSubmatch(l); m != nil {
				reg := Registration{
					Kind: KindCLICommand, Label: m[1], Framework: "rake",
					Detector: DetectorPattern, Confidence: Candidate, File: rel, Line: i + 1,
				}
				if lastDescLine >= i-3 && lastDesc != "" {
					reg.Description, reg.DescSource = lastDesc, DescRegistration
				}
				out = append(out, reg)
			}
		}
	}
	return out
}

func rubyComment(lines []string, i int) string {
	if i <= 0 {
		return ""
	}
	prev := strings.TrimSpace(lines[i-1])
	if strings.HasPrefix(prev, "#") {
		return summarize(strings.TrimPrefix(prev, "#"))
	}
	return ""
}

// detectLua detects Neovim user commands (nvim_create_user_command('Name',
// ...)), the dominant Lua command surface in indexed repos. OpenResty content
// by phase is configuration, not commands, and is deliberately not claimed.
func detectLua(rel string, src []byte) []Registration {
	if !hasAny(src, "nvim_create_user_command") {
		return nil
	}
	lines := splitLines(src)
	var out []Registration
	for i, l := range lines {
		if isCommentLine(l) {
			continue
		}
		for _, m := range luaCmdRe.FindAllStringSubmatch(l, -1) {
			reg := Registration{
				Kind: KindCLICommand, Label: m[1], Framework: "neovim",
				Detector: DetectorPattern, Confidence: Candidate, File: rel, Line: i + 1,
			}
			if m[2] != "" && m[2] != "function" {
				reg.Handler = &HandlerRef{Name: m[2], SameFileOnly: true}
			}
			out = append(out, reg)
		}
	}
	return out
}

// detectGDScript is a recognized-but-no-claims detector: Godot lifecycle
// hooks (_ready, _process) are not user-facing features and there is no
// honest syntactic signal for exported tool scripts or scene entrypoints.
func detectGDScript(rel string, src []byte) []Registration {
	return nil
}

func pyModuleDoc(lines []string) string {
	for i, l := range lines {
		if i > 25 {
			break
		}
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if doc, ok := pyStringLine(l); ok {
			if doc == "" && i+1 < len(lines) {
				return summarize(lines[i+1])
			}
			return doc
		}
		return ""
	}
	return ""
}

// DetectNextPath derives Next.js page / API route features from a file's path
// (app/**/page.*, app/**/route.*, pages/**). The caller must have established
// that the file lives in a Next.js package.
func DetectNextPath(rel string, src []byte) []Registration {
	rel = strings.ReplaceAll(rel, `\`, "/")
	ext := strings.ToLower(path.Ext(rel))
	switch ext {
	case ".tsx", ".jsx", ".ts", ".js":
	default:
		return nil
	}
	if strings.HasSuffix(rel, ".d.ts") {
		return nil
	}
	segs := strings.Split(rel, "/")
	routerIdx, router := -1, ""
	for i, s := range segs[:len(segs)-1] {
		if s == "app" || s == "pages" {
			routerIdx, router = i, s
			break
		}
	}
	if routerIdx < 0 {
		return nil
	}
	stem := strings.TrimSuffix(segs[len(segs)-1], ext)
	dirs := segs[routerIdx+1 : len(segs)-1]
	if router == "app" {
		route := nextRouteFromSegments(dirs)
		switch stem {
		case "page":
			return []Registration{nextReg(KindPage, route, "", rel, src)}
		case "route":
			var out []Registration
			seen := map[string]bool{}
			for _, m := range nextVerbRe.FindAllStringSubmatch(string(src), -1) {
				if seen[m[1]] {
					continue
				}
				seen[m[1]] = true
				reg := nextReg(KindAPIRoute, route, m[1], rel, src)
				reg.Handler = &HandlerRef{Name: m[1], SameFileOnly: true}
				out = append(out, reg)
			}
			sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
			if len(out) == 0 {
				reg := nextReg(KindAPIRoute, route, "ANY", rel, src)
				reg.Confidence = Candidate
				out = append(out, reg)
			}
			return out
		}
		return nil
	}
	// pages router
	if strings.HasPrefix(stem, "_") {
		return nil
	}
	if stem != "index" {
		dirs = append(append([]string(nil), dirs...), stem)
	}
	route := nextRouteFromSegments(dirs)
	if len(dirs) > 0 && dirs[0] == "api" {
		return []Registration{nextReg(KindAPIRoute, route, "ANY", rel, src)}
	}
	return []Registration{nextReg(KindPage, route, "", rel, src)}
}

func nextRouteFromSegments(segs []string) string {
	var keep []string
	for _, s := range segs {
		if s == "" || strings.HasPrefix(s, "(") || strings.HasPrefix(s, "@") {
			continue // route groups, intercepting routes and parallel slots add no URL segment
		}
		keep = append(keep, s)
	}
	return "/" + strings.Join(keep, "/")
}

func nextReg(kind, route, verb, rel string, src []byte) Registration {
	reg := Registration{
		Kind: kind, Label: route, Framework: "nextjs", Detector: DetectorPath, Confidence: Confirmed,
		File: rel, Line: 1,
	}
	if verb != "" {
		reg.Label = verb + " " + route
		reg.Invocation = reg.Label
	}
	if kind == KindPage {
		reg.Handler = nextDefaultHandler(src)
	} else if verb == "ANY" {
		reg.Handler = nextDefaultHandler(src)
	}
	if verb == "" || verb == "ANY" || reg.Handler != nil {
		if c := leadingComment(src); c != "" {
			reg.Description, reg.DescSource = c, DescDocstring
		}
	}
	return reg
}

func nextDefaultHandler(src []byte) *HandlerRef {
	s := string(src)
	if m := nextDefaultFn.FindStringSubmatch(s); m != nil {
		return &HandlerRef{Name: m[1], SameFileOnly: true}
	}
	if m := nextDefaultWr.FindStringSubmatch(s); m != nil {
		return &HandlerRef{Name: m[1], SameFileOnly: true}
	}
	if m := nextDefaultID.FindStringSubmatch(s); m != nil {
		switch m[1] {
		case "async", "function", "class":
		default:
			return &HandlerRef{Name: m[1], SameFileOnly: true}
		}
	}
	return nil
}

// leadingComment returns the first line of a file-leading comment block.
func leadingComment(src []byte) string {
	head := src
	if len(head) > 4096 {
		head = head[:4096]
	}
	for _, l := range strings.Split(string(head), "\n") {
		t := strings.TrimSpace(l)
		switch {
		case t == "", strings.HasPrefix(t, "#!"), strings.HasPrefix(t, `"use `), strings.HasPrefix(t, `'use `):
			continue
		case strings.HasPrefix(t, "/**"), strings.HasPrefix(t, "/*"):
			t = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "/**"), "/*"))
			t = strings.TrimSpace(strings.TrimSuffix(t, "*/"))
			if t != "" {
				return summarize(t)
			}
		case strings.HasPrefix(t, "*"):
			t = strings.TrimSpace(strings.TrimPrefix(t, "*"))
			if t != "" && !strings.HasPrefix(t, "@") && !strings.HasPrefix(t, "/") {
				return summarize(t)
			}
		case strings.HasPrefix(t, "//"):
			return summarize(strings.TrimLeft(t, "/ "))
		default:
			return ""
		}
	}
	return ""
}

// DetectPackageBin turns a package.json `bin` field into program features.
func DetectPackageBin(rel string, src []byte) []Registration {
	var pkg struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Bin         json.RawMessage `json:"bin"`
	}
	if json.Unmarshal(src, &pkg) != nil || len(pkg.Bin) == 0 {
		return nil
	}
	line := 1
	if i := strings.Index(string(src), `"bin"`); i >= 0 {
		line = 1 + strings.Count(string(src[:i]), "\n")
	}
	pkgName := pkg.Name
	if i := strings.LastIndex(pkgName, "/"); i >= 0 {
		pkgName = pkgName[i+1:]
	}
	names := map[string]string{}
	var single string
	if json.Unmarshal(pkg.Bin, &single) == nil && single != "" {
		if pkgName == "" {
			pkgName = path.Base(path.Dir(rel))
		}
		names[pkgName] = single
	} else {
		var obj map[string]string
		if json.Unmarshal(pkg.Bin, &obj) == nil {
			for k, v := range obj {
				names[k] = v
			}
		}
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Registration
	for _, k := range keys {
		reg := Registration{
			Kind: KindProgram, Label: k, Invocation: k, Framework: "node", Detector: DetectorPath,
			Confidence: Confirmed, File: rel, Line: line,
		}
		if pkg.Description != "" {
			reg.Description, reg.DescSource = summarize(pkg.Description), DescRegistration
		}
		out = append(out, reg)
	}
	return out
}
