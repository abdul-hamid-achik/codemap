/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// isInteractiveTTY reports whether BOTH stdin and stdout are terminals — the
// gate (with !--json) for the live index progress bar. Both matter: Bubble Tea
// reads stdin for key events, so if stdin isn't a terminal (piped, /dev/null, a
// CI runner) the program can't run and indexing would hard-fail; requiring both
// keeps such cases on the plain path. Redirected/piped output stays plain.
func isInteractiveTTY() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// fileMsg advances the bar to a scanned file; doneMsg tells it indexing finished
// and it should quit (the real Result is delivered out-of-band, see
// runIndexWithBar).
type (
	fileMsg struct {
		done, total int
		file        string
	}
	embedMsg struct{ done, total int } // embedding-phase progress (the long part)
	doneMsg  struct{}
)

// progressModel is a minimal inline Bubble Tea program: a single animated
// progress bar that tracks the extraction pass, file by file.
type progressModel struct {
	prog        progress.Model
	done, total int
	file        string
	embedding   bool      // switched to the embedding phase (the long part of a reindex)
	finished    bool      // indexing reported done
	canceled    bool      // user pressed ctrl+c
	start       time.Time // when the first progress event arrived
	embedStart  time.Time // when the embedding phase started (for ETA)
}

func newProgressModel() progressModel {
	return progressModel{prog: progress.New(progress.WithDefaultBlend(), progress.WithWidth(30))}
}

func (m progressModel) Init() tea.Cmd { return nil }

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case fileMsg:
		if m.start.IsZero() {
			m.start = time.Now()
		}
		m.done, m.total, m.file = msg.done, msg.total, msg.file
		var pct float64
		if msg.total > 0 {
			pct = float64(msg.done) / float64(msg.total)
		}
		return m, m.prog.SetPercent(pct)
	case embedMsg:
		// Parsing is fast; embedding is the wait. Switch the bar to it so it doesn't
		// sit at 100% silently while Ollama works through the nodes.
		if m.embedStart.IsZero() {
			m.embedStart = time.Now()
		}
		m.embedding, m.done, m.total, m.file = true, msg.done, msg.total, ""
		var pct float64
		if msg.total > 0 {
			pct = float64(msg.done) / float64(msg.total)
		}
		return m, m.prog.SetPercent(pct)
	case doneMsg:
		m.finished = true
		return m, tea.Quit
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.canceled = true // runIndexWithBar cancels the index and reports it
			return m, tea.Quit
		}
	case progress.FrameMsg:
		pm, cmd := m.prog.Update(msg)
		m.prog = pm
		return m, cmd
	}
	return m, nil
}

func (m progressModel) View() tea.View {
	if m.finished {
		return tea.NewView("") // clear the bar; the summary prints after the program quits
	}
	line := m.prog.View()
	if m.total > 0 {
		line += fmt.Sprintf("  %d/%d", m.done, m.total)
	}
	switch {
	case m.embedding:
		line += "  embedding"
		if eta := m.eta(m.embedStart); eta != "" {
			line += "  ~" + eta + " left"
		}
	case m.file != "":
		line += "  " + truncStr(m.file, 26)
		if eta := m.eta(m.start); eta != "" {
			line += "  ~" + eta + " left"
		}
	}
	return tea.NewView(line) // inline (AltScreen defaults false) — a transient one-liner
}

// eta returns a human-readable remaining-time estimate based on elapsed time
// and the current completion rate. Returns "" until enough data is available
// (at least 2 items done and 1 second elapsed) for a meaningful estimate.
func (m progressModel) eta(start time.Time) string {
	if m.done < 2 || m.total <= m.done || start.IsZero() {
		return ""
	}
	elapsed := time.Since(start)
	if elapsed < time.Second {
		return ""
	}
	remaining := m.total - m.done
	rate := float64(m.done) / elapsed.Seconds()
	if rate <= 0 {
		return ""
	}
	eta := time.Duration(float64(remaining)/rate) * time.Second
	if eta < time.Second {
		return "<1s"
	}
	return formatETA(eta)
}

// formatETA renders a duration compactly: "45s", "2m30s", "1h05m".
func formatETA(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) - m*60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) - h*60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}

// runIndexWithBar runs an index while showing a live progress bar, then returns
// the real *app.IndexReport. The indexer blocks, so it runs on a goroutine and
// feeds the bar via prog.Send; the authoritative result travels back on a
// buffered channel (not the model) so it survives interruption.
//
// Crucially, a TUI failure never fails the index: indexing runs on the parent
// context, so if prog.Run errors (e.g. no usable terminal), we still wait for
// the index to finish and return its real result. Only a genuine ctrl+c cancels
// indexing — via a child context cancelled solely on that path, so the goroutine
// can't outlive this call and touch the DB after sess.Close.
func runIndexWithBar(ctx context.Context, svc *app.Service, cwd string, opts index.Options, withEmbed bool) (*app.IndexReport, error) {
	idxCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	prog := tea.NewProgram(newProgressModel(), tea.WithContext(ctx))
	opts.OnFile = func(done, total int, rel string) {
		prog.Send(fileMsg{done: done, total: total, file: rel})
	}
	opts.OnEmbed = func(done, total int) {
		prog.Send(embedMsg{done: done, total: total})
	}

	type indexOut struct {
		rep *app.IndexReport
		err error
	}
	resCh := make(chan indexOut, 1) // buffered: the goroutine never blocks on send
	go func() {
		rep, err := svc.Index(idxCtx, cwd, opts, withEmbed)
		resCh <- indexOut{rep, err}
		prog.Send(doneMsg{}) // no-op if the program already quit
	}()

	finalModel, runErr := prog.Run()
	if runErr != nil {
		// The bar couldn't run (not a user action) — don't kill indexing; let it
		// finish silently and return the real result.
		out := <-resCh
		return out.rep, out.err
	}
	if m, ok := finalModel.(progressModel); ok && m.canceled {
		cancel() // stop indexing
		<-resCh  // wait for the goroutine to unwind
		return nil, fmt.Errorf("indexing canceled")
	}
	out := <-resCh // doneMsg path: indexing already finished; authoritative result
	return out.rep, out.err
}
