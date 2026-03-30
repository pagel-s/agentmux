package tui

import (
	"fmt"
	"sort"
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
	err     error
	width   int
	height  int
	cursor  int // Selected pane index
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
		case "down", "j":
			if m.cursor < len(m.panes)-1 {
				m.cursor++
			}
		case "f":
			// Focus selected pane
			if m.cursor < len(m.panes) {
				_ = tmux.FocusPaneByName(m.session, m.panes[m.cursor].Name, false)
			}
		case "r":
			// Restart selected pane
			if m.cursor < len(m.panes) {
				_ = tmux.RestartPaneByName(m.session, m.panes[m.cursor].Name, false)
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case dataMsg:
		m.panes = msg.panes
		m.kv = msg.kv
		m.err = msg.err
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.fetchData, tick())
	}

	return m, nil
}

type dataMsg struct {
	panes []tmux.PaneStatus
	kv    map[string]string
	err   error
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
	return dataMsg{panes: panes, kv: kv}
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

	kvBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#444444")).
			Padding(0, 1)
)

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress 'q' to quit.", m.err)
	}

	if len(m.panes) == 0 {
		return "Loading dashboard..."
	}

	header := titleStyle.Render("Agentmux Mission Control") + "  Session: " + headerStyle.Render(m.session)
	
	// Left column: Panes
	var paneList strings.Builder
	paneList.WriteString(headerStyle.Render("AGENTS / PANES") + "\n\n")
	for i, p := range m.panes {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}

		status := runningStyle.Render("●")
		if p.Dead {
			status = deadStyle.Render("●")
		}

		name := p.Name
		if name == "" {
			name = fmt.Sprintf("unnamed (%s)", p.PaneID)
		}

		line := fmt.Sprintf("%s %s %-15s [%s]", cursor, status, name, p.PaneID)
		if i == m.cursor {
			paneList.WriteString(selectedStyle.Render(line) + "\n")
		} else {
			paneList.WriteString(line + "\n")
		}
	}

	// Right column: Blackboard
	var kvList strings.Builder
	kvList.WriteString(headerStyle.Render("BLACKBOARD (KV)") + "\n\n")
	keys := make([]string, 0, len(m.kv))
	for k := range m.kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		kvList.WriteString(" (empty)")
	}
	for _, k := range keys {
		kvList.WriteString(fmt.Sprintf("%s: %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700")).Render(k), m.kv[k]))
	}

	// Combine columns
	left := paneBoxStyle.Width(m.width/2 - 2).Height(m.height - 10).Render(paneList.String())
	right := kvBoxStyle.Width(m.width/2 - 2).Height(m.height - 10).Render(kvList.String())
	
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	// Footer: Help
	footer := "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("q: quit | f: focus | r: restart | up/down: navigate")

	// Assemble
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func RunDashboard(session string) error {
	// Probe session
	if _, err := tmux.ListKV(session, false); err != nil {
		return fmt.Errorf("verify session %q: %w", session, err)
	}

	p := tea.NewProgram(NewModel(session), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
