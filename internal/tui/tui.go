package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"agentmux/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tickMsg struct {
	Time time.Time
}

type model struct {
	session string
	panes   []tmux.PaneStatus
	kv      map[string]string
	logs    string
	err     error
	width   int
	height  int
	cursor  int
	focus   string
}

type dataMsg struct {
	panes    []tmux.PaneStatus
	kv       map[string]string
	logs     string
	selected string
	err      error
}

func NewModel(session string) tea.Model {
	return model{
		session: session,
		kv:      make(map[string]string),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.fetchData,
		tick(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, m.fetchData
		case "down", "j":
			if m.cursor < len(m.panes)-1 {
				m.cursor++
			}
			return m, m.fetchData
		case "f":
			if pane, ok := m.selectedPane(); ok {
				m.focus = paneDisplayName(pane)
				return m, tea.Quit
			}
		case "r":
			if pane, ok := m.selectedPane(); ok {
				_ = tmux.RestartPaneByName(m.session, paneDisplayName(pane), false)
			}
			return m, m.fetchData
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, m.fetchData

	case dataMsg:
		m.panes = msg.panes
		m.kv = msg.kv
		m.logs = msg.logs
		m.err = msg.err
		if msg.selected != "" {
			m.cursor = cursorForPaneName(m.panes, msg.selected)
		} else if len(m.panes) == 0 {
			m.cursor = 0
		} else if m.cursor >= len(m.panes) {
			m.cursor = len(m.panes) - 1
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.fetchData, tick())
	}

	return m, nil
}

func (m model) fetchData() tea.Msg {
	panes, err := tmux.Status(m.session)
	if err != nil {
		return dataMsg{err: err}
	}

	kv, err := tmux.ListKV(m.session, false)
	if err != nil {
		return dataMsg{err: err}
	}

	selected := m.selectedPaneName()
	if selected == "" && len(panes) > 0 {
		selected = paneDisplayName(panes[clamp(m.cursor, 0, len(panes)-1)])
	}

	logs := ""
	if selected != "" {
		logs, err = tmux.CapturePaneByName(m.session, selected, m.logTailLines(), false, false)
		if err != nil {
			logs = fmt.Sprintf("capture error: %v", err)
		}
	}

	return dataMsg{
		panes:    panes,
		kv:       kv,
		logs:     logs,
		selected: selected,
	}
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg{Time: t}
	})
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7D56F4")).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4"))

	runningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
	deadStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
	paneBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	detailBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#1D4ED8")).
			Padding(0, 1)

	kvBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#444444")).
			Padding(0, 1)

	subtleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))
	accentStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#60A5FA")).Bold(true)
	warningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	logToolStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#93C5FD"))
	logTraceStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#C4B5FD"))
)

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress 'q' to quit.", m.err)
	}

	if len(m.panes) == 0 {
		return "Loading dashboard..."
	}

	totalWidth := maxInt(m.width, 110)
	totalHeight := maxInt(m.height, 30)
	leftWidth := clamp(totalWidth/3, 30, 44)
	rightWidth := maxInt(totalWidth-leftWidth-1, 56)
	kvHeight := clamp(totalHeight/4, 8, 12)
	detailHeight := maxInt(totalHeight-kvHeight-8, 14)

	header := titleStyle.Render("Agentmux Mission Control") + "  Session: " + headerStyle.Render(m.session)
	if pane, ok := m.selectedPane(); ok {
		header += "  Selected: " + headerStyle.Render(paneDisplayName(pane))
	}

	left := paneBoxStyle.Width(leftWidth).Height(totalHeight - 8).Render(m.renderPaneList())
	rightTop := detailBoxStyle.Width(rightWidth).Height(detailHeight).Render(m.renderSelectedPane(rightWidth-4, detailHeight-2))
	rightBottom := kvBoxStyle.Width(rightWidth).Height(kvHeight).Render(m.renderKVSummary(rightWidth-4, kvHeight-2))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, lipgloss.JoinVertical(lipgloss.Left, rightTop, rightBottom))

	footer := "\n" + subtleStyle.Render("q: quit | f: attach to selected pane | r: restart selected pane | up/down: navigate | selected pane shows live output and MCP/tool activity")
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m model) renderPaneList() string {
	var paneList strings.Builder
	paneList.WriteString(headerStyle.Render("AGENTS / PANES") + "\n\n")
	for i, pane := range m.panes {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}

		status := runningStyle.Render("●")
		if pane.Dead {
			status = deadStyle.Render("●")
		}

		line := fmt.Sprintf("%s %s %s [%s]", cursor, status, paneDisplayName(pane), pane.PaneID)
		meta := providerSummary(pane)
		if pane.IntegrationMode != "" {
			meta += " · " + pane.IntegrationMode
		}
		metaLine := "    " + meta
		if i == m.cursor {
			paneList.WriteString(selectedStyle.Render(line) + "\n")
			paneList.WriteString(selectedStyle.Render(metaLine) + "\n")
		} else {
			paneList.WriteString(line + "\n")
			paneList.WriteString(subtleStyle.Render(metaLine) + "\n")
		}
	}
	return paneList.String()
}

func (m model) renderSelectedPane(width int, height int) string {
	selected, ok := m.selectedPane()
	if !ok {
		return "No pane selected."
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render("SELECTED PANE") + "\n\n")
	b.WriteString(accentStyle.Render(paneDisplayName(selected)))
	if selected.Title != "" && selected.Title != paneDisplayName(selected) {
		b.WriteString("  " + subtleStyle.Render(selected.Title))
	}
	b.WriteString("\n")
	b.WriteString(renderPaneState(selected) + "\n")
	b.WriteString(renderPaneMeta("Window", fmt.Sprintf("%d (%s)", selected.WindowIndex, selected.WindowName)) + "\n")
	b.WriteString(renderPaneMeta("Provider", providerSummary(selected)) + "\n")
	if selected.IntegrationMode != "" {
		b.WriteString(renderPaneMeta("Integration", selected.IntegrationMode) + "\n")
	}
	if selected.PersonaChannel != "" {
		b.WriteString(renderPaneMeta("Persona", selected.PersonaChannel) + "\n")
	}
	if selected.ToolChannel != "" {
		b.WriteString(renderPaneMeta("Tools", selected.ToolChannel) + "\n")
	}
	if selected.ReasoningEffort != "" {
		b.WriteString(renderPaneMeta("Reasoning", selected.ReasoningEffort) + "\n")
	}
	if selected.Pid > 0 {
		process := "pid " + strconv.Itoa(selected.Pid)
		if selected.CPU > 0 || selected.Memory > 0 {
			process += fmt.Sprintf(" · cpu %.1f%% · mem %.1f%%", selected.CPU, selected.Memory)
		}
		b.WriteString(renderPaneMeta("Process", process) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(headerStyle.Render("RECENT OUTPUT") + "\n")
	b.WriteString(subtleStyle.Render("Live pane capture. MCP/tool activity appears here when providers emit it.") + "\n\n")
	b.WriteString(renderLogs(m.logs, width, maxInt(height-12, 4)))
	return b.String()
}

func (m model) renderKVSummary(width int, height int) string {
	var kvList strings.Builder
	kvList.WriteString(headerStyle.Render("BLACKBOARD (KV)") + "\n\n")
	keys := make([]string, 0, len(m.kv))
	for key := range m.kv {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		kvList.WriteString(subtleStyle.Render("(empty)"))
		return kvList.String()
	}

	maxItems := maxInt(height-2, 1)
	for i, key := range keys {
		if i >= maxItems {
			kvList.WriteString(subtleStyle.Render(fmt.Sprintf("… %d more keys", len(keys)-i)))
			break
		}
		kvList.WriteString(fmt.Sprintf("%s: %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700")).Render(key), summarizeKVValue(m.kv[key], width-12)))
	}
	return kvList.String()
}

func RunDashboard(session string) error {
	if _, err := tmux.ListKV(session, false); err != nil {
		return fmt.Errorf("verify session %q: %w", session, err)
	}

	program := tea.NewProgram(NewModel(session), tea.WithAltScreen())
	finalModel, err := program.Run()
	if err != nil {
		return err
	}
	dashboard, ok := finalModel.(model)
	if !ok || dashboard.focus == "" {
		return nil
	}
	return tmux.AttachToPaneByName(session, dashboard.focus, false)
}

func (m model) selectedPane() (tmux.PaneStatus, bool) {
	if len(m.panes) == 0 {
		return tmux.PaneStatus{}, false
	}
	return m.panes[clamp(m.cursor, 0, len(m.panes)-1)], true
}

func (m model) selectedPaneName() string {
	pane, ok := m.selectedPane()
	if !ok {
		return ""
	}
	return paneDisplayName(pane)
}

func (m model) logTailLines() int {
	height := maxInt(m.height, 30)
	return clamp(height/2, 14, 48)
}

func paneDisplayName(pane tmux.PaneStatus) string {
	if pane.Name != "" {
		return pane.Name
	}
	if pane.PaneID != "" {
		return pane.PaneID
	}
	return "unnamed"
}

func cursorForPaneName(panes []tmux.PaneStatus, name string) int {
	if len(panes) == 0 {
		return 0
	}
	for i, pane := range panes {
		if paneDisplayName(pane) == name {
			return i
		}
	}
	return 0
}

func renderPaneState(pane tmux.PaneStatus) string {
	state := "RUNNING"
	style := successStyle
	if pane.Dead {
		state = "DEAD"
		style = warningStyle
	}
	if pane.PaneActive {
		state += " · ACTIVE"
	}
	return style.Render(state)
}

func renderPaneMeta(label string, value string) string {
	return headerStyle.Render(label+":") + " " + value
}

func providerSummary(pane tmux.PaneStatus) string {
	provider := pane.Provider
	if provider == "" {
		provider = "cmd"
	}
	if pane.Model != "" {
		return provider + ":" + pane.Model
	}
	return provider
}

func renderLogs(logs string, width int, height int) string {
	if strings.TrimSpace(logs) == "" {
		return subtleStyle.Render("(no recent output)")
	}
	lines := strings.Split(strings.TrimSuffix(logs, "\n"), "\n")
	if height > 0 && len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	for i, line := range lines {
		lines[i] = styleLogLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func styleLogLine(line string, width int) string {
	rendered := truncateDisplay(line, maxInt(width, 20))
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.Contains(line, "(MCP)") || strings.Contains(line, "agentmux - "):
		return logToolStyle.Render(rendered)
	case strings.HasPrefix(trimmed, "●") || strings.HasPrefix(trimmed, "•"):
		return logTraceStyle.Render(rendered)
	default:
		return rendered
	}
}

func summarizeKVValue(value string, width int) string {
	flat := strings.ReplaceAll(value, "\n", " ")
	flat = strings.ReplaceAll(flat, "\t", " ")
	flat = strings.Join(strings.Fields(flat), " ")
	return truncateDisplay(flat, maxInt(width, 20))
}

func truncateDisplay(value string, width int) string {
	runes := []rune(value)
	if width <= 0 || len(runes) <= width {
		return value
	}
	if width == 1 {
		return string(runes[:1])
	}
	return string(runes[:width-1]) + "…"
}

func clamp(value int, min int, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
