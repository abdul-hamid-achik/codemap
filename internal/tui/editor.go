package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editorClosedMsg reports the outcome of a suspend-to-$EDITOR round trip; err is
// nil on a clean exit.
type editorClosedMsg struct {
	file string
	line int
	err  error
}

// settledSelection is the current selection ONLY when the active tab is settled
// on a concrete result — Graph/Metrics always, Search/Impact once their input
// matches the last-run query (so a half-typed query still receives the letter),
// and Path only on the focused result. It gates the plain-letter actions (`e`
// jump-to-editor, `y` yank) exactly like the annotation composer gates `a`, so
// they never steal a keystroke the person meant to type into a query.
func (m Model) settledSelection() (graphCenter, bool) {
	switch m.active {
	case tabGraph, tabMetrics:
		return m.selectedCenter()
	case tabSearch:
		if m.searchQuery == "" || m.search.Value() != m.searchQuery {
			return graphCenter{}, false
		}
		return m.selectedCenter()
	case tabImpact:
		if m.impactRep == nil || !m.impactRep.Found || m.impactSymbol == "" || m.impact.Value() != m.impactSymbol {
			return graphCenter{}, false
		}
		return m.selectedCenter()
	case tabPath:
		if m.pathFocus != focusPathResult {
			return graphCenter{}, false
		}
		return m.selectedCenter()
	}
	return graphCenter{}, false
}

// locationSelection is a settled selection that carries an openable file:line —
// the target for both `e` (jump to $EDITOR) and `y` (yank file:line).
func (m Model) locationSelection() (graphCenter, bool) {
	c, ok := m.settledSelection()
	if !ok || c.file == "" || c.line <= 0 {
		return graphCenter{}, false
	}
	return c, true
}

// openEditor suspends studio and opens the selection's file:line in $EDITOR
// (falling back to $VISUAL). With neither set it is a no-op with a status hint,
// so it degrades cleanly on a bare shell.
func (m Model) openEditor(c graphCenter) (tea.Model, tea.Cmd) {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("VISUAL"))
	}
	if editor == "" {
		m.errMsg = ""
		m.statusMsg = "no $EDITOR (or $VISUAL) set — cannot open " + fmt.Sprintf("%s:%d", c.file, c.line)
		return m, nil
	}
	target := c.file
	if m.startDir != "" && !filepath.IsAbs(target) {
		target = filepath.Join(m.startDir, c.file)
	}
	cmd := editorCommand(editor, target, c.line)
	if m.startDir != "" {
		cmd.Dir = m.startDir
	}
	file, line := c.file, c.line
	m.errMsg = ""
	m.statusMsg = fmt.Sprintf("opening %s:%d in %s…", file, line, filepath.Base(strings.Fields(editor)[0]))
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorClosedMsg{file: file, line: line, err: err}
	})
}

// editorCommand builds the launch for an $EDITOR value, honoring the common
// file:line conventions of the major editors and falling back to the widely
// supported `+LINE file` form (vim/nvim/vi/nano/emacs/kakoune). The editor value
// may itself carry flags (e.g. "code -w"), which are preserved.
func editorCommand(editor, file string, line int) *exec.Cmd {
	fields := strings.Fields(editor)
	name := fields[0]
	args := append([]string{}, fields[1:]...)
	base := strings.ToLower(filepath.Base(name))
	loc := fmt.Sprintf("%s:%d", file, line)
	switch {
	case strings.Contains(base, "code") || strings.Contains(base, "codium") || strings.Contains(base, "cursor"):
		args = append(args, "-g", loc)
	case base == "subl" || strings.Contains(base, "sublime") || base == "hx" || base == "helix" || base == "micro":
		args = append(args, loc)
	case base == "idea" || base == "goland" || base == "pycharm" || base == "webstorm" || base == "rider" || base == "clion":
		args = append(args, "--line", strconv.Itoa(line), file)
	default: // vim, nvim, vi, nano, emacs, kak, gedit, …
		args = append(args, "+"+strconv.Itoa(line), file)
	}
	return exec.Command(name, args...)
}

// yank copies the selection's file:line to the system clipboard via OSC52
// (bubbletea v2's tea.SetClipboard) and also echoes it into the status line, so
// the target is copyable even in a terminal that does not honor OSC52.
func (m Model) yank(c graphCenter) (tea.Model, tea.Cmd) {
	loc := fmt.Sprintf("%s:%d", c.file, c.line)
	m.errMsg = ""
	m.statusMsg = "yanked " + loc
	return m, tea.SetClipboard(loc)
}
