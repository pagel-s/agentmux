package main

import (
	"context"
	"encoding/json"

	"agentmux/internal/managed"
)

// workRegistry reads every broker page. Agent definitions may be configured in
// larger research runs, and even small registries can span transport-sized pages.
func workRegistry(ctx context.Context, client *managed.Client) ([]managed.AgentInfo, error) {
	agents := []managed.AgentInfo{}
	cursor := ""
	for {
		raw, err := workCall(ctx, client, "agent.list", map[string]any{"limit": 200, "after": cursor})
		if err != nil {
			return nil, err
		}
		var page struct {
			Agents []managed.AgentInfo `json:"agents"`
			Cursor string              `json:"cursor"`
		}
		if err = json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		agents = append(agents, page.Agents...)
		if len(page.Agents) == 0 || page.Cursor == "" || page.Cursor == cursor {
			return agents, nil
		}
		cursor = page.Cursor
	}
}
