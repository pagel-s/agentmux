package managed

import (
	"context"
	"encoding/json"
)

func (s *Store) listEvents(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	if !a.owner() {
		return nil, forbidden()
	}
	var in struct {
		Agent string `json:"agent"`
		Actor string `json:"actor"`
		Type  string `json:"type"`
		Query string `json:"query"`
		After string `json:"after"`
		Order string `json:"order"`
		Limit int    `json:"limit"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	if in.Order != "" && in.Order != "asc" && in.Order != "desc" {
		return nil, fault("invalid", "order must be asc or desc")
	}
	if len(in.Query) > 4096 || len(in.Type) > 128 || len(in.Actor) > 128 || len(in.Agent) > 128 {
		return nil, fault("invalid", "event filters exceed bounds")
	}
	if in.Limit <= 0 || in.Limit > 1000 {
		in.Limit = 200
	}
	condition := ""
	values := []any{}
	if in.After != "" {
		var seq int64
		if err := q.QueryRowContext(ctx, "SELECT seq FROM records WHERE kind='event' AND id=?", in.After).Scan(&seq); err != nil {
			return nil, fault("invalid", "invalid event cursor")
		}
		cmp := "<"
		if in.Order == "asc" {
			cmp = ">"
		}
		condition += " AND seq" + cmp + "?"
		values = append(values, seq)
	}
	if in.Actor != "" {
		condition += " AND actor=?"
		values = append(values, in.Actor)
	}
	if in.Type != "" {
		condition += " AND json_extract(data,'$.type')=?"
		values = append(values, in.Type)
	}
	if in.Agent != "" {
		condition += " AND (actor=? OR json_extract(data,'$.entity_id')=?"
		values = append(values, in.Agent, in.Agent)
		for _, field := range []string{"agent", "from", "to", "subject", "owner", "before_owner", "pending_owner"} {
			condition += " OR json_extract(data,'$.details." + field + "')=?"
			values = append(values, in.Agent)
		}
		for _, field := range []string{"recipients", "agents", "members", "before_members", "invited", "before_invited"} {
			condition += " OR EXISTS (SELECT 1 FROM json_each(records.data,'$.details." + field + "') WHERE value=?)"
			values = append(values, in.Agent)
		}
		condition += ")"
	}
	if in.Query != "" {
		condition += " AND (instr(lower(id),lower(?))>0 OR instr(lower(actor),lower(?))>0 OR instr(lower(coalesce(json_extract(data,'$.type'),'')),lower(?))>0 OR instr(lower(coalesce(json_extract(data,'$.entity_id'),'')),lower(?))>0 OR EXISTS (SELECT 1 FROM json_tree(records.data,'$.details') AS detail WHERE detail.type NOT IN ('array','object','null') AND instr(lower(CAST(detail.value AS TEXT)),lower(?))>0))"
		values = append(values, in.Query, in.Query, in.Query, in.Query, in.Query)
	}
	order := "DESC"
	if in.Order == "asc" {
		order = "ASC"
	}
	condition += " ORDER BY seq " + order + " LIMIT ?"
	values = append(values, in.Limit+1)
	all, err := list[Event](ctx, q, "event", condition, values...)
	if err != nil {
		return nil, internal(err)
	}
	page := []Event{}
	size := 0
	cursor := in.After
	for _, e := range all {
		raw, _ := json.Marshal(e)
		if len(page) >= in.Limit || size+len(raw) > maxWireBytes/2 {
			break
		}
		page = append(page, e)
		size += len(raw)
		cursor = e.ID
	}
	return map[string]any{"events": page, "cursor": cursor, "has_more": len(all) > len(page)}, nil
}
