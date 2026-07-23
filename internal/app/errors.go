package app

import (
	"errors"
)

// CodedError carries a stable, machine-readable code plus a remediation hint, so
// a --json agent consumer (or the MCP server) can map a failure to a deterministic
// next step instead of parsing a free-form error string. The service seam wraps
// hard failures (a graph DB that won't open, a non-repo where a repo is required)
// in a CodedError; the CLI maps Code→exit code and prints the {ok,error,code,hint}
// envelope under --json.
//
// Stable codes (exported so the CLI/MCP can map them without hardcoding strings):
const (
	CodeMissing      = "index_missing" // no index for the project (DB absent / unregistered)
	CodeCorrupt      = "index_corrupt" // the graph DB exists but won't open (schema, disk, perms)
	CodeNotARepo     = "not_a_repo"    // a git operation was required but cwd isn't a git repository
	CodeInvalidInput = "invalid_input" // the call itself is malformed (bad/empty argument); fix the input, not an internal fault
	CodeOperational  = "operational"   // an unclassified runtime failure (default; exit 1)
)

type CodedError struct {
	Code string // one of the Code* constants above
	Hint string // one-line remediation, e.g. "run: codemap index"
	Err  error
}

func (e *CodedError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (e *CodedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// coded wraps err in a CodedError with the given stable code + hint. A nil err
// returns nil.
func coded(code, hint string, err error) *CodedError {
	if err == nil {
		return nil
	}
	return &CodedError{Code: code, Hint: hint, Err: err}
}

// CodeOf extracts the stable machine code from err, defaulting to "operational"
// for an untyped error so a consumer always gets a switchable value.
func CodeOf(err error) string {
	var ce *CodedError
	if err != nil && errors.As(err, &ce) {
		if ce.Code == "" {
			return "operational"
		}
		return ce.Code
	}
	return "operational"
}

// HintOf extracts the remediation hint from a CodedError (empty for an
// untyped error, so a consumer falls back to its own default).
func HintOf(err error) string {
	var ce *CodedError
	if err != nil && errors.As(err, &ce) {
		return ce.Hint
	}
	return ""
}
