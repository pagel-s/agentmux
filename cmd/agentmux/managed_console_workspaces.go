package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type consoleWorkspaceList struct {
	request uint64
	rows    []workspaceListing
	err     error
}
type consoleWorkspacePicker struct {
	rows     []workspaceListing
	selected int
}

func (m console) workspaceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.workspacePicker = nil
	case "ctrl+c":
		return m, m.close()
	case "down", "j":
		m.workspacePicker.selected = min(max(0, len(m.workspacePicker.rows)-1), m.workspacePicker.selected+1)
	case "up", "k":
		m.workspacePicker.selected = max(0, m.workspacePicker.selected-1)
	case "enter":
		if len(m.workspacePicker.rows) == 0 {
			return m, nil
		}
		row := m.workspacePicker.rows[m.workspacePicker.selected]
		if row.Status != "running" && row.Status != "unknown" {
			m.message = "Workspace is " + row.Status + ". Inspect it with agentmux managed status --state " + row.State + " before restarting."
			return m, nil
		}
		if row.State == m.state {
			m.workspacePicker = nil
			return m, nil
		}
		exe, err := os.Executable()
		if err != nil {
			return m, func() tea.Msg { return consoleAction{err: err} }
		}
		cmd := exec.Command(exe, "dashboard", "--state", row.State)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "TMUX=") && !strings.HasPrefix(entry, "TMUX_PANE=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		m.workspacePicker = nil
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return consoleAction{message: "Returned to the previous workspace. Both workspaces keep running.", err: err}
		})
	}
	return m, nil
}
func (m console) workspacePanel(width, height int) string {
	var out strings.Builder
	out.WriteString(consoleAccent.Render("Workspaces") + "\n" + consoleMuted.Render("↑/↓ choose · Enter opens observer · q returns here · Esc cancels") + "\n\n")
	rows := m.workspacePicker.rows
	if len(rows) == 0 {
		out.WriteString("No saved workspaces found. agentmux start creates one.\nOlder custom paths: agentmux workspaces --register DIR")
	}
	count := max(1, (height-5)/4)
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
