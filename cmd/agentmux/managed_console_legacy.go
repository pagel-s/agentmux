package main

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
)

func (m *console) reviewLegacyCollaboration(ids []string) bool {
	for _, group := range m.data.controls.Groups {
		if group.ContributionsVersion >= 1 || len(group.Agents) != len(ids) {
			continue
		}
		match := true
		for _, id := range ids {
			if !teamContains(group.Agents, id) {
				match = false
			}
		}
		if !match {
			continue
		}
		action := consoleTeamAction{operation: "control.update", teamID: group.ID, teamName: "Legacy collaboration", version: m.data.controls.Version, groupAgents: append([]string{}, group.Agents...)}
		for _, edge := range m.data.controls.Edges {
			if edge.Source == "" && edge.From != edge.To && teamContains(group.Agents, edge.From) && teamContains(group.Agents, edge.To) {
				action.legacyEdges = append(action.legacyEdges, edge)
			}
		}
		m.teamAction = &action
		return true
	}
	return false
}
func (m console) legacyCollaborationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.teamAction = nil
		return m, nil
	case "q", "ctrl+c":
		return m, m.close()
	case "1":
		a := *m.teamAction
		a.legacyPolicy = "preserve"
		m.teamAction = &a
	case "2":
		a := *m.teamAction
		a.legacyPolicy = "group"
		m.teamAction = &a
	case "o":
		data, _ := json.MarshalIndent(m.teamAction.legacyEdges, "", "  ")
		m.reader = &consoleReader{title: "Exact historical pair overrides", body: string(data)}
	case "enter":
		if m.teamAction.legacyPolicy == "" {
			m.message = "Choose 1 preserve or 2 group explicitly before confirming; no permission was changed."
			return m, nil
		}
		if m.offline || m.busy {
			m.message = "Broker unavailable or applying a change. Migration choice kept; refresh before submitting."
			return m, nil
		}
		action := *m.teamAction
		m.teamAction = nil
		m.busy = true
		return m, m.mutate("control.update", action.args(), "Legacy choice "+action.legacyPolicy+" saved for "+strings.Join(action.groupAgents, ", ")+". Explicit denies remain; inspect effective Connections permissions.")
	}
	return m, nil
}
func (m console) legacyCollaborationPanel(width, height int) string {
	a := m.teamAction
	allows, denies := 0, 0
	for _, edge := range a.legacyEdges {
		if edge.Allow {
			allows++
		} else {
			denies++
		}
	}
	choice := a.legacyPolicy
	if choice == "" {
		choice = "none (required)"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n%s\n\nGroup: %s\nAgents: %s\nPermission version: %d\nHistorical pair allows %d · denies %d\nChoice: %s\n\n", consoleAccent.Render("Review legacy collaboration"), consoleMuted.Render("1 preserve · 2 group · Enter applies · o edges · Esc cancels"), consoleContent(a.teamID, 80), consoleContent(strings.Join(a.groupAgents, ", "), 250), a.version, allows, denies, consoleAccent.Render(choice))
	out.WriteString("1 preserve: keep old pair allows as direct owner grants.\n2 group: remove old pair allows; use group/configuration policy.\nAll explicit denies, other pairs and stored scopes remain.\nThis reconnects exactly the listed original members.\n\n")
	fmt.Fprintf(&out, "Choice: %s\n%s", consoleAccent.Render(choice), consoleMuted.Render("1/2 chooses · Enter applies exact reviewed version\no reads historical edges · Esc cancels"))
	return consoleClip(out.String(), width, height)
}
