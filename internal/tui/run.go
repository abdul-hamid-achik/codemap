package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

// Run launches the studio TUI over the given session and project directory.
// The caller owns the session lifecycle (Close).
//
// Note: alt-screen and mouse reporting are enabled per-View in bubbletea v2
// (View.AltScreen / View.MouseMode — see view.go), not through program options,
// so there is nothing to configure on NewProgram here.
func Run(ctx context.Context, sess *app.Session, startDir string) error {
	_, err := tea.NewProgram(NewModel(ctx, sess, startDir)).Run()
	return err
}
