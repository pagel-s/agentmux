package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
)

type consoleHistoryPage struct {
	Scope, Agent, Board, Query, After, Cursor string
	More                                      bool
}

func (m console) messagePageRequest() consoleHistoryPage {
	p := consoleHistoryPage{Scope: "all", Query: m.messageQuery, After: m.messageAfter}
	if m.boardFilter == 1 {
		p.Scope = "board"
		p.Board = m.selectedBoard()
	}
	if m.boardFilter == 2 {
		p.Scope = "inbox"
		p.Agent = m.selectedID()
	}
	return p
}
func (m console) eventPageRequest() consoleHistoryPage {
	p := consoleHistoryPage{Query: m.eventQuery, After: m.eventAfter}
	if m.eventScope {
		p.Agent = m.selectedID()
	}
	return p
}
func sameHistoryRequest(a, b consoleHistoryPage) bool {
	return a.Scope == b.Scope && a.Agent == b.Agent && a.Board == b.Board && a.Query == b.Query && a.After == b.After
}
func (m console) fetchHistory(d *consoleData) {
	if d.err != nil {
		return
	}
	p := m.messagePageRequest()
	op := "message.list"
	if m.tab == 7 {
		p = m.eventPageRequest()
		op = "event.list"
	}
	if m.tab != 0 && m.tab != 7 {
		return
	}
	args := map[string]any{"limit": 40, "order": "desc", "query": p.Query, "after": p.After, "agent": p.Agent}
	if m.tab == 0 {
		args["scope"] = p.Scope
		args["board"] = p.Board
		if p.Scope == "board" && p.Board == "" {
			d.activity.Messages = nil
			d.messagePage = &p
			return
		}
	}
	if m.tab == 0 {
		d.messagePage = &p
	} else {
		d.eventPage = &p
	}
	raw, err := workCall(context.Background(), m.client, op, args)
	if err != nil {
		d.historyErr = err
		// An unsupported history API must not discard the healthy roster or
		// disable owner controls. Only the unqueried first page can use the
		// bounded activity snapshot; never present it as a search/older page.
		if p.After != "" || p.Query != "" || m.tab == 7 && p.Agent != "" {
			if m.tab == 0 {
				d.activity.Messages = nil
			} else {
				d.activity.Events = nil
			}
		}
		return
	}
	var result struct {
		Messages []managed.Message `json:"messages"`
		Events   []managed.Event   `json:"events"`
		Cursor   string            `json:"cursor"`
		More     bool              `json:"has_more"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		d.historyErr = err
		if m.tab == 0 {
			d.activity.Messages = nil
		} else {
			d.activity.Events = nil
		}
		return
	}
	p.Cursor, p.More = result.Cursor, result.More
	if m.tab == 0 {
		d.activity.Messages = result.Messages
		d.messagePage = &p
	} else {
		d.activity.Events = result.Events
		d.eventPage = &p
	}
}

func (m console) historyAvailability() string {
	if m.data.historyErr == nil {
		return ""
	}
	return "Complete history unavailable; recent snapshot only when no search or older page is selected.\nThe running controller may be older. Use an updated controller/new workspace for scoped history.\n" + activityText(m.data.historyErr.Error(), 180) + "\n\n"
}
func (m console) historyPageKey(key string) (console, tea.Cmd, bool) {
	if m.tab != 0 && m.tab != 7 {
		return m, nil, false
	}
	after, cursor, more, back := &m.messageAfter, m.messageCursor, m.messageMore, &m.messageBack
	if m.tab == 7 {
		after, cursor, more, back = &m.eventAfter, m.eventCursor, m.eventMore, &m.eventBack
	}
	switch key {
	case "pgdown":
		if m.tab == 0 && (m.data.messagePage == nil || !sameHistoryRequest(*m.data.messagePage, m.messagePageRequest())) {
			return m, nil, true
		}
		if m.tab == 7 && (m.data.eventPage == nil || !sameHistoryRequest(*m.data.eventPage, m.eventPageRequest())) {
			return m, nil, true
		}
		if more && cursor != "" {
			*back = append(*back, *after)
			*after = cursor
			if m.tab == 7 {
				m.eventIndex = 0
				m.eventPinned = nil
			} else {
				m.messageIndex = 0
			}
			return m, m.fetch, true
		}
		return m, nil, true
	case "pgup":
		if len(*back) > 0 {
			*after = (*back)[len(*back)-1]
			*back = (*back)[:len(*back)-1]
			if m.tab == 7 {
				m.eventIndex = 0
				m.eventPinned = nil
			} else {
				m.messageIndex = 0
			}
			return m, m.fetch, true
		}
		return m, nil, true
	case "F":
		if m.tab == 7 {
			m.compose("event-search", "Search complete event history", "", m.eventQuery)
		} else {
			m.compose("message-search", "Search scoped message history", "", m.messageQuery)
		}
		return m, nil, true
	case "g":
		if m.tab == 7 {
			m.eventScope = !m.eventScope
			m.eventAfter = ""
			m.eventBack = nil
			m.eventPinned = nil
			m.syncHistoryScopes()
			return m, m.fetch, true
		}
	}
	return m, nil, false
}
func (m console) attentionReason(id string) string {
	if m.offline {
		return ""
	}
	if n, ok := m.data.nativeStates[id]; ok {
		if n.Policy.Mode == "review" && len(n.Pending) > 0 {
			return fmt.Sprintf("review · %d pending", len(n.Pending))
		}
		switch n.State {
		case "blocked", "approval", "failed", "uncertain":
			return n.State + " · " + n.Reason
		}
		if n.Policy.Mode != "manual" && (n.State == "unavailable" || n.State == "offline" || n.State == "limited") {
			return n.State + " · " + n.Reason
		}
		if n.Active != nil {
			switch n.Active.Phase {
			case "failed", "uncertain", "blocked":
				return n.Active.Phase + " · " + n.Active.Reason
			}
		}
	}
	if s, ok := m.data.taskSummary[id]; ok {
		if s.Uncertain > 0 {
			return fmt.Sprintf("%d uncertain tasks · complete retained history", s.Uncertain)
		}
		if s.Failed > 0 {
			return fmt.Sprintf("%d failed tasks · complete retained history", s.Failed)
		}
	}
	if m.data.taskSummary == nil {
		for _, t := range m.data.activity.Tasks {
			if t.To == id {
				if t.Delivery == "uncertain" || t.Delivery == "cancel_uncertain" {
					return "uncertain task " + t.ID
				}
				if t.State == "failed" {
					return "failed task " + t.ID
				}
			}
		}
	}
	if p, ok := m.data.runtime.Agents[id]; ok && p.State != "" && p.State != "running" {
		return "native process " + p.State
	}
	return ""
}

func historyScope(p consoleHistoryPage) consoleHistoryPage {
	p.After, p.Cursor = "", ""
	p.More = false
	return p
}
func (m *console) resetMessageHistory() {
	m.messageAfter, m.messageCursor = "", ""
	m.messageBack = nil
	m.messageMore = false
	m.messageIndex = 0
	m.data.activity.Messages = nil
	m.data.messagePage = nil
}
func (m *console) resetEventHistory() {
	m.eventAfter, m.eventCursor = "", ""
	m.eventBack = nil
	m.eventMore = false
	m.eventIndex = 0
	m.eventPinned = nil
	m.eventPinScope = nil
	m.data.activity.Events = nil
	m.data.eventPage = nil
}
func (m *console) resetSessionHistory(previous, current string) {
	if previous == current {
		return
	}
	m.scope = 0
	m.closeMemory()
	if m.boardFilter != 0 {
		m.resetMessageHistory()
	}
	if m.eventScope {
		m.resetEventHistory()
	}
	if m.taskRecipient != "" && m.taskExactID == "" {
		m.taskRecipient = current
		m.taskAfter, m.taskCursor = "", ""
		m.taskBack = nil
		m.taskMore = false
		m.task = 0
		m.data.activity.Tasks = nil
		m.data.taskPage = nil
	}
}

// Cursor and selected event identities belong to one exact scope/query. A
// cursor from a different recipient or board must never suppress its newer data.
func (m *console) syncHistoryScopes() {
	messages, events := historyScope(m.messagePageRequest()), historyScope(m.eventPageRequest())
	if m.messageHistoryScope != nil && !sameHistoryRequest(*m.messageHistoryScope, messages) {
		m.resetMessageHistory()
	}
	if m.eventHistoryScope != nil && !sameHistoryRequest(*m.eventHistoryScope, events) {
		m.resetEventHistory()
	}
	if m.eventPinned != nil && m.eventPinScope != nil && !sameHistoryRequest(*m.eventPinScope, events) {
		m.eventPinned = nil
		m.eventPinScope = nil
		m.eventIndex = 0
	}
	m.messageHistoryScope, m.eventHistoryScope = &messages, &events
	if m.eventPinned != nil && m.eventPinScope == nil {
		copy := events
		m.eventPinScope = &copy
	}
}

func (m console) fetchTaskSummary(d *consoleData) {
	if !m.attentionOnly || d.err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	active := map[string]bool{}
	for _, a := range d.agents {
		if a.State == "active" {
			active[a.ID] = true
		}
	}
	summaries := map[string]managed.TaskSummary{}
	cursor := ""
	for {
		raw, err := workCall(ctx, m.client, "task.summary", map[string]any{"limit": 200, "after": cursor})
		if err != nil {
			d.taskSummaryErr = err
			return
		}
		var page struct {
			Agents []managed.TaskSummary `json:"agents"`
			Cursor string                `json:"cursor"`
			More   bool                  `json:"has_more"`
		}
		if err = json.Unmarshal(raw, &page); err != nil {
			d.taskSummaryErr = err
			return
		}
		for _, s := range page.Agents {
			if active[s.Agent] {
				summaries[s.Agent] = s
			}
		}
		if !page.More {
			break
		}
		if page.Cursor == "" || page.Cursor == cursor {
			d.taskSummaryErr = fmt.Errorf("task summary cursor did not advance")
			return
		}
		cursor = page.Cursor
	}
	d.taskSummary = summaries
}
