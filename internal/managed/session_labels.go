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

// ValidateSessionTitle validates presentation text, never agent instructions.
// Empty text clears a label and restores the interface's default display name.
func ValidateSessionTitle(title string) error {
	if !utf8.ValidString(title) || len(title) > 512 || utf8.RuneCountInString(title) > 120 {
		return errors.New("session title must be valid UTF-8, at most 120 characters and 512 bytes")
	}
	runes := []rune(title)
	for i, r := range runes {
		joiner := r == 0x200c || r == 0x200d
		if joiner && (i == 0 || i+1 == len(runes) || !unicode.IsPrint(runes[i-1]) || !unicode.IsPrint(runes[i+1]) || unicode.IsSpace(runes[i-1]) || unicode.IsSpace(runes[i+1])) {
			return errors.New("session title joiners must connect printable characters")
		}
		if (!unicode.IsPrint(r) && !joiner) || r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return errors.New("session title must be one printable line without control or bidirectional formatting characters")
		}
	}
	return nil
}

type sessionLabel struct {
	Title   string `json:"title"`
	Version int    `json:"version"`
}

func sessionPresentation(ctx context.Context, q queryer, info AgentInfo) (AgentInfo, error) {
	var label sessionLabel
	err := get(ctx, q, "agent_label", "agent_label_"+info.ID, &label)
	if err == nil {
		info.Title, info.TitleVersion = label.Title, label.Version
	} else if !errors.Is(err, sql.ErrNoRows) {
		return info, err
	}
	if info.CreatedAt == "" && info.Parent == "" {
		err = q.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='created_at'").Scan(&info.CreatedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return info, err
		}
	}
	return liveSessionRole(ctx, q, info)
}

func (s *Store) labelSession(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	if !a.owner() {
		return nil, forbidden()
	}
	var in struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Version int    `json:"version"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	if err := ValidateSessionTitle(in.Title); err != nil {
		return nil, invalid(err)
	}
	in.Title = strings.TrimSpace(in.Title)
	info, err := s.agent(ctx, q, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fault("not_found", "agent not found")
	}
	if err != nil {
		return nil, internal(err)
	}
	if info.State == "retired" {
		return nil, fault("conflict", "retired agent cannot be changed")
	}
	if in.Version != info.TitleVersion {
		return nil, fault("conflict", "session title changed; refresh before editing")
	}
	label := sessionLabel{Title: in.Title, Version: info.TitleVersion + 1}
	if err = save(ctx, q, "agent_label", "agent_label_"+in.ID, ownerID, in.ID, "owner", label); err != nil {
		return nil, internal(err)
	}
	if err = event(ctx, q, a, "agent.labelled", in.ID, map[string]any{"version": label.Version}); err != nil {
		return nil, internal(err)
	}
	info.Title, info.TitleVersion = label.Title, label.Version
	for key := range info.Config.Env {
		info.Config.Env[key] = "[redacted]"
	}
	return map[string]any{"agent": info}, nil
}
