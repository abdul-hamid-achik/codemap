/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

// Exit-code taxonomy (extends P2-06):
//
//	0 = answered (results, possibly empty-but-resolved like "no callers")
//	1 = operational error (bad flag, git failure, untyped runtime error)
//	2 = not found / not indexed (a valid query with no answer)
//	3 = index_missing  — no index for the project (DB absent / unregistered)
//	4 = index_corrupt  — the graph DB exists but won't open
//	5 = not_a_repo     — a git operation was required but cwd isn't a repo
//
// Scripts/agents can distinguish "answered" from a structured failure without
// parsing output: the --json envelope carries the same code, so a consumer
// can map code→action deterministically either way.
const (
	exitOK           = 0
	exitOperational  = 1
	exitNotFound     = 2
	exitIndexMissing = 3
	exitIndexCorrupt = 4
	exitNotARepo     = 5
)

// exitCodeFor maps a stable machine code (app.CodedError.Code) to its exit code.
// Unknown codes fall back to the operational exit.
func exitCodeFor(code string) int {
	switch code {
	case app.CodeMissing:
		return exitIndexMissing
	case app.CodeCorrupt:
		return exitIndexCorrupt
	case app.CodeNotARepo:
		return exitNotARepo
	}
	return exitOperational
}

// exitCoded carries the exit code chosen for a --json failure through cobra's
// error return so main() can os.Exit it. The envelope is already printed to
// stdout, so cobra must NOT echo the error (silenced in jsonHandler).
type exitCoded struct {
	code int
	err  error
}

func (e *exitCoded) Error() string {
	if e == nil {
		return ""
	}
	if e.err != nil {
		return e.err.Error()
	}
	return ""
}

func (e *exitCoded) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// jsonEnvelope is the structured failure a --json consumer reads on ANY error.
// Printed to stdout (not stderr) so an agent parsing stdout JSON still gets it
// even when stderr is discarded.
type jsonEnvelope struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Code  string `json:"code"`
	Hint  string `json:"hint,omitempty"`
}

// jsonHandler wraps a RunE so a --json failure prints the structured envelope
// to stdout (instead of cobra's stderr "Error: …" line) with a stable machine
// code + remediation hint, and returns an exitCoded error so main() maps it to
// the documented exit taxonomy. Under a non-json run the original error passes
// through unchanged (cobra prints it normally; main maps not-found→2).
func jsonHandler(fn func(cmd *cobra.Command, args []string) error) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		err := fn(cmd, args)
		if err == nil {
			return nil
		}
		if jsonOut(cmd) {
			code := app.CodeOf(err)
			env := jsonEnvelope{
				OK:    false,
				Error: err.Error(),
				Code:  code,
				Hint:  app.HintOf(err),
			}
			if env.Hint == "" {
				env.Hint = defaultHint(code)
			}
			_ = printEnvelope(env)
			cmd.SilenceErrors = true // envelope already printed; don't let cobra echo
			return &exitCoded{code: exitCodeFor(code), err: err}
		}
		return err
	}
}

// printEnvelope writes the JSON error envelope to stdout (indented, no HTML
// escaping — matches printJSON so paths/generics stay legible).
func printEnvelope(env jsonEnvelope) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

// defaultHint is the fallback remediation when a CodedError carried none.
func defaultHint(code string) string {
	switch code {
	case app.CodeMissing:
		return "run: codemap index"
	case app.CodeCorrupt:
		return "back up the graph DB, then run: codemap index --reindex"
	case app.CodeNotARepo:
		return "run this inside a git repository"
	}
	return ""
}

// asExitCoded extracts the exit code from an exitCoded error; ok is false for a
// plain error (main falls back to the not-found/operational mapping).
func asExitCoded(err error) (int, bool) {
	var ec *exitCoded
	if errors.As(err, &ec) {
		return ec.code, true
	}
	return 0, false
}
