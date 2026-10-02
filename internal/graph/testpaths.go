package graph

import (
	"path/filepath"
	"strings"
)

// IsTestFilePath reports whether a project-relative path looks like a test file by
// the common conventions across codemap's languages (Go _test.go; JS/TS
// .test/.spec; Python test_*.py / *_test.py; Ruby/Lua _spec / _test).
func IsTestFilePath(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") {
		return true
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	for _, suf := range []string{"_spec.rb", "_test.rb", "_spec.lua", "_test.lua"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	for _, ext := range []string{"ts", "tsx", "js", "jsx", "mjs", "cjs"} {
		if strings.HasSuffix(base, ".test."+ext) || strings.HasSuffix(base, ".spec."+ext) {
			return true
		}
	}
	return false
}

// testSupportDirs lists directory names whose whole subtree is test-only
// (fixtures, mocks, test suites), so symbols defined there are not product code.
var testSupportDirs = map[string]bool{
	"testdata": true, "__tests__": true, "__mocks__": true, "test": true, "tests": true,
}

// IsTestPath reports whether a project-relative path is test code: a test file
// (IsTestFilePath) or anything under a test-only directory (testdata, __tests__,
// __mocks__, test, tests). Importance rankings (read-order, hotspots, map) use it
// to keep tests and their mocks from outranking product code.
func IsTestPath(p string) bool {
	if IsTestFilePath(p) {
		return true
	}
	clean := filepath.ToSlash(p)
	dir := filepath.ToSlash(filepath.Dir(clean))
	if dir == "." || dir == "" {
		return false
	}
	for _, seg := range strings.Split(dir, "/") {
		if testSupportDirs[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

// IsTestNode reports whether a node is test code: a test node (KindTest) or any
// node defined in a test path.
func IsTestNode(n Node) bool {
	return n.Kind == KindTest || IsTestPath(n.FilePath)
}
