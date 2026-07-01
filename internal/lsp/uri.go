package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
)

// PathToURI converts a filesystem path to a percent-encoded file:// URI
// suitable for sending to a language server. Uses net/url so spaces,
// non-ASCII characters, and Windows drive letters are encoded correctly
// (P1-02: pre-fix this was bare string concatenation, so paths with
// spaces produced "file:///path/with space" which the server couldn't
// match back to the on-disk file).
func PathToURI(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	// On Windows, filepath.Abs produces "C:\path\..."; net/url wants
	// the forward-slash form for the URL Path.
	abs = filepath.ToSlash(abs)
	u := url.URL{Scheme: "file", Path: abs}
	return u.String(), nil
}

// PathFromURI is the inverse of PathToURI: it percent-decodes a file://
// URI to a filesystem path. Returns the decoded Path and the unmodified
// URL for callers that need both.
func PathFromURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", nil // non-file URI — caller decides what to do
	}
	// url.Parse already percent-decodes the Path field, so this is
	// the on-disk path. On Windows, u.Path starts with "/C:/..."; the
	// leading slash is the URI's path-root marker, not a real separator.
	path := u.Path
	if len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = strings.TrimPrefix(path, "/")
	}
	return filepath.FromSlash(path), nil
}
