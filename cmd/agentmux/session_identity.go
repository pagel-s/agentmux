package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/sessioninventory"
)

func sessionProject(path string) string {
	if path == "" {
		return "Project unknown"
	}
	return filepath.Base(filepath.Clean(path))
}

func sessionTitle(a managed.AgentInfo) string {
	if a.Title != "" {
		return a.Title
	}
	return sessionProject(a.Config.Workdir) + " · " + a.Config.Harness
}

func sessionAdded(value string) string {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || t.IsZero() {
		return "time unknown"
	}
	return t.Local().Format("Jan 2 15:04")
}

func sessionOrigin(a managed.AgentInfo) string {
	if a.External {
		return "existing terminal"
	}
	if a.Parent != "" && !a.OwnerManaged {
		return "spawned by " + a.Parent
	}
	return "workspace"
}

func observedTitle(v sessioninventory.Session) string {
	if v.Title != "" && v.Title != v.Harness && v.Title != sessionProject(v.Project) {
		return v.Title
	}
	return sessionProject(v.Project) + " · " + v.Harness
}

func (m console) agentLabel(id string) string {
	for _, a := range m.agents {
		if a.ID == id {
			return sessionTitle(a) + " [" + id + "]"
		}
	}
	return id
}

type sessionMatch struct {
	agent, observation int
	title, detail      string
}

// Search is a picker: no action targets a hidden or partial match. Enter chooses
// a stable identity; changing a title never changes routing or native input.
func (m console) sessionMatches() []sessionMatch {
	query := strings.Fields(strings.ToLower(string(m.text)))
	matches := func(text string) bool {
		text = strings.ToLower(text)
		for _, word := range query {
			if !strings.Contains(text, word) {
				return false
			}
		}
		return true
	}
	out := []sessionMatch{}
	for i, a := range m.agents {
		title := sessionTitle(a)
		detail := fmt.Sprintf("%s · %s · %s · added %s\n%s", a.ID, a.Config.Harness, sessionOrigin(a), sessionAdded(a.CreatedAt), a.Config.Workdir)
		if matches(title + " " + detail + " " + a.Config.Role) {
			out = append(out, sessionMatch{agent: i, observation: -1, title: title, detail: detail})
		}
	}
	for i, v := range m.observedSessions() {
		title := observedTitle(v)
		detail := fmt.Sprintf("%s · %s · %s · started %s\n%s · %s", v.ID, v.Harness, v.TTY, sessionAdded(v.StartedAt), v.Project, v.TmuxSession)
		if matches(title + " " + detail) {
			out = append(out, sessionMatch{agent: -1, observation: i, title: title, detail: detail})
		}
	}
	return out
}

func (m console) sessionSearchPanel(width, height int) string {
	var out strings.Builder
	out.WriteString(consoleAccent.Render("Find a session") + "\n" + consoleMuted.Render("Task, project, provider or ID · ↑/↓ choose · Enter select · Esc cancel") + "\n\n")
	matches := m.sessionMatches()
	if len(matches) == 0 {
		out.WriteString("No sessions match. Change the search or Esc to return.")
	}
	selected := 0
	if len(matches) > 0 {
		selected = m.findIndex % len(matches)
	}
	first := max(0, selected-max(1, (height-5)/4)+1)
	for i := first; i < len(matches); i++ {
		v := matches[i]
		title := "  " + consoleContent(v.title, 180)
		if i == selected {
			title = consoleSelected.Render("› " + consoleContent(v.title, 180))
		}
		out.WriteString(consoleClip(title, width, 1) + "\n" + consoleMuted.Render(consoleClip(consoleContent(v.detail, 400), width, 2)) + "\n\n")
		if strings.Count(out.String(), "\n") >= height-3 {
			break
		}
	}
	return consoleClip(out.String(), width, height)
}
