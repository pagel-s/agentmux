package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentmux/internal/research"
	tea "github.com/charmbracelet/bubbletea"
)

func (m console) fetchStudy(d *consoleData) {
	if !m.studyFollow {
		d.study = m.data.study
		return
	}
	if d.research == nil && m.data.study == nil {
		return
	}
	path := m.state
	if m.data.study != nil {
		path = m.data.study.Output
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if view, err := research.StudyStatus(ctx, path); err == nil {
		d.study = &view
	}
}
func (m console) studyPanel(width int) string {
	s := m.data.study
	if s == nil {
		return ""
	}
	mode := "following"
	if !m.studyFollow {
		mode = "snapshot"
	}
	trial := "between trials"
	if s.Progress != nil {
		p := s.Progress.Progress
		trial = fmt.Sprintf("%s · attempt %d · round %d / %s", p.Trial, p.Attempt, p.Round, p.Phase)
	}
	return consoleClip(fmt.Sprintf("Study %s · %d/%d completed · %s\n%s\nControls remain pinned to this observer's exact workspace.\nCtrl+K → Open current study trial changes workspace explicitly.\n\n", mode, s.Completed, s.Total, s.State, trial), width, 6)
}
func (m *console) openStudyDetails() {
	s := m.data.study
	if s == nil {
		m.message = "No validated study output is attached."
		return
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Study output: %s\nState: %s\nCompleted: %d/%d\nCurrent trial state: %s\nLive broker: %t\n\n", s.Output, s.State, s.Completed, s.Total, s.CurrentState, s.Live)
	for _, r := range s.Results {
		fmt.Fprintf(&out, "%s · %s · attempt %d · %s\n", r.Trial, r.Condition, r.Attempt, r.State)
	}
	if s.Progress != nil {
		p := s.Progress.Progress
		fmt.Fprintf(&out, "\nCurrent trial: %s\nDeadline: %s\nAdmitted population:\n%s\nTask references: %v\nTask IDs: %v\n", p.Trial, p.Deadline, strings.Join(p.Population, "\n"), p.Tasks, p.TaskIDs)
	}
	out.WriteString("\nStudy follow reads verified study metadata through trial gaps. Control actions stay bound to the observer's current workspace. Open the current trial explicitly to inspect its live controls; prior view drafts remain in their own attachment.")
	m.reader = &consoleReader{title: "Whole study overview", body: out.String()}
}
func (m console) openCurrentStudyTrial() (tea.Model, tea.Cmd) {
	s := m.data.study
	if s == nil || !s.Live || s.CurrentState == "" {
		m.message = "No current live study trial is available; follow remains active through this gap."
		return m, nil
	}
	if s.CurrentState == m.state {
		m.tab = 7
		m.message = "Already observing the exact current trial."
		return m, m.fetch
	}
	return m.openWorkspace(s.CurrentState)
}
