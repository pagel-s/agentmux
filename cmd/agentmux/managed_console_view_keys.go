package main

import tea "github.com/charmbracelet/bubbletea"

// selectView keeps all routes through the view strip consistent. Leaving a
// memory namespace invalidates its outstanding reads; entering Tasks requests
// the selected history page regardless of which navigation key was used.
func (m *console) selectView(index int) tea.Cmd {
	index = (index%8 + 8) % 8
	if m.tab != index {
		m.closeMemory()
	}
	m.tab = index
	m.contentFocus = index != 1
	if index == 4 || index == 0 || index == 7 {
		return m.fetch
	}
	return nil
}
