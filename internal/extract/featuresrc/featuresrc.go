// Package featuresrc detects a repository's user-facing entry surfaces (CLI
// commands, HTTP routes, RPC/MCP tools, UI pages, programs) from source text.
//
// Detectors are pure functions: input is a project-relative path plus source
// bytes, output is a list of Registrations. Resolving a registration's handler
// to a graph node, computing footprints and shaping the report is the job of
// internal/app (dependency direction: app -> featuresrc).
package featuresrc

import (
	"sort"
	"strings"
)

// Feature kinds (stable enum).
const (
	KindCLICommand = "cli_command"
	KindHTTPRoute  = "http_route"
	KindRPCTool    = "rpc_tool"
	KindPage       = "page"
	KindAPIRoute   = "api_route"
	KindProgram    = "program"
)

// Detectors (how a registration was found).
const (
	DetectorGoAST   = "go_ast"
	DetectorPattern = "pattern"
	DetectorPath    = "path"
)

// Confidence values.
const (
	Confirmed = "confirmed"
	Candidate = "candidate"
)

// Description sources.
const (
	DescRegistration = "registration"
	DescDocstring    = "docstring"
)

// KindOrder is the canonical report ordering of kinds.
var KindOrder = []string{KindProgram, KindCLICommand, KindRPCTool, KindHTTPRoute, KindAPIRoute, KindPage}

// Surface returns the human surface label of a kind.
func Surface(kind string) string {
	switch kind {
	case KindCLICommand:
		return "CLI"
	case KindHTTPRoute, KindAPIRoute:
		return "HTTP"
	case KindRPCTool:
		return "RPC/MCP"
	case KindPage:
		return "UI"
	case KindProgram:
		return "Program"
	}
	return ""
}

// KindRank returns the sort position of a kind (unknown kinds sort last).
func KindRank(kind string) int {
	for i, k := range KindOrder {
		if k == kind {
			return i
		}
	}
	return len(KindOrder)
}

// HandlerRef is a syntactic reference to the function that implements a
// feature. It is deliberately unresolved: the app layer maps it onto graph
// nodes (same file first, then same package/directory, then project-unique).
type HandlerRef struct {
	// Name is the bare function or method name (for a gRPC impl: the type name).
	Name string
	// Recv is the receiver type when the reference is x.Name and x is the
	// receiver of the enclosing method (so it names a method of Recv).
	Recv string
	// Qualifier is the package qualifier of pkg.Name references.
	Qualifier string
	// IsType marks a gRPC implementation type (Name is a type name).
	IsType bool
	// SameFileOnly restricts resolution to the registration file (framework
	// conventions such as Next.js default exports and route verbs live in the
	// file itself; a same-named function elsewhere is never the handler).
	SameFileOnly bool
	// File is an optional override of where Name is looked up first (defaults
	// to the registration file).
	File string
}

// Registration is one detected feature registration.
type Registration struct {
	Kind        string
	Label       string
	Invocation  string
	Description string
	DescSource  string
	Framework   string
	Detector    string
	Confidence  string
	File        string
	Line        int
	Hidden      bool
	// Parent is the label of the parent cli_command, "" for top level.
	Parent  string
	Handler *HandlerRef
	// ConfirmedIfHandler downgrades Confidence to candidate when the app layer
	// cannot resolve Handler (used for syntactic matches that can be junk).
	ConfirmedIfHandler bool
}

// SourceFile is one source file handed to a detector.
type SourceFile struct {
	Path string // project-relative, forward slashes
	Src  []byte
}

// SortRegistrations orders registrations deterministically: kind order, label,
// file, line.
func SortRegistrations(regs []Registration) {
	sort.SliceStable(regs, func(i, j int) bool {
		a, b := regs[i], regs[j]
		if ra, rb := KindRank(a.Kind), KindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
}

// firstSentence trims text to its first line, capped for readable summaries.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// summarize returns the first sentence-ish line of free text.
func summarize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	line := firstLine(s)
	if len(line) > 240 {
		line = line[:240]
	}
	return line
}

// IsTypeHandler reports whether the handler reference names a type (gRPC impl).
func (r Registration) IsTypeHandler() bool { return r.Handler != nil && r.Handler.IsType }
