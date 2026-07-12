package drivers

import "context"

// GeminiDriver is a v1 stub. The Gemini CLI headless mode can implement this
// interface later (run a prompt, parse its event stream, fold into Metrics).
type GeminiDriver struct{}

func (GeminiDriver) Name() string { return "gemini" }

func (GeminiDriver) Run(context.Context, string, Arm, string) (Metrics, error) {
	return Metrics{}, ErrNotImplemented
}
