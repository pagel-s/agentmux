package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
)

type consoleExposureRead struct {
	request     uint64
	agent, body string
	err         error
}

func (m *console) readExposureLedger() tea.Cmd {
	m.readRequest++
	request, agent, client := m.readRequest, m.selectedID(), m.client
	m.busy = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		result := consoleExposureRead{request: request, agent: agent}
		cursor := ""
		var out strings.Builder
		out.WriteString("Successful authenticated disclosures to " + agent + ". This ledger records what the broker returned, with exact versions and content hashes. It does not prove the recipient understood, retained or used the content. Owner reads do not count as agent exposure. Earlier content may remain in memory after access changes. The retained ledger is bounded.\n\n")
		for {
			raw, err := workCall(ctx, client, "exposure.list", map[string]any{"agent": agent, "limit": 200, "after": cursor})
			if err != nil {
				result.err = err
				return result
			}
			var page struct {
				Entries []managed.Exposure `json:"exposures"`
				Cursor  string             `json:"cursor"`
				More    bool               `json:"has_more"`
			}
			if err = json.Unmarshal(raw, &page); err != nil {
				result.err = err
				return result
			}
			for _, e := range page.Entries {
				fmt.Fprintf(&out, "%s · %s · %s\nReader %s · generation %d · version %d\nEntity %s · namespace %s · key %s\nSHA256 %s\nRoutes %s\n\n", e.At, e.ID, e.Kind, e.Reader, e.Generation, e.Version, e.EntityID, e.Namespace, e.Key, e.SHA256, strings.Join(e.Routes, ", "))
			}
			if !page.More {
				break
			}
			if page.Cursor == "" || page.Cursor == cursor {
				result.err = fmt.Errorf("ledger cursor did not advance")
				return result
			}
			cursor = page.Cursor
		}
		result.body = out.String()
		return result
	}
}
