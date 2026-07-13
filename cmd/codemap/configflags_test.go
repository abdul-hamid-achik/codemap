/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

// TestIdleTimeoutMinutes pins P1-12 (B69): a sub-minute --idle-timeout must
// never round down to 0 (which daemon.go reads as "never shut down") — the
// opposite of what the user asked for.
func TestIdleTimeoutMinutes(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want int
	}{
		{"zero means never", 0, 0},
		{"sub-minute rounds up, not down to never", 30 * time.Second, 1},
		{"non-exact minute rounds up", 90 * time.Second, 2},
		{"exact minutes pass through unchanged", 2 * time.Minute, 2},
		{"exact one minute stays one", time.Minute, 1},
		{"negative treated as never", -5 * time.Second, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := idleTimeoutMinutes(tc.d); got != tc.want {
				t.Fatalf("idleTimeoutMinutes(%s) = %d, want %d", tc.d, got, tc.want)
			}
		})
	}
}

// newIdleTimeoutTestCmd builds a standalone command exposing exactly the
// --idle-timeout flag applyConfigFlags touches, mirroring
// newServeTestCmd/newConfigShowTestCmd's approach in config_test.go.
func newIdleTimeoutTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "daemon-start"}
	cmd.Flags().Duration("idle-timeout", 0, "")
	return cmd
}

// TestApplyConfigFlagsIdleTimeout exercises the fix through the actual flag
// pipeline (cobra flag parse + applyConfigFlags), not just the helper.
func TestApplyConfigFlagsIdleTimeout(t *testing.T) {
	cases := []struct {
		name string
		arg  string
		want int
	}{
		{"30s no longer truncates to never", "30s", 1},
		{"90s rounds up to 2m", "90s", 2},
		{"0 stays never", "0s", 0},
		{"2m exact", "2m", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newIdleTimeoutTestCmd()
			if err := cmd.Flags().Set("idle-timeout", tc.arg); err != nil {
				t.Fatal(err)
			}
			cfg := config.DefaultConfig()
			applyConfigFlags(cmd, cfg)
			if cfg.Daemon.IdleTimeoutMin != tc.want {
				t.Fatalf("--idle-timeout %s -> IdleTimeoutMin = %d, want %d", tc.arg, cfg.Daemon.IdleTimeoutMin, tc.want)
			}
		})
	}
}

// TestApplyConfigFlagsIdleTimeoutUnset pins that an unset flag never
// overrides a config-file/env value (the same .Changed gate every other
// knob in applyConfigFlags relies on).
func TestApplyConfigFlagsIdleTimeoutUnset(t *testing.T) {
	cmd := newIdleTimeoutTestCmd()
	cfg := config.DefaultConfig()
	cfg.Daemon.IdleTimeoutMin = 7
	applyConfigFlags(cmd, cfg)
	if cfg.Daemon.IdleTimeoutMin != 7 {
		t.Fatalf("unset --idle-timeout must not override the config value: got %d, want 7", cfg.Daemon.IdleTimeoutMin)
	}
}
