package main

func (m *console) moveContent(delta int) {
	switch m.tab {
	case 2:
		m.target = consoleStep(m.target, len(m.agents), delta)
	case 5:
		m.observedSelected = consoleStep(m.observedSelected, len(m.observedSessions()), delta)
		m.details = ""
	default:
		m.stepContent(delta)
	}
}

func (m console) contentFocusLabel() string {
	names := []string{"BOARD", "SESSION DETAILS", "RECIPIENT", "MEMORY", "TASKS", "EXISTING TERMINALS", "TEAMS", "EVENTS"}
	label := names[m.tab%len(names)]
	if m.contentFocus {
		return consoleSelected.Render(" "+label+" · ↑/↓ here ") + consoleMuted.Render(" · Tab: sessions")
	}
	return consoleMuted.Render(label + " · Tab: focus here")
}
