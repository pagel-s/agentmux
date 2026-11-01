package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

type sessionRole struct {
	Role    string `json:"role"`
	Version int    `json:"version"`
}

func liveSessionRole(ctx context.Context, q queryer, info AgentInfo) (AgentInfo, error) {
	var role sessionRole
	err := get(ctx, q, "agent_role", "agent_role_"+info.ID, &role)
	if err == nil {
		info.Config.Role, info.RoleVersion = role.Role, role.Version
	} else if !errors.Is(err, sql.ErrNoRows) {
		return info, err
	}
	return info, nil
}

func (s *Store) updateSessionRole(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	if !a.owner() {
		return nil, forbidden()
	}
	var in struct {
		ID      string `json:"id"`
		Role    string `json:"role"`
		Version int    `json:"version"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	if !utf8.ValidString(in.Role) || len(in.Role) > 16384 || strings.IndexFunc(in.Role, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		return nil, fault("invalid", "role must be valid UTF-8 up to 16384 bytes without terminal control characters")
	}
	info, err := s.agent(ctx, q, in.ID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && info.State != "active" {
		return nil, fault("not_found", "active agent not found")
	}
	if err != nil {
		return nil, internal(err)
	}
	if info.Config.NativeOnly && in.Role != "" {
		return nil, fault("invalid", "native_only sessions do not accept injected roles; add a coordinated session instead")
	}
	if in.Version != info.RoleVersion {
		return nil, fault("conflict", "session role changed; refresh before editing")
	}
	role := sessionRole{Role: in.Role, Version: info.RoleVersion + 1}
	if err := save(ctx, q, "agent_role", "agent_role_"+in.ID, ownerID, in.ID, "owner", role); err != nil {
		return nil, internal(err)
	}
	// This notice is a broker inbox item, never terminal input. A native
	// conversation keeps running and observes the changed role when it reads
	// identity; managed workers receive the current role with their next claim.
	_, problem := s.send(ctx, q, a, Request{Operation: "message.send", Args: roleNotice(in.ID)})
	if problem != nil {
		return nil, problem
	}
	if err := event(ctx, q, a, "agent.role_changed", in.ID, map[string]any{"version": role.Version}); err != nil {
		return nil, internal(err)
	}
	info.Config.Role, info.RoleVersion = role.Role, role.Version
	for key := range info.Config.Env {
		info.Config.Env[key] = "[redacted]"
	}
	return map[string]any{"agent": info, "native_delivery": "broker notice only; native input and in-flight turns unchanged", "managed_delivery": "current role is captured by the next task claim"}, nil
}

func roleNotice(id string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"to": []string{id}, "text": "The owner updated your role. Read managed call identity for the current role and role_version before your next task. This notice grants no new permissions and is separate from native human input.", "reply_to": ""})
	return raw
}
