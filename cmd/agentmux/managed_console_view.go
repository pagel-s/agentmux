package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"agentmux/internal/managed"
	"github.com/charmbracelet/lipgloss"
)

func consoleContent(value string, limit int) string {
	var out strings.Builder
	for i, r := range []rune(value) {
		if i >= limit {
			out.WriteString("…")
			break
		}
		if r == '\n' {
			out.WriteRune(r)
		} else if r == '\t' {
			out.WriteString("  ")
		} else if (!unicode.IsPrint(r) && r != 0x200c && r != 0x200d) || r == 0x061c || r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			quoted := strconv.QuoteRuneToASCII(r)
			out.WriteString(quoted[1 : len(quoted)-1])
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
func consoleTime(value string) string {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ""
	}
	return t.Local().Format("15:04:05")
}
func consoleClip(value string, width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return lipgloss.NewStyle().Width(width).MaxWidth(width).MaxHeight(height).Render(value)
}
func (m console) roster(width, height int) string {
	var out strings.Builder
	position := 0
	if len(m.agents) > 0 {
		position = m.selected + 1
	}
	if !m.contentFocus {
		out.WriteString(consoleSelected.Render(" SESSIONS · ↑/↓ ") + "\n")
	} else {
		out.WriteString(consoleMuted.Render("SESSIONS · Tab focus") + "\n")
	}
	out.WriteString(consoleMuted.Render(fmt.Sprintf("%d/%d · Ctrl+P switch", position, len(m.agents))) + "\n")
	rows := max(1, (height-4)/2)
	start := max(0, m.selected-rows+1)
	end := min(len(m.agents), start+rows)
	for i := start; i < end; i++ {
		a := m.agents[i]
		mode := a.Config.Interaction
		if a.External {
			mode = "external bridge"
		}
		if mode == "" {
			mode = "managed"
		}
		status := consoleAgentStatus(a, m)
		name := "  " + consoleMiddle(sessionTitle(a), max(1, width-2))
		if i == m.selected {
			name = consoleSelected.Render("› " + consoleMiddle(sessionTitle(a), max(1, width-2)))
		}
		_ = mode
		out.WriteString(consoleClip(name, width, 1) + "\n" + consoleMuted.Render(consoleMiddle(a.ID, max(1, width/2))+" · "+consoleMiddle(status, max(1, width-width/2-3))) + "\n")
	}
	if start > 0 || end < len(m.agents) {
		fmt.Fprintf(&out, "%s\n", consoleMuted.Render(fmt.Sprintf("↑ %d above · ↓ %d below", start, len(m.agents)-end)))
	}
	hint := m.nativeEntryHint()
	if m.contentFocus {
		hint = "Tab: focus sessions\nCtrl+P: find a session"
	}
	if m.data.headless {
		hint = "↑/↓ select · / find\nTasks output · Events state"
	}
	out.WriteString(consoleMuted.Render(hint))
	return consoleClip(out.String(), width, height)
}
func (m console) panel(width, height int) string {
	if m.workspacePicker != nil {
		return m.workspacePanel(width, height)
	}
	if m.ownerReview != nil {
		return m.ownerReviewPanel(width, height)
	}
	if m.composer == "actions" {
		return m.actionsPanel(width, height)
	}
	if m.teamAction != nil {
		return m.teamReviewPanel(width, height)
	}
	if m.nativeAction != nil {
		return m.nativeDeliveryPanel(width, height)
	}
	if m.composer == "find" {
		return m.sessionSwitcherPanel(width, height)
	}
	var out strings.Builder
	if len(m.agents) > 0 && m.tab != 5 && m.tab != 1 && m.tab != 2 && m.tab != 6 && m.tab != 7 && m.tab != 4 {
		a := m.agents[m.selected%len(m.agents)]
		fmt.Fprintf(&out, "%s\n%s\n\n", consoleAccent.Render(consoleClip(consoleContent(sessionTitle(a), 150), width, 1)), consoleMuted.Render(consoleClip(consoleContent(a.ID+" · "+a.Config.Harness+" · "+a.Config.Workdir, 300), width, 1)))
	}
	switch m.tab {
	case 0:
		out.WriteString(consoleMuted.Render(m.historyAvailability()))
		board := m.selectedBoard()
		boardLabel := "No shared board"
		if board != "" {
			boardLabel = "#" + board
		}
		filters := []string{"All coordination", boardLabel, m.selectedID() + " inbox"}
		messages := m.boardMessages()
		boardHints := fmt.Sprintf("%d on page %d · f scope · Shift+F search", len(messages), len(m.messageBack)+1)
		if m.messageMore {
			boardHints += " · PgDn older"
		}
		if len(m.messageBack) > 0 {
			boardHints += " · PgUp newer"
		}
		if len(messages) > 0 {
			boardHints += " · ↑/↓ select · r reply · Enter read"
		}
		fmt.Fprintf(&out, "%s  %s\n\n", consoleAccent.Render(filters[m.boardFilter%3]), consoleMuted.Render(boardHints))
		if len(messages) == 0 {
			empty := "No messages here yet."
			if m.selectedID() != "" {
				empty += "\n\nPress s to send to the selected agent.\n" + m.nativeEntryHint() + "."
			}
			if board != "" {
				empty += "\nPress b to post; +/- chooses a shared board."
			} else {
				empty += "\nPress 3 for Connections, then c to connect a pair and share a board."
			}
			out.WriteString(consoleMuted.Render(empty))
		}
		if len(messages) > 0 {
			start := m.messageIndex % len(messages)
			for i := start; i < len(messages); i++ {
				v := messages[i]
				to := strings.Join(v.To, ", ")
				if v.Board != "" {
					to = "#" + m.scopeLabel(v.Board)
				}
				prefix := "  "
				if i == start {
					prefix = consoleAccent.Render("› ")
				}
				fmt.Fprintf(&out, "%s%s %s %s\n", prefix, consoleAccent.Render(v.From), consoleMuted.Render("→ "+to), consoleMuted.Render(consoleTime(v.CreatedAt)))
				body := consoleClip(consoleContent(v.Text, 1500), width-3, 6)
				out.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render(body) + "\n\n")
				if lipgloss.Height(out.String()) > height-4 {
					break
				}
			}
		}
	case 1:
		hint := m.nativeEntryHint() + " · o details · Shift+L title"
		if m.data.headless {
			hint = "Headless managed worker · 5 Tasks output · 8 Events · o details"
		}
		fmt.Fprintf(&out, "%s\n%s\n\n", consoleAccent.Render(consoleClip(consoleContent(m.agentLabel(m.selectedID()), 200), width, 1)), consoleMuted.Render(consoleClip(hint, width, 1)))
		if len(m.agents) > 0 {
			a := m.agents[m.selected%len(m.agents)]
			if m.nativeNeedsRelaunch() && !m.offline {
				pane := m.data.runtime.Agents[a.ID]
				out.WriteString(consoleAccent.Render("NATIVE CLI "+strings.ToUpper(consoleContent(pane.State, 60))) + "\n")
				out.WriteString("Nothing restarts automatically.\n")
				out.WriteString(consoleMuted.Render("Enter or Shift+U reviews this session's relaunch.") + "\n\n")
				out.WriteString(consoleMuted.Render("LAST TERMINAL OUTPUT") + "\n")
				preview := strings.TrimSpace(m.data.preview)
				if m.data.selected != a.ID {
					preview = "Loading this session's terminal preview…"
				}
				if preview == "" {
					preview = "No captured output."
				}
				out.WriteString(consoleContent(preview, 6000))
				break
			}
			if m.offline && a.Config.Interaction != "interactive" {
				out.WriteString(consoleAccent.Render("Saved runtime snapshot · execution now unknown") + "\n")
			}
			out.WriteString(m.agentRuntimePanel(width) + "\n")
			fmt.Fprintf(&out, "%s\n%s\n%s\n%s\n\n", consoleClip(consoleContent(a.Config.Role, 300), width, 2), consoleMuted.Render(consoleClip(consoleContent(a.Config.Workdir, 200), width, 1)), consoleMuted.Render(a.Config.Harness+" · "+a.Config.Sandbox+" · network "+a.Config.Network), consoleMuted.Render(consoleContent(sessionOrigin(a)+" · added "+sessionAdded(a.CreatedAt), 200)))
			if a.External {
				out.WriteString(consoleContent(m.externalObservationDetails(a.ExternalRef), 4000) + "\n")
				fmt.Fprintf(&out, "%s  %s\n\n%s\n\n%s\n", consoleAccent.Render("EXTERNAL NATIVE SESSION"), consoleMuted.Render(observationStatus(a.ExternalRef, m.data.observed)), consoleContent("Use its original terminal. Helper: "+m.bridgeHelper(a.ID), 500), consoleMuted.Render("Registration prepares a bridge; it does not prove the native agent read the setup.\nCopy the setup below into its existing conversation yourself."))
				out.WriteString("\n" + consoleContent(bridgeInstructions(m.state, a.ID), 6000))
				break
			}
		}
		if m.data.headless {
			out.WriteString(consoleMuted.Render("HEADLESS MANAGED WORKER\nInspect Tasks for results and reported traces; Events for lifecycle.\nThere is no native terminal pane for this bounded runner.") + "\n")
			if m.data.research != nil {
				out.WriteString(consoleMuted.Render("Research controls in Events apply at phase barriers.") + "\n")
			}
			break
		}
		previewLabel := "LIVE TERMINAL PREVIEW"
		if m.nativeNeedsRelaunch() {
			previewLabel = "LAST TERMINAL OUTPUT · native CLI " + m.data.runtime.Agents[m.selectedID()].State
		} else if m.offline {
			previewLabel = "SAVED TERMINAL PREVIEW · current process state unknown"
		}
		preview := strings.TrimSpace(m.data.preview)
		if m.data.selected != m.selectedID() {
			previewLabel = "TERMINAL PREVIEW · loading"
			preview = "Loading this session's terminal preview…"
		}
		out.WriteString(consoleMuted.Render(previewLabel) + "\n")
		if preview == "" {
			preview = "Waiting for terminal output…"
			if m.nativeNeedsRelaunch() {
				preview = "No captured output. Enter opens a deliberate same-ID relaunch review."
			}
		}
		out.WriteString(consoleContent(preview, 6000))
	case 2:
		if m.data.boundaries.Agent != m.selectedID() {
			out.WriteString(consoleMuted.Render("Loading this session's current boundaries…"))
			break
		}
		out.WriteString(consoleAccent.Render("Connections") + consoleMuted.Render(fmt.Sprintf("  revision %d", m.data.boundaries.Version)) + "\n\n")
		if len(m.agents) > 0 {
			target := m.agents[m.target%len(m.agents)].ID
			fmt.Fprintf(&out, "%s\n%s\n%s\n%s\n\n", consoleMuted.Render("SENDER · Tab → sessions"), consoleAccent.Render(consoleClip(consoleContent(m.agentLabel(m.selectedID()), 300), width, 1)), consoleMuted.Render("RECIPIENT · ↑/↓ here · h/l"), consoleAccent.Render(consoleClip(consoleContent(m.agentLabel(target), 300), width, 1)))
			for _, row := range []struct{ key, action, label string }{{"m", "message.send", "Messages"}, {"p", "ping.send", "Pings"}, {"i", "instruct", "Commands"}} {
				enabled := boundaryAllows(m.data.boundaries, target, row.action)
				value := "○  denied"
				if enabled {
					value = consoleAccent.Render("●  allowed")
				}
				reason := ""
				for _, grant := range m.data.boundaries.Targets {
					if grant.To == target {
						if enabled {
							reason = grant.Sources[row.action]
						} else {
							reason = grant.DeniedReasons[row.action]
						}
					}
				}
				fmt.Fprintf(&out, "  [%s]  %-13s %s %s\n", row.key, row.label, value, consoleMuted.Render(consoleClip(consoleContent(reason, 250), max(1, width-35), 1)))
			}
			out.WriteString("\n" + consoleMuted.Render("c connect scoped group · d isolate sender · M/P/I reset overrides\nCommands authorize tasks; 2 Sessions → Shift+D sets native delivery.\nGroups add grants; explicit owner overrides take precedence.\nExisting tasks stay accepted; cancel them in Tasks.\no reads full effective boundaries and stored controls"))
			out.WriteString("\n\n" + consoleAccent.Render("Workspace groups") + "\n")
			found := false
			for _, g := range m.data.controls.Groups {
				member := false
				for _, id := range g.Agents {
					member = member || id == m.selectedID()
				}
				if !member {
					continue
				}
				found = true
				fmt.Fprintf(&out, "%s · %s\n  %s\n  board #%s\n  memory %s\n", consoleContent(g.ID, 64), g.State, strings.Join(g.Members, ", "), g.Board, g.Memory)
			}
			if !found {
				out.WriteString(consoleMuted.Render("No group yet. Choose a recipient, then c."))
			}
		}
	case 3:
		if m.data.boundaries.Agent != m.selectedID() {
			out.WriteString(consoleMuted.Render("Loading this session's current memory boundaries…"))
			break
		}
		if m.memoryOpen {
			fmt.Fprintf(&out, "%s  %s\n\n", consoleAccent.Render("Memory / "+m.scopeLabel(m.memoryNamespace)), consoleMuted.Render("owner view · ↑/↓ entry · o read · e edit · n new · v refresh · Esc back"))
			if len(m.memoryEntries) == 0 {
				out.WriteString(consoleMuted.Render("No entries. Press n to create a shared note."))
			}
			selected := 0
			if len(m.memoryEntries) > 0 {
				selected = m.memoryEntry % len(m.memoryEntries)
			}
			rows := max(1, min(3, height-lipgloss.Height(out.String())-4))
			first := max(0, selected-rows+1)
			for i := first; i < len(m.memoryEntries) && i < first+rows; i++ {
				v := m.memoryEntries[i]
				line := fmt.Sprintf("  %-24s v%d", consoleContent(v.Key, 24), v.Version)
				if i == m.memoryEntry%len(m.memoryEntries) {
					line = consoleSelected.Render(line)
				}
				out.WriteString(line + "\n")
			}
			if len(m.memoryEntries) > 0 {
				v := m.memoryEntries[m.memoryEntry%len(m.memoryEntries)]
				out.WriteString("\n" + consoleContent(v.Value, 4000))
			}
		} else {
			scopes := append(append([]managed.BoundaryScope{}, m.data.boundaries.Boards...), m.data.boundaries.Memory...)
			hints := ""
			if len(scopes) > 0 {
				hints = "  ↑/↓ select · r/w access · Shift+R reset · o details"
				if len(m.data.boundaries.Memory) > 0 {
					hints += " · v memory"
				}
			}
			out.WriteString(consoleAccent.Render("Shared boundaries") + consoleMuted.Render(hints) + "\n\n")
			if len(scopes) == 0 {
				out.WriteString(consoleMuted.Render("No shared namespaces. Press 3 for Connections, then c to connect a pair.\n\n"))
			}
			selected := 0
			if len(scopes) > 0 {
				selected = m.scope % len(scopes)
				v := scopes[selected]
				fmt.Fprintf(&out, "Effective: read %s · write %s · %s\n", accessLabel(v.Read), accessLabel(v.Write), consoleContent(v.Reason, 250))
				if v.StoredRead != nil && v.StoredWrite != nil {
					fmt.Fprintf(&out, "Stored owner intent: read %s · write %s (membership still required)\n", accessLabel(*v.StoredRead), accessLabel(*v.StoredWrite))
				}
			}
			rows := max(1, height-lipgloss.Height(out.String())-3)
			first := max(0, selected-rows+1)
			for i := first; i < len(scopes) && i < first+rows; i++ {
				v := scopes[i]
				kind := "board"
				if i >= len(m.data.boundaries.Boards) {
					kind = "memory"
				}
				line := fmt.Sprintf("  %-6s read %-7s write %-7s %s", kind, accessLabel(v.Read), accessLabel(v.Write), m.scopeLabel(v.Name))
				if v.Source == "team" {
					line += " · membership-managed"
				}
				if i == m.scope%len(scopes) {
					line = consoleSelected.Render(line)
				}
				out.WriteString(consoleClip(line, width, 1) + "\n")
			}
			out.WriteString("\n" + consoleMuted.Render("Private memory is separate for every agent.\nRevocation prevents future reads; past knowledge remains."))
		}
	case 4:
		taskHints := ""
		if len(m.data.activity.Tasks) > 0 {
			taskHints = "  ↑/↓ select · Enter read · r follow up · c cancel"
		}
		out.WriteString(consoleAccent.Render("Tasks & results") + consoleMuted.Render(taskHints) + "\n\n")
		historyHint := fmt.Sprintf("Page %d · f search · y to · z state", len(m.taskBack)+1)
		if m.taskExactID != "" {
			historyHint = "Exact task: " + consoleContent(m.taskExactID, 256) + " · f/y/z browse history"
		}
		if m.taskRecipient != "" {
			historyHint += " · to " + m.taskRecipient
		}
		if m.taskState != "" {
			historyHint += " · " + m.taskState
		}
		if m.taskMore {
			historyHint += " · PgDn older"
		}
		if len(m.taskBack) > 0 {
			historyHint += " · PgUp newer"
		}
		out.WriteString(consoleMuted.Render(historyHint) + "\n")
		if m.taskExactID != "" && len(m.data.activity.Tasks) > 0 {
			v := m.data.activity.Tasks[m.task%len(m.data.activity.Tasks)]
			if recovery := consoleTaskRecovery(v); recovery != nil {
				out.WriteString("Recovery: " + consoleContent(recovery.Delivery, 100) + " · Ctrl+K reviews recovery actions for this exact task/version.\n" + consoleContent(recovery.Reason, 1000) + "\n")
			}
		}
		if m.taskQuery != "" {
			out.WriteString("Search: " + consoleContent(m.taskQuery, 200) + "\n")
		}
		out.WriteString("\n")
		selected := 0
		if len(m.data.activity.Tasks) > 0 {
			selected = m.task % len(m.data.activity.Tasks)
		}
		rows := max(1, min(3, height-lipgloss.Height(out.String())-5))
		first := max(0, selected-rows+1)
		for i := first; i < len(m.data.activity.Tasks) && i < first+rows; i++ {
			v := m.data.activity.Tasks[i]
			line := fmt.Sprintf("  %-16s %-12s %s", v.To, v.State, v.Delivery)
			if i == m.task%len(m.data.activity.Tasks) {
				line = consoleSelected.Render(line)
			}
			out.WriteString(line + "\n")
		}
		if len(m.data.activity.Tasks) > 0 {
			v := m.data.activity.Tasks[m.task%len(m.data.activity.Tasks)]
			out.WriteString("\n" + consoleMuted.Render(v.ID) + fmt.Sprintf(" · version %d\n", v.Version))
			if m.taskExactID != "" {
				out.WriteString("Instruction · Enter reads full content\n" + consoleContent(v.Text, 2000) + "\n")
			}
			out.WriteString("Result · o reads full content\n" + consoleContent(v.Result, 4000))
			if len(v.Usage) > 0 {
				out.WriteString("\n\n" + consoleMuted.Render("Provider-reported usage: ") + consoleContent(string(v.Usage), 500))
			}
		} else {
			if m.taskExactID != "" {
				out.WriteString(consoleMuted.Render("Exact task unavailable. Refresh or verify this task in the selected workspace; no other task is substituted."))
			} else {
				out.WriteString(consoleMuted.Render("No managed tasks yet.\nHuman-led native sessions use their own conversation UI."))
			}
		}
	case 5:
		observations := m.observedSessions()
		sessionHints := ""
		if len(observations) > 0 {
			sessionHints = "  ↑/↓ select · Enter details · c prepare bridge"
		}
		out.WriteString(consoleAccent.Render("Existing terminals") + consoleMuted.Render(sessionHints) + "\n\n")
		out.WriteString(consoleMuted.Render("Observed live terminals keep their native conversation and tools.\nObservation grants no communication rights and sends no input.") + "\n\n")
		if len(observations) == 0 {
			out.WriteString(consoleMuted.Render("No unregistered live TTY sessions found.\nRegistered bridges appear in Sessions under their assigned names.\nShift+N creates a native session. Generic wrappers may not be identifiable.\nDiscovery is local and best effort; custom tmux servers may be absent."))
		}
		first, count := max(0, m.observedSelected-4), 5
		if m.details != "" {
			first, count = m.observedSelected, 1
		}
		for i := first; i < len(observations) && i < first+count; i++ {
			v := observations[i]
			line := "  " + consoleContent(observedTitle(v), 160)
			if i == m.observedSelected {
				line = consoleSelected.Render(line)
			}
			fmt.Fprintf(&out, "%s\n  %s\n  %s\n  %s\n", line, consoleContent(v.ID+" · "+v.Harness+" · "+v.TTY, 200), consoleMuted.Render(consoleContent(v.Project, 200)), consoleMuted.Render(consoleContent("started "+sessionAdded(v.StartedAt)+" · "+v.TmuxSession, 200)))
		}
		if m.details != "" {
			out.WriteString("\n" + consoleAccent.Render("Manual setup / terminal details") + "\n" + consoleContent(m.details, 7000))
		}
	case 6:
		return m.teamsPanel(width, height)
	case 7:
		return m.eventsPanel(width, height)
	}
	return consoleClip(out.String(), width, height)
}
func (m console) View() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}
	header := consoleAccent.Render(" AGENTMUX ") + " " + consoleContent(m.data.runtime.Session, 60) + "  " + consoleMuted.Render(m.data.runtime.State)
	if m.offline {
		header = consoleAccent.Render(" AGENTMUX · BROKER OFFLINE ") + consoleMuted.Render("saved snapshot · writes disabled")
	}
	if !m.data.observedAt.IsZero() {
		header += consoleMuted.Render(" · observed " + m.data.observedAt.Local().Format("15:04:05"))
	}
	tabs := []string{"1 Board", "2 Sessions", "3 Connections", "4 Memory", "5 Tasks", "6 Existing terminals", "7 Teams", "8 Events"}
	if width < 100 {
		tabs[2] = "3 Links"
		tabs[5] = "6 Existing"
	}
	var navigation strings.Builder
	firstTab, lastTab := 0, len(tabs)
	navigationWidth := func() int {
		length := 0
		if firstTab > 0 {
			length += 2 // Left overflow cue and space.
		}
		if lastTab < len(tabs) {
			length += 2 // Space and right overflow cue.
		}
		for _, label := range tabs[firstTab:lastTab] {
			length += len(label) + 2
		}
		return length
	}
	// Keep the current view in the visible range instead of clipping the final
	// tabs. Drop the farther edge first, preserving nearby destinations.
	for lastTab-firstTab > 1 && navigationWidth() > width {
		if m.tab-firstTab > lastTab-1-m.tab {
			firstTab++
		} else {
			lastTab--
		}
	}
	if firstTab > 0 {
		navigation.WriteString(consoleMuted.Render("‹ "))
	}
	for i := firstTab; i < lastTab; i++ {
		label := tabs[i]
		if i == m.tab {
			navigation.WriteString(consoleSelected.Render(" " + label + " "))
		} else {
			navigation.WriteString(consoleMuted.Render(" " + label + " "))
		}
	}
	if lastTab < len(tabs) {
		navigation.WriteString(consoleMuted.Render(" ›"))
	}
	var footer strings.Builder
	if m.composer != "" && m.composer != "find" && m.composer != "actions" && m.reader == nil && m.workspacePicker == nil {
		label := m.composer + " → " + m.composeTarget
		if m.composer == "send" || m.composer == "reply" || m.composer == "message" {
			mode := m.composeMode
			if mode == "" {
				mode = "auto"
			}
			label = mode + " → " + m.composeTarget
		}
		if m.replyID != "" {
			label += " · reply " + m.replyID
		}
		cursor := m.textCursor
		if cursor < 0 || cursor > len(m.text) {
			cursor = len(m.text)
		}
		text := consoleEditor(m.text, cursor, width, 4)
		footer.WriteString(consoleAccent.Render(label) + "\n" + consoleClip(text, width, 4) + "\n" + consoleMuted.Render(m.editorHelp()) + "\n")
	}
	if m.busy {
		footer.WriteString(consoleMuted.Render("Applying…") + "\n")
	} else if m.message != "" && (m.reader == nil || !m.reader.help) {
		footer.WriteString(consoleClip(consoleContent(m.message, 500), width, 2) + "\n")
	}
	help := m.contextualKeys()
	if m.reader != nil && m.workspacePicker == nil {
		help = m.readerHelp()
	}
	footer.WriteString(consoleMuted.Render(help))
	bottom := consoleClip(footer.String(), width, height/2)
	bodyHeight := height - lipgloss.Height(bottom) - 4
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	var compact string
	if width < 64 && len(m.agents) > 0 && m.workspacePicker == nil && m.reader == nil && m.nativeAction == nil && m.composer != "find" {
		compact = consoleClip(consoleMuted.Render(fmt.Sprintf("Session %d/%d · Ctrl+P switch", m.selected+1, len(m.agents))), width, 1) + "\n" + consoleClip(consoleSelected.Render("› "+consoleContent(m.agentLabel(m.selectedID()), 300)), width, 1) + "\n"
		bodyHeight = max(1, bodyHeight-2)
	}
	var body string
	if m.workspacePicker != nil {
		body = m.workspacePanel(width, bodyHeight)
	} else if m.reader != nil {
		body = m.readerPanel(width, bodyHeight)
	} else if m.nativeAction != nil {
		body = m.nativeDeliveryPanel(width, bodyHeight)
	} else if m.ownerReview != nil {
		body = m.ownerReviewPanel(width, bodyHeight)
	} else if m.composer == "actions" {
		body = m.actionsPanel(width, bodyHeight)
	} else if m.composer == "find" {
		body = m.sessionSwitcherPanel(width, bodyHeight)
	} else if sideWidth := consoleSidebarWidth(width); sideWidth > 0 {
		separator := consoleMuted.Render("│")
		// Each divider row needs its own left gutter. Prefixing the complete
		// multiline block would indent only its first row.
		bar := strings.TrimSuffix(strings.Repeat(" "+separator+"\n", bodyHeight), "\n")
		right := lipgloss.NewStyle().PaddingLeft(2).Render(m.contentFocusLabel() + "\n" + m.panel(width-sideWidth-4, max(1, bodyHeight-1)))
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.roster(sideWidth, bodyHeight), bar, right)
	} else {
		body = m.contentFocusLabel() + "\n" + m.panel(width, max(1, bodyHeight-1))
	}
	return consoleClip(header, width, 1) + "\n" + consoleClip(navigation.String(), width, 1) + "\n\n" + compact + consoleClip(body, width, bodyHeight) + "\n" + bottom
}

func (m console) scopeLabel(name string) string {
	for _, team := range m.data.teams {
		if team.Board == name || team.Memory == name {
			return team.Name + " (consented team)"
		}
	}
	for _, g := range m.data.controls.Groups {
		if g.Board == name || g.Memory == name {
			labels := make([]string, 0, len(g.Agents))
			for _, id := range g.Agents {
				labels = append(labels, m.agentLabel(id))
			}
			return strings.Join(labels, " + ")
		}
	}
	return name
}

func (m console) editorHelp() string {
	if m.composer == "role" {
		return "Enter saves · Ctrl-R review latest role · Ctrl-G help · Esc back"
	}
	if m.composer == "memory-value" {
		return "Enter saves · Ctrl-R review latest · Ctrl-G help · Alt-Enter newline · Esc back"
	}
	if m.composer == "send" || m.composer == "reply" || m.composer == "message" {
		return "Enter queues · Ctrl-T note/task · Ctrl-G help · Alt-Enter newline · Esc cancels"
	}
	return "Enter submits · Ctrl-G help · Alt-Enter newline · Esc cancels · Ctrl-U clears"
}
