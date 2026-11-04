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
	if m.tab == 2 {
		if m.contentFocus {
			return consoleSelected.Render(" RECIPIENT · ↑/↓ choose ") + consoleMuted.Render(" · [ sender · Tab switch")
		}
		return consoleMuted.Render("RECIPIENT · ] focus · Tab switch")
	}
	if m.contentFocus {
		return consoleSelected.Render(" "+label+" · ↑/↓ here ") + consoleMuted.Render(" · Tab: sessions")
	}
	return consoleMuted.Render(label + " · Tab: focus here")
}

// Connections has two independent identities. Only the focused identity may
// claim the Up/Down keys; show direct focus keys beside both fields as well as
// in the sidebar, including when a narrow terminal hides that sidebar.
func (m console) connectionFocusLabel(recipient bool) string {
	label, key := "SENDER", "["
	if recipient {
		label, key = "RECIPIENT", "]"
	}
	if m.contentFocus == recipient {
		return consoleSelected.Render(" "+label+" · ↑/↓ choose ") + consoleMuted.Render(" · "+key+" focus")
	}
	return consoleMuted.Render(label + " · " + key + " focus")
}
