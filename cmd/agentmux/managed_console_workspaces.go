package main

import (
	"agentmux/internal/managedlaunch"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type consoleWorkspaceList struct {
	request uint64
	rows    []workspaceListing
	err     error
}
type consoleWorkspacePicker struct {
	rows        []workspaceListing
	selected    int
	loading     bool
	err         error
	query       string
	filtering   bool
	filterFocus string
}

func (p *consoleWorkspacePicker) visibleRows() []workspaceListing {
	return matchingWorkspaceRows(p.rows, p.query)
}

func (p *consoleWorkspacePicker) selectedState() string {
	rows := p.visibleRows()
	if p.selected < 0 || p.selected >= len(rows) {
		return ""
	}
	return rows[p.selected].State
}

func (p *consoleWorkspacePicker) selectState(state string) {
	p.selected = 0
	for i, row := range p.visibleRows() {
		if row.State == state {
			p.selected = i
			return
		}
	}
}

func (p *consoleWorkspacePicker) setRows(rows []workspaceListing) {
	focus := p.selectedState()
	p.rows, p.loading, p.err = rows, false, nil
	p.selectState(focus)
}

func (p *consoleWorkspacePicker) setQuery(query string) {
	focus := p.selectedState()
	if strings.TrimSpace(p.query) == "" && strings.TrimSpace(query) != "" {
		p.filterFocus = focus
	}
	p.query = query
	if strings.TrimSpace(query) == "" && p.filterFocus != "" {
		focus = p.filterFocus
	}
	p.selectState(focus)
}

func (m console) workspaceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	picker := m.workspacePicker
	switch msg.String() {
	case "esc":
		m.workspaceRequest++
		m.workspacePicker = nil
	case "ctrl+c":
		return m, m.close()
	case "down", "ctrl+n":
		picker.selected = min(max(0, len(picker.visibleRows())-1), picker.selected+1)
	case "up", "ctrl+p":
		picker.selected = max(0, picker.selected-1)
	case "enter":
		if m.workspacePicker.loading || m.workspacePicker.err != nil {
			return m, nil
		}
		rows := picker.visibleRows()
		if picker.selected < 0 || picker.selected >= len(rows) {
			return m, nil
		}
		row := rows[picker.selected]
		if row.Status != "running" && row.Status != "unknown" {
			m.message = "Workspace is " + row.Status + ". Inspect it with amx managed status --state " + row.State + " before restarting."
			return m, nil
		}
		if row.State == m.state {
			m.workspacePicker = nil
			return m, nil
		}
		return m.openWorkspace(row.State)
	default:
		if picker.filtering {
			switch msg.Type {
			case tea.KeyRunes:
				query := picker.query + string(msg.Runes)
				if len(query) <= 512 {
					picker.setQuery(query)
				}
			case tea.KeySpace:
				if len(picker.query) < 512 {
					picker.setQuery(picker.query + " ")
				}
			case tea.KeyBackspace, tea.KeyCtrlH:
				runes := []rune(picker.query)
				if len(runes) > 0 {
					picker.setQuery(string(runes[:len(runes)-1]))
				}
			case tea.KeyCtrlU:
				picker.setQuery("")
			}
			return m, nil
		}
		switch msg.String() {
		case "/", "ctrl+f":
			picker.filtering, picker.filterFocus = true, picker.selectedState()
		case "j":
			picker.selected = min(max(0, len(picker.visibleRows())-1), picker.selected+1)
		case "k":
			picker.selected = max(0, picker.selected-1)
		case "q":
			m.workspaceRequest++
			m.workspacePicker = nil
		}
	}
	return m, nil
}
func (m console) workspacePanel(width, height int) string {
	var out strings.Builder
	out.WriteString(consoleAccent.Render("Workspaces") + "\n" + consoleMuted.Render("↑/↓ choose · Enter opens workspace · / filters · Esc cancels") + "\n")
	rows := m.workspacePicker.visibleRows()
	if m.workspacePicker.filtering {
		fmt.Fprintf(&out, "Filter: %s▏ · %d/%d · Ctrl+U clears\n\n", consoleContent(m.workspacePicker.query, 200), len(rows), len(m.workspacePicker.rows))
	} else {
		out.WriteString("\n")
	}
	if m.workspacePicker.loading {
		return consoleClip(out.String()+"Loading saved workspaces…\nYour previous draft or review remains open. Esc cancels.", width, height)
	}
	if m.workspacePicker.err != nil {
		return consoleClip(out.String()+"Workspace inventory unavailable: "+consoleContent(m.workspacePicker.err.Error(), 300)+"\nEsc returns to your previous draft or review.", width, height)
	}
	if len(rows) == 0 {
		if len(m.workspacePicker.rows) > 0 {
			out.WriteString("No matching workspaces. Ctrl+U clears the filter.\n")
		} else {
			out.WriteString("No saved workspaces found. amx start creates one.\nOlder custom paths: amx workspaces --register DIR")
		}
	}
	count := max(1, (height-7)/4)
	first := max(0, m.workspacePicker.selected-count+1)
	for i := first; i < len(rows) && i < first+count; i++ {
		row := rows[i]
		label := fmt.Sprintf("%s · %s · %d saved session(s)", row.Session, row.Status, row.Agents)
		if i == m.workspacePicker.selected {
			label = consoleSelected.Render("› " + consoleContent(label, 300))
		}
		fmt.Fprintf(&out, "%s\n  %s\n\n", label, consoleContent(row.State, 500))
	}
	return consoleClip(out.String(), width, height)
}

func (m console) openWorkspace(state string) (tea.Model, tea.Cmd) {
	if state == m.state {
		m.workspacePicker = nil
		return m, nil
	}
	if request := os.Getenv("AGENTMUX_HANDOFF_FILE"); request != "" && !m.popup && strings.HasPrefix(os.Getenv("TMUX"), managedlaunch.SocketPath(m.state)+",") {
		info, err := os.Lstat(filepath.Dir(request))
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 {
			m.message = "Workspace handoff channel unavailable; reopen an attachment with the current CLI."
			return m, nil
		}
		body, _ := json.Marshal(map[string]string{"state": state})
		file, err := os.OpenFile(request, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, err = file.Write(body)
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil {
			m.message = "Workspace handoff request failed: " + err.Error()
			return m, nil
		}
		m.workspacePicker = nil
		return m, m.close()
	}
	exe, err := os.Executable()
	if err != nil {
		return m, func() tea.Msg { return consoleAction{err: err} }
	}
	cmd := exec.Command(exe, "dashboard", "--state", state, "--popup")
	// dashboard is an inline observer, not a tmux attachment. Retain its
	// actual host identity so Enter can prevent nesting another server's
	// native attachment inside this workspace's dashboard pane.
	cmd.Env = os.Environ()
	m.workspacePicker = nil
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return consoleAction{message: "Returned to the previous workspace. Both workspaces keep running.", err: err}
	})
}

func (m *console) requestWorkspacePicker() tea.Cmd {
	m.workspaceRequest++
	m.workspacePicker = &consoleWorkspacePicker{loading: true}
	request := m.workspaceRequest
	return func() tea.Msg {
		rows, err := workspaceInventory("")
		return consoleWorkspaceList{rows: rows, err: err, request: request}
	}
}
