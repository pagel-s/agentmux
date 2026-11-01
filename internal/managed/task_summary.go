package managed

import (
	"context"
	"encoding/json"
)

// TaskSummary aggregates the entire retained history for each recipient. It
// contains no task bodies or leases and does not infer native model activity.
type TaskSummary struct {
	Agent      string         `json:"agent"`
	Total      int            `json:"total"`
	Failed     int            `json:"failed"`
	Uncertain  int            `json:"uncertain"`
	Pending    int            `json:"pending"`
	Running    int            `json:"running"`
	States     map[string]int `json:"states"`
	Deliveries map[string]int `json:"deliveries"`
}

func (s *Store) taskSummary(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	if !a.owner() {
		return nil, forbidden()
	}
	var in struct {
		Agent string `json:"agent"`
		After string `json:"after"`
		Limit int    `json:"limit"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 200
	}
	if len(in.Agent) > 128 || len(in.After) > 128 {
		return nil, fault("invalid", "task summary agent filter exceeds bounds")
	}
	condition := " AND target<>'' AND target>?"
	args := []any{in.After}
	if in.Agent != "" {
		condition += " AND target=?"
		args = append(args, in.Agent)
	}
	args = append(args, in.Limit+1)
	// Page recipient identities first, then aggregate all tasks for those exact
	// recipients. LIMIT never truncates a recipient's historical failure counts.
	rows, err := q.QueryContext(ctx, `SELECT target,COALESCE(json_extract(data,'$.state'),''),COALESCE(json_extract(data,'$.delivery'),''),COUNT(*) FROM records WHERE kind='task' AND target IN (SELECT DISTINCT target FROM records WHERE kind='task'`+condition+` ORDER BY target LIMIT ?) GROUP BY target,json_extract(data,'$.state'),json_extract(data,'$.delivery') ORDER BY target`, args...)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	all := []TaskSummary{}
	for rows.Next() {
		var id, state, delivery string
		var n int
		if err = rows.Scan(&id, &state, &delivery, &n); err != nil {
			return nil, internal(err)
		}
		if len(all) == 0 || all[len(all)-1].Agent != id {
			all = append(all, TaskSummary{Agent: id, States: map[string]int{}, Deliveries: map[string]int{}})
		}
		entry := &all[len(all)-1]
		entry.Total += n
		entry.States[state] += n
		entry.Deliveries[delivery] += n
		if state == "failed" || delivery == "failed" {
			entry.Failed += n
		}
		switch delivery {
		case "uncertain", "cancel_uncertain":
			entry.Uncertain += n
		case "pending":
			entry.Pending += n
		case "dispatching", "cancel_requested":
			entry.Running += n
		}
	}
	if err = rows.Err(); err != nil {
		return nil, internal(err)
	}
	more := len(all) > in.Limit
	if more {
		all = all[:in.Limit]
	}
	cursor := in.After
	if len(all) > 0 {
		cursor = all[len(all)-1].Agent
	}
	return map[string]any{"agents": all, "cursor": cursor, "has_more": more}, nil
}
