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
	out.WriteString(consoleMuted.Render("AGENTS") + "\n\n")
	start := max(0, m.selected-max(1, (height-5)/4)+1)
	for i := start; i < len(m.agents); i++ {
		a := m.agents[i]
		mode := a.Config.Interaction
		if a.External {
			mode = "external bridge"
		}
		if mode == "" {
			mode = "managed"
		}
		status := a.State
		if a.External && a.State == "active" {
			status = observationStatus(a.ExternalRef, m.data.observed)
		}
		if pane, ok := m.data.runtime.Agents[a.ID]; ok {
			status = pane.State
		}
		if m.offline {
			status = "saved " + status
		}
		name := "  " + consoleContent(sessionTitle(a), 150)
		if i == m.selected {
			name = consoleSelected.Render("› " + consoleContent(sessionTitle(a), 150))
		}
		out.WriteString(consoleClip(name, width, 1) + "\n" + consoleMuted.Render(consoleClip("  "+a.ID+" · "+a.Config.Harness, width, 1)) + "\n" + consoleMuted.Render(consoleClip(consoleContent("  "+sessionProject(a.Config.Workdir)+" · "+mode+" · "+status, 300), width, 1)) + "\n\n")
		if lipgloss.Height(out.String()) >= height-2 {
			break
		}
	}
	hint := "↑/↓ select · / find\nEnter native CLI · L title"
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
	if m.teamAction != nil {
		return m.teamReviewPanel(width, height)
	}
	if m.composer == "find" {
		return m.sessionSearchPanel(width, height)
	}
	var out strings.Builder
	if len(m.agents) > 0 && m.tab != 5 && m.tab != 1 && m.tab != 6 && m.tab != 7 {
		a := m.agents[m.selected%len(m.agents)]
		fmt.Fprintf(&out, "%s\n%s\n\n", consoleAccent.Render(consoleClip(consoleContent(sessionTitle(a), 150), width, 1)), consoleMuted.Render(consoleClip(consoleContent(a.ID+" · "+a.Config.Harness+" · "+a.Config.Workdir, 300), width, 1)))
	}
	switch m.tab {
	case 0:
		board := m.selectedBoard()
		boardLabel := "No shared board"
		if board != "" {
			boardLabel = "#" + board
		}
		filters := []string{"All coordination", boardLabel, m.selectedID() + " inbox"}
		messages := m.boardMessages()
		fmt.Fprintf(&out, "%s  %s\n\n", consoleAccent.Render(filters[m.boardFilter%3]), consoleMuted.Render(fmt.Sprintf("%d recent · f filter · [/] select · r reply · o read all", len(messages))))
		if len(messages) == 0 {
			out.WriteString(consoleMuted.Render("No messages here yet.\n\nPress b to post to a shared board; +/- chooses the board.\nPress s to send to the selected agent.\nEnter opens its full native terminal.\nConnections → c enables a scoped collaboration group."))
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
		hint := "Enter worker console · Tasks output · o details · L title"
		if m.data.headless {
			hint = "Headless managed worker · Tasks output · Events lifecycle · o details · L title"
		}
		if len(m.agents) > 0 && m.agents[m.selected%len(m.agents)].Config.Interaction == "interactive" {
			hint = "Enter native terminal · o session details · L edit title"
		}
		fmt.Fprintf(&out, "%s\n%s\n\n", consoleAccent.Render(consoleClip(consoleContent(m.agentLabel(m.selectedID()), 200), width, 1)), consoleMuted.Render(hint))
		if len(m.agents) > 0 {
			a := m.agents[m.selected%len(m.agents)]
			if m.offline && a.Config.Interaction != "interactive" {
				out.WriteString(consoleAccent.Render("Saved runtime snapshot · execution now unknown") + "\n")
			}
			out.WriteString(m.agentRuntimePanel(width) + "\n")
			fmt.Fprintf(&out, "%s\n%s\n%s\n%s\n\n", consoleClip(consoleContent(a.Config.Role, 300), width, 2), consoleMuted.Render(consoleClip(consoleContent(a.Config.Workdir, 200), width, 1)), consoleMuted.Render(a.Config.Harness+" · "+a.Config.Sandbox+" · network "+a.Config.Network), consoleMuted.Render(consoleContent(sessionOrigin(a)+" · added "+sessionAdded(a.CreatedAt), 200)))
			if a.External {
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
		out.WriteString(consoleMuted.Render("LIVE TERMINAL PREVIEW") + "\n")
		preview := strings.TrimSpace(m.data.preview)
		if preview == "" {
			preview = "Waiting for terminal output…"
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
			fmt.Fprintf(&out, "%s  →  %s\n%s\n\n", consoleAccent.Render(consoleContent(m.agentLabel(m.selectedID()), 150)), consoleAccent.Render(consoleContent(m.agentLabel(target), 150)), consoleMuted.Render("←/→ recipient · ↑/↓ sender"))
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
			out.WriteString("\n" + consoleMuted.Render("c connect scoped group · d isolate sender · M/P/I reset overrides\nGroups add grants; explicit owner overrides take precedence.\nExisting tasks stay accepted; cancel them in Tasks.\no reads full effective boundaries and stored controls"))
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
			fmt.Fprintf(&out, "%s  %s\n\n", consoleAccent.Render("Memory / "+m.scopeLabel(m.memoryNamespace)), consoleMuted.Render("owner view · [/] entry · o read · e edit · n new · v refresh · Esc back"))
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
			out.WriteString(consoleAccent.Render("Shared boundaries") + consoleMuted.Render("  [/] select · r/w access · R reset · v memory · o details") + "\n\n")
			scopes := append(append([]managed.BoundaryScope{}, m.data.boundaries.Boards...), m.data.boundaries.Memory...)
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
		out.WriteString(consoleAccent.Render("Tasks & results") + consoleMuted.Render("  [/] select · o read all · r follow up · c cancel") + "\n\n")
		out.WriteString(consoleMuted.Render(fmt.Sprintf("History page %d · newest first · PgDn older / PgUp newer · f search", len(m.taskBack)+1)) + "\n")
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
			out.WriteString("\n" + consoleMuted.Render(v.ID) + "\nResult · o reads full result, instruction and trace\n" + consoleContent(v.Result, 4000))
			if len(v.Usage) > 0 {
				out.WriteString("\n\n" + consoleMuted.Render("Provider-reported usage: ") + consoleContent(string(v.Usage), 500))
			}
		} else {
			out.WriteString(consoleMuted.Render("No managed tasks yet.\nHuman-led native sessions use their own conversation UI."))
		}
	case 5:
		observations := m.observedSessions()
		out.WriteString(consoleAccent.Render("Existing sessions") + consoleMuted.Render("  ↑/↓ select · Enter details · c prepare bridge") + "\n\n")
		out.WriteString(consoleMuted.Render("Observed live terminals keep their native conversation and tools.\nObservation grants no communication rights and sends no input.") + "\n\n")
		if len(observations) == 0 {
			out.WriteString(consoleMuted.Render("No unregistered live TTY sessions found.\nRegistered bridges appear in Agents under their assigned names.\nN creates a native session. Generic interpreter wrappers may not be identifiable.\nDiscovery is local and best effort; custom tmux servers may be absent."))
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
	tabs := []string{"1 Board", "2 Agents", "3 Connections", "4 Memory", "5 Tasks", "6 Sessions", "7 Teams", "8 Events"}
	if width < 100 {
		tabs[2] = "3 Links"
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
	if m.composer != "" && m.reader == nil {
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
	} else if m.message != "" {
		footer.WriteString(consoleClip(consoleContent(m.message, 500), width, 2) + "\n")
	}
	help := "↑/↓ select · Enter native CLI · / find · L title · V role · Tab views\nN new · s send · b board · x revoke + descendants · q detach/close\nW workspaces · U relaunch · ? help · F1 workbench"
	if m.data.headless {
		help = "↑/↓ agent · / find · L title · Tab views · 7 Teams · 8 Events\nTasks: output/cancel · Events: lifecycle · x revoke tree · q close observer"
		if m.data.research != nil {
			help += " · P/R/S/T phase controls"
		}
	}
	if m.reader != nil {
		help = m.readerHelp()
	}
	footer.WriteString(consoleMuted.Render(help))
	bottom := consoleClip(footer.String(), width, height/2)
	bodyHeight := height - lipgloss.Height(bottom) - 4
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	var compact string
	if width < 85 && len(m.agents) > 0 && m.reader == nil {
		first := max(0, m.selected-1)
		for i := first; i < len(m.agents) && i < first+3; i++ {
			label := " " + consoleContent(sessionTitle(m.agents[i])+" ["+m.agents[i].ID+"]", 28) + " "
			if i == m.selected {
				compact += consoleSelected.Render(label)
			} else {
				compact += consoleMuted.Render(label)
			}
		}
		compact = consoleClip(compact, width, 1) + "\n"
		bodyHeight = max(1, bodyHeight-1)
	}
	var body string
	if m.reader != nil {
		body = m.readerPanel(width, bodyHeight)
	} else if width >= 85 {
		sideWidth := 30
		if width < 105 {
			sideWidth = 24
		}
		separator := consoleMuted.Render("│")
		bar := strings.TrimSuffix(strings.Repeat(separator+"\n", bodyHeight), "\n")
		right := lipgloss.NewStyle().PaddingLeft(2).Render(m.panel(width-sideWidth-4, bodyHeight))
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.roster(sideWidth, bodyHeight), " "+bar, right)
	} else {
		body = m.panel(width, bodyHeight)
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
