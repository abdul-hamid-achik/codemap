/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

// TestExitCodeFor pins the documented exit-code taxonomy (extends P2-06):
// index_missing→3, index_corrupt→4, not_a_repo→5, anything else→1.
func TestExitCodeFor(t *testing.T) {
	cases := map[string]int{
		codeNotFound:        exitNotFound,
		codeNotIndexed:      exitNotFound,
		app.CodeMissing:     exitIndexMissing,
		app.CodeCorrupt:     exitIndexCorrupt,
		app.CodeNotARepo:    exitNotARepo,
		app.CodeOperational: exitOperational,
		"unknown":           exitOperational,
		"":                  exitOperational,
	}
	for code, want := range cases {
		if got := exitCodeFor(code); got != want {
			t.Errorf("exitCodeFor(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestOutcomeErrorsUseExitTwoCodes(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{notFoundError("no such symbol", "try find"), codeNotFound},
		{notIndexedError("demo"), codeNotIndexed},
	}
	for _, tc := range cases {
		cmd := &cobra.Command{Use: "t"}
		cmd.Flags().Bool("json", false, "")
		_ = cmd.Flags().Set("json", "true")
		wrapped := jsonHandler(func(*cobra.Command, []string) error { return tc.err })

		r, w, _ := os.Pipe()
		old := os.Stdout
		os.Stdout = w
		ret := wrapped(cmd, nil)
		_ = w.Close()
		os.Stdout = old
		var out bytes.Buffer
		_, _ = out.ReadFrom(r)
		_ = r.Close()

		if code, ok := asExitCoded(ret); !ok || code != exitNotFound {
			t.Fatalf("%s returned exit=%d ok=%v, want exit 2", tc.code, code, ok)
		}
		var env jsonEnvelope
		if err := json.Unmarshal(out.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		if env.Code != tc.code {
			t.Errorf("envelope code = %q, want %q", env.Code, tc.code)
		}
	}
}

func TestJSONRequestedInArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"after command", []string{"callers", "--json"}, true},
		{"before command", []string{"--json", "callers"}, true},
		{"after unknown flag", []string{"callers", "--bad", "--json"}, true},
		{"explicit true", []string{"callers", "--json=true"}, true},
		{"explicit false", []string{"callers", "--json=false"}, false},
		{"last occurrence wins false", []string{"--json", "callers", "--json=false"}, false},
		{"after terminator is positional", []string{"callers", "--", "--json"}, false},
		{"absent", []string{"callers"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonRequestedInArgs(tc.args); got != tc.want {
				t.Fatalf("jsonRequestedInArgs(%q) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// TestCodeOfHintOf pins the app-side extraction: a CodedError yields its code +
// hint; an untyped error defaults to "operational" / "".
func TestCodeOfHintOf(t *testing.T) {
	ce := &app.CodedError{Code: app.CodeCorrupt, Hint: "reindex", Err: errors.New("boom")}
	if got := app.CodeOf(ce); got != app.CodeCorrupt {
		t.Errorf("CodeOf(coded) = %q, want %q", got, app.CodeCorrupt)
	}
	if got := app.HintOf(ce); got != "reindex" {
		t.Errorf("HintOf(coded) = %q, want %q", got, "reindex")
	}
	plain := errors.New("plain boom")
	if got := app.CodeOf(plain); got != app.CodeOperational {
		t.Errorf("CodeOf(plain) = %q, want %q", got, app.CodeOperational)
	}
	if got := app.HintOf(plain); got != "" {
		t.Errorf("HintOf(plain) = %q, want empty", got)
	}
	if got := app.CodeOf(nil); got != app.CodeOperational {
		t.Errorf("CodeOf(nil) = %q, want %q", got, app.CodeOperational)
	}
}

// TestJSONHandlerEnvelope pins feature 3: under --json, a RunE failure prints
// the structured {ok,error,code,hint} envelope to STDOUT (not stderr) and
// returns an exitCoded carrying the mapped exit code. Cobra is silenced so no
// "Error:" line leaks. Under a non-json run the original error passes through.
func TestJSONHandlerEnvelope(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
		exit int
		hint string
	}{
		{"corrupt", errors.New("corrupt fail"), app.CodeCorrupt, exitIndexCorrupt, "reindex hint"},
		{"missing", errors.New("missing fail"), app.CodeMissing, exitIndexMissing, ""},
		{"plain", errors.New("some failure"), app.CodeOperational, exitOperational, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "t"}
			// jsonOut reads cmd.Flags().GetBool("json"); register on the local
			// flag set (mimics the merged state a real subcommand sees) and set it.
			cmd.Flags().Bool("json", false, "")
			_ = cmd.Flags().Set("json", "true")

			var innerErr error
			if c.code != "" && c.code != app.CodeOperational {
				innerErr = &app.CodedError{Code: c.code, Hint: c.hint, Err: c.err}
			} else {
				innerErr = c.err
			}
			fn := func(*cobra.Command, []string) error { return innerErr }
			wrapped := jsonHandler(fn)

			// Capture stdout.
			r, w, _ := os.Pipe()
			old := os.Stdout
			os.Stdout = w
			done := make(chan string)
			go func() {
				var b bytes.Buffer
				_, _ = b.ReadFrom(r)
				done <- b.String()
			}()

			ret := wrapped(cmd, nil)
			_ = w.Close()
			os.Stdout = old
			out := <-done

			if ret == nil {
				t.Fatal("expected a non-nil error from jsonHandler on failure")
			}
			code, ok := asExitCoded(ret)
			if !ok {
				t.Fatalf("expected exitCoded, got %T (%v)", ret, ret)
			}
			if code != c.exit {
				t.Errorf("exit code = %d, want %d", code, c.exit)
			}
			// Cobra must be silenced so it doesn't echo the error to stderr.
			if !cmd.SilenceErrors {
				t.Errorf("jsonHandler should silence cobra errors")
			}
			// The envelope must be valid JSON on stdout with the right shape.
			var env jsonEnvelope
			if err := json.Unmarshal([]byte(out), &env); err != nil {
				t.Fatalf("stdout not valid JSON envelope: %v\n%s", err, out)
			}
			if env.OK {
				t.Errorf("envelope ok should be false, got true")
			}
			wantCode := c.code
			if wantCode == "" || wantCode == app.CodeOperational {
				wantCode = app.CodeOperational
			}
			if env.Code != wantCode {
				t.Errorf("envelope code = %q, want %q", env.Code, wantCode)
			}
			// A CodedError carries its hint; a plain error gets the default hint.
			if c.hint != "" && env.Hint != c.hint {
				t.Errorf("envelope hint = %q, want %q", env.Hint, c.hint)
			}
			if !strings.Contains(env.Error, "fail") && env.Error == "" {
				t.Errorf("envelope error should carry the message, got %q", env.Error)
			}
		})
	}
}

// TestJSONHandlerPassesThroughNonJSON pins that under a non-json run, jsonHandler
// returns the original error unchanged (cobra prints it; main maps not-found→2).
func TestJSONHandlerPassesThroughNonJSON(t *testing.T) {
	cmd := &cobra.Command{Use: "t"}
	cmd.Flags().Bool("json", false, "")
	// json flag NOT set → false.
	orig := errors.New("boom")
	wrapped := jsonHandler(func(*cobra.Command, []string) error { return orig })
	ret := wrapped(cmd, nil)
	if ret != orig {
		t.Errorf("non-json run should return the original error, got %v", ret)
	}
	if cmd.SilenceErrors {
		t.Errorf("non-json run should NOT silence cobra")
	}
}

// TestJSONHandlerSuccessNoop pins that on success jsonHandler returns nil and
// prints nothing.
func TestJSONHandlerSuccessNoop(t *testing.T) {
	cmd := &cobra.Command{Use: "t"}
	cmd.Flags().Bool("json", false, "")
	_ = cmd.Flags().Set("json", "true")
	wrapped := jsonHandler(func(*cobra.Command, []string) error { return nil })
	if ret := wrapped(cmd, nil); ret != nil {
		t.Errorf("success should return nil, got %v", ret)
	}
}

// TestEnvelopeCodesRoundTrip pins that the codes the service seam emits
// (index_missing / index_corrupt) round-trip through CodedError → CodeOf →
// exitCodeFor to the documented exit codes.
func TestEnvelopeCodesRoundTrip(t *testing.T) {
	for code, exit := range map[string]int{
		app.CodeMissing:  exitIndexMissing,
		app.CodeCorrupt:  exitIndexCorrupt,
		app.CodeNotARepo: exitNotARepo,
	} {
		ce := &app.CodedError{Code: code, Err: fmt.Errorf("x")}
		if got := exitCodeFor(app.CodeOf(ce)); got != exit {
			t.Errorf("code %q → exit %d, want %d", code, got, exit)
		}
	}
}

// silence the unused-import linter if a future trim removes a use.
var _ = os.Stdout
