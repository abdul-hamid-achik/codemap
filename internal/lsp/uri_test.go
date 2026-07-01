package lsp

import (
	"path/filepath"
	"testing"
)

func TestPathToURI(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/tmp/x.go", "file:///tmp/x.go"},
		{"/Users/a/My Projects/x.go", "file:///Users/a/My%20Projects/x.go"},
		{"/Users/a/naïve/x.go", "file:///Users/a/na%C3%AFve/x.go"},
	}
	for _, tc := range cases {
		got, err := PathToURI(tc.in)
		if err != nil {
			t.Fatalf("PathToURI(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("PathToURI(%q) = %q, want %q", tc.in, got, tc.want)
		}
		// Round-trip
		back, err := PathFromURI(got)
		if err != nil {
			t.Fatalf("PathFromURI(%q): %v", got, err)
		}
		absIn, _ := filepath.Abs(tc.in)
		if filepath.ToSlash(back) != filepath.ToSlash(absIn) {
			t.Errorf("round-trip: %q -> %q -> %q (want %q)", tc.in, got, back, absIn)
		}
	}
}
