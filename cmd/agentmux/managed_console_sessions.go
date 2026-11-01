package main

import (
	"fmt"
	"strings"

	"agentmux/internal/managed"
	"agentmux/internal/sessioninventory"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *console) openSessionSwitcher() {
	m.compose("find", "session title, project, provider or ID", "", "")
	m.findIndex, m.findIdentity = 0, "agent:"+m.selectedID()
	if m.attentionOnly {
		m.findIdentity = ""
	}
	if m.selectedID() == "" {
		m.findIdentity = ""
	}
	if m.tab == 5 {
		if rows := m.observedSessions(); len(rows) > 0 {
			m.findIdentity = "observed:" + rows[m.observedSelected%len(rows)].ID
		}
	}
	m.message = ""
}

func (m console) findMatchIdentity(v sessionMatch) string {
	if v.agent >= 0 && v.agent < len(m.agents) {
		return "agent:" + m.agents[v.agent].ID
	}
	if rows := m.observedSessions(); v.observation >= 0 && v.observation < len(rows) {
		return "observed:" + rows[v.observation].ID
	}
	return ""
}

func (m console) findMatchIndex(matches []sessionMatch) int {
	if len(matches) == 0 {
		return -1
	}
	if m.findIdentity == "" {
		return m.findIndex % len(matches)
	}
	for i, v := range matches {
		if m.findMatchIdentity(v) == m.findIdentity {
			return i
		}
	}
	return -1
}

func (m console) findMatch() (sessionMatch, bool) {
	matches := m.sessionMatches()
	if index := m.findMatchIndex(matches); index >= 0 {
		return matches[index], true
	}
	return sessionMatch{}, false
}

func (m console) chooseSession(openNative bool) (tea.Model, tea.Cmd) {
	match, ok := m.findMatch()
	if !ok {
		m.message = "Choose a visible session with ↑/↓ or change the search. The previous selection may no longer be available."
		return m, nil
	}
	m.composer, m.details, m.text = "", "", nil
	m.message = ""
	if match.agent >= 0 {
		m.explicitSessionChoice(match.agent)
		m.scope = 0
		if m.tab == 6 {
			m.teamMemberIndex = match.agent
		}
		m.closeMemory()
		a := m.agents[m.selected]
		m.data.headless = !a.External && m.data.runtime.Agents[a.ID].PaneID == "" && (m.data.research != nil || m.data.runtime.AttemptID == "" && a.Config.Interaction != "interactive")
		if !openNative {
			if m.tab == 5 {
				m.tab = 1
			}
			return m, m.fetch
		}
		m.tab = 1
		if m.nativeNeedsRelaunch() {
			m.reviewNativeRelaunch()
			return m, nil
		}
		return m, tea.Batch(m.fetch, m.attach())
	}
	m.explicitSessionChoice(m.selected)
	m.observedSelected, m.tab = match.observation, 5
	if openNative {
		v := m.observedSessions()[m.observedSelected]
		body := observedTitle(v) + "\nObserved ID: " + v.ID + "\nProject: " + v.Project + "\nOriginal terminal: " + v.TTY
		if v.AttachCommand != "" {
			body += "\n\nOpen from a separate terminal:\n" + v.AttachCommand
		}
		body += observationWorkspaceNote(v)
		body += "\n\nThis terminal was discovered, not launched by Agentmux. Its native input stays in its original terminal. Esc returns to Existing terminals; c prepares a communication bridge."
		m.reader = &consoleReader{title: "Existing terminal", body: body}
	}
	return m, nil
}

func consoleAgentStatus(a managed.AgentInfo, m console) string {
	status := a.State
	if a.External && a.State == "active" {
		status = observationStatus(a.ExternalRef, m.data.observed)
	}
	if pane, ok := m.data.runtime.Agents[a.ID]; ok {
		status = pane.State
	}
	if status == "" {
		status = "unknown"
	}
	if m.offline {
		status = "saved " + status
	}
	return status
}

func (m console) sessionSwitcherPanel(width, height int) string {
	var out strings.Builder
	matches := m.sessionMatches()
	selected := m.findMatchIndex(matches)
	position := 0
	if selected >= 0 {
		position = selected + 1
	}
	title := "Switch session"
	if m.attentionOnly {
		title = "Attention queue"
	}
	fmt.Fprintf(&out, "%s %s\n", consoleAccent.Render(title), consoleMuted.Render(fmt.Sprintf("%d/%d", position, len(matches))))
	query := consoleEditor(m.text, m.textCursor, max(1, width-9), 1)
	out.WriteString("Search: " + query + "\n\n")
	if m.attentionOnly {
		if m.data.taskSummaryErr != nil {
			out.WriteString(consoleMuted.Render("Task totals unavailable; native facts and loaded task evidence only") + "\n")
		} else if m.data.taskSummary != nil {
			out.WriteString(consoleMuted.Render("Native facts + complete retained task totals") + "\n")
		}
	}

	if len(matches) == 0 {
		out.WriteString("No sessions match. Search a task title, project, provider or ID.")
		return consoleClip(out.String(), width, height)
	}
	if selected < 0 {
		out.WriteString(consoleAccent.Render("Selection changed. Choose a session with ↑/↓.") + "\n")
	}
	rows := max(1, (height-4)/4)
	first := max(0, selected-rows+1)
	last := min(len(matches), first+rows)
	for i := first; i < last; i++ {
		v := matches[i]
		title := "  " + consoleMiddle(v.title, max(1, width-2))
		if i == selected {
			title = consoleSelected.Render("› " + consoleMiddle(v.title, max(1, width-2)))
		}
		out.WriteString(consoleClip(title, width, 1) + "\n")
		detail, project := "", ""
		if v.agent >= 0 {
			a := m.agents[v.agent]
			detail = consoleMiddle(a.ID, max(8, width/2)) + " · " + a.Config.Harness + " · " + consoleAgentStatus(a, m)
			project = a.Config.Workdir
			if reason := m.attentionReason(a.ID); m.attentionOnly {
				project = reason
			}
			if a.External {
				detail += " · original terminal"
				for _, o := range m.data.observed {
					if o.ID == a.ExternalRef {
						detail += " · " + o.TTY + " " + o.Pane
						break
					}
				}
			}
		} else {
			o := m.observedSessions()[v.observation]
			detail = o.ID + " · " + o.Harness + " · original terminal " + o.TTY
			project = o.Project
		}
		detailRows := 1
		if i == selected {
			detailRows = 2
		}
		out.WriteString(consoleMuted.Render(consoleClip("  "+consoleContent(detail, 500), width, detailRows)) + "\n")
		out.WriteString(consoleMuted.Render(consoleClip("  "+consoleContent(project, 500), width, 1)) + "\n\n")
	}
	if first > 0 || last < len(matches) {
		fmt.Fprintf(&out, "%s", consoleMuted.Render(fmt.Sprintf("↑ %d above · ↓ %d below", first, len(matches)-last)))
	}
	return consoleClip(out.String(), width, height)
}

func consoleSidebarWidth(width int) int {
	if width < 64 {
		return 0
	}
	if width < 85 {
		return 22
	}
	if width < 105 {
		return 24
	}
	return 30
}

func (m console) externalObservationDetails(reference string) string {
	for _, v := range m.data.observed {
		if v.ID == reference {
			return fmt.Sprintf("Observed ID: %s\nOriginal TTY: %s\nTmux session: %s\nTmux pane: %s\nAttach guidance: %s\nProject: %s", v.ID, v.TTY, v.TmuxSession, v.Pane, v.AttachCommand, v.Project) + observationWorkspaceNote(v)
		}
	}
	return "Observed ID: " + reference + "\nOriginal terminal is not currently observed; no attach location is guessed."
}

func observationWorkspaceNote(v sessioninventory.Session) string {
	if len(v.WorkspaceOrigins) == 0 {
		return ""
	}
	return "\nExisting workspace identities: " + strings.Join(v.WorkspaceOrigins, ", ") + "\nThis native process is already associated with these workspaces. A new bridge adds a separate coordination identity; it does not merge or move those identities."
}
