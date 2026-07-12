package drivers

import "context"

// CodexDriver is a v1 stub. The Codex CLI headless mode can implement this
// interface later (run a prompt, parse its event stream, fold into Metrics).
type CodexDriver struct{}

func (CodexDriver) Name() string { return "codex" }

func (CodexDriver) Run(context.Context, string, Arm, string) (Metrics, error) {
	return Metrics{}, ErrNotImplemented
}
