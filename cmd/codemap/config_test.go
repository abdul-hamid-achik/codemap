/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// isolateConfigEnv points every codemap directory at a temp HOME so config
// show never touches the real data/config dirs, and clears the env vars that
// could otherwise inject an api key or config path from the host.
func isolateConfigEnv(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("CODEMAP_CONFIG_DIR", "")
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CACHE", filepath.Join(home, "cache"))
	t.Setenv("CODEMAP_OLLAMA_API_KEY", "")
}

// newConfigShowTestCmd builds a standalone command exposing exactly the flags
// runConfigShow/openSession touch: --path is read via
// cmd.Root().PersistentFlags() (targetDir), --config/--json via cmd.Flags()
// (openSessionAt/jsonOut) — registered on the matching flag set so both
// resolve without going through cobra's Execute()/merge step.
func newConfigShowTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "show"}
	cmd.PersistentFlags().StringP("path", "C", "", "")
	cmd.Flags().StringP("config", "c", "", "")
	cmd.Flags().Bool("json", false, "")
	return cmd
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()
	return buf.String()
}

// TestMaskSecret pins the masking rule the orchestrator's verification gate
// checks: empty stays empty (no key = today's behavior, unchanged), a short
// value collapses to "(set)" rather than exposing most of it via a 4-char
// suffix, and a normal-length key keeps only its last 4 characters.
func TestMaskSecret(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"short", "(set)"},
		{"12345678", "(set)"},
		{"test-key-1234", "****1234"},
		{"a-much-longer-ollama-cloud-key-abcd", "****abcd"},
	}
	for _, tc := range cases {
		if got := maskSecret(tc.in); got != tc.want {
			t.Errorf("maskSecret(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestConfigShowMasksAPIKey drives the real runConfigShow handler (text and
// --json) against a project config file carrying a fake embedding.api_key and
// asserts the raw secret never reaches stdout in either mode, while the
// masked last-4-chars form does — this is the secrets-hygiene contract for
// `codemap config show`.
func TestConfigShowMasksAPIKey(t *testing.T) {
	home := t.TempDir()
	isolateConfigEnv(t, home)

	proj := t.TempDir()
	const secret = "test-key-1234-5678" //nolint:gosec // fake test value
	wantMasked := maskSecret(secret)
	cfgYAML := "embedding:\n  api_key: " + secret + "\n"
	if err := os.WriteFile(filepath.Join(proj, "codemap.yaml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(jsonMode bool) string {
		cmd := newConfigShowTestCmd()
		if err := cmd.PersistentFlags().Set("path", proj); err != nil {
			t.Fatal(err)
		}
		if jsonMode {
			if err := cmd.Flags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}
		}
		return captureStdout(t, func() {
			if err := runConfigShow(cmd, nil); err != nil {
				t.Fatal(err)
			}
		})
	}

	text := run(false)
	if strings.Contains(text, secret) {
		t.Fatalf("config show (text) leaked the API key: %q", text)
	}
	if !strings.Contains(text, wantMasked) {
		t.Errorf("config show (text) missing masked form %q, got %q", wantMasked, text)
	}

	out := run(true)
	if strings.Contains(out, secret) {
		t.Fatalf("config show --json leaked the API key: %q", out)
	}
	var shown struct {
		Embedding struct {
			APIKey string `json:"APIKey"`
		} `json:"Embedding"`
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("decode config show --json: %v\noutput: %s", err, out)
	}
	if shown.Embedding.APIKey != wantMasked {
		t.Errorf("config show --json api_key = %q, want masked %q", shown.Embedding.APIKey, wantMasked)
	}
}

// TestConfigShowNoAPIKeyUnchanged pins additive-only behavior: with no
// embedding.api_key configured anywhere, `config show` renders exactly as it
// did before this feature existed — an empty api_key, no masking noise.
func TestConfigShowNoAPIKeyUnchanged(t *testing.T) {
	home := t.TempDir()
	isolateConfigEnv(t, home)
	proj := t.TempDir()

	cmd := newConfigShowTestCmd()
	if err := cmd.PersistentFlags().Set("path", proj); err != nil {
		t.Fatal(err)
	}
	text := captureStdout(t, func() {
		if err := runConfigShow(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(text, "api_key: \n") {
		t.Errorf("config show with no api_key configured should render an empty api_key line, got %q", text)
	}
}

// newServeTestCmd builds a standalone command exposing exactly the flags
// runServe/openSessionAt touch for the MCP profile: --path (targetDir),
// --config (openSessionAt), and --profile (applyConfigFlags), mirroring
// newConfigShowTestCmd's approach of registering flags directly rather than
// going through cobra's Execute()/merge step.
func newServeTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "serve"}
	cmd.PersistentFlags().StringP("path", "C", "", "")
	cmd.Flags().StringP("config", "c", "", "")
	cmd.Flags().String("profile", "full", "")
	return cmd
}

// TestMCPProfileFlagPrecedence pins I01's full file < env < flag chain at
// the CLI layer (internal/config's own test covers file < env in
// isolation): a project codemap.yaml sets core, CODEMAP_MCP_PROFILE=full
// overrides the file, and an explicit --profile=core flag wins over both —
// matching every other per-setting knob registered in configflags.go.
func TestMCPProfileFlagPrecedence(t *testing.T) {
	home := t.TempDir()
	isolateConfigEnv(t, home)
	t.Setenv("CODEMAP_MCP_PROFILE", "")
	proj := t.TempDir()

	openWithProfile := func(t *testing.T, setProfileFlag string) string {
		t.Helper()
		cmd := newServeTestCmd()
		if err := cmd.PersistentFlags().Set("path", proj); err != nil {
			t.Fatal(err)
		}
		if setProfileFlag != "" {
			if err := cmd.Flags().Set("profile", setProfileFlag); err != nil {
				t.Fatal(err)
			}
		}
		sess, err := openSessionAt(cmd, proj)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = sess.Close() }()
		return sess.Config.MCP.Profile
	}

	// 1. no file, no env, no flag -> DefaultConfig's "full".
	if got := openWithProfile(t, ""); got != "full" {
		t.Errorf("no override: profile = %q, want full", got)
	}

	// 2. file sets core -> file wins over the default.
	if err := os.WriteFile(filepath.Join(proj, "codemap.yaml"), []byte("mcp:\n  profile: core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := openWithProfile(t, ""); got != "core" {
		t.Errorf("file only: profile = %q, want core", got)
	}

	// 3. env overrides the file.
	t.Setenv("CODEMAP_MCP_PROFILE", "full")
	if got := openWithProfile(t, ""); got != "full" {
		t.Errorf("env over file: profile = %q, want full", got)
	}

	// 4. an explicit --profile flag wins over env (and file).
	if got := openWithProfile(t, "core"); got != "core" {
		t.Errorf("flag over env: profile = %q, want core", got)
	}
}
