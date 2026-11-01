package managed

import (
	"context"
	"sort"
)

// A move is the invitee's consent, not a manager's unilateral reassignment.
// Both compare-and-swap versions and both records share Call's transaction.
func (s *Store) moveTeamMembership(ctx context.Context, q queryer, a actor, source teamRecord, version int, targetID string, targetVersion int) (any, *Fault) {
	if a.owner() || source.Owner == a.ID || !contains(source.Members, a.ID) {
		return nil, fault("forbidden", "only a non-owner member can consent to moving its own membership")
	}
	if source.ID == targetID {
		return nil, fault("invalid", "choose a different destination team")
	}
	var target teamRecord
	if err := get(ctx, q, "team", targetID, &target); err != nil || target.State != "active" || !contains(target.Invited, a.ID) {
		return nil, fault("not_found", "active invited destination team not found")
	}
	if source.Version != version || target.Version != targetVersion {
		return nil, fault("conflict", "source or destination team version changed; refresh both teams")
	}
	if !s.canJoinTeams(ctx, q, a.ID) {
		return nil, forbidden()
	}
	info, err := s.agent(ctx, q, a.ID)
	if err != nil {
		return nil, forbidden()
	}
	if info.Parent != "" && info.Parent != ownerID && !s.teamAccess(ctx, q, info.Parent, target.Board, false) {
		return nil, fault("forbidden", "destination requires the parent's current team access")
	}
	var floor int64
	if err = q.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM records").Scan(&floor); err != nil {
		return nil, internal(err)
	}
	if target.JoinedAfter == nil {
		target.JoinedAfter = map[string]int64{}
	}
	target.JoinedAfter[a.ID] = floor
	target.Invited = without(target.Invited, a.ID)
	target.Members = append(target.Members, a.ID)
	sort.Strings(target.Members)
	// Invitation capacity was reserved at invite time; enforce it again against
	// malformed restored state rather than accepting an oversized destination.
	if len(target.Members)+len(target.Invited) > s.cfg.Limits.MaxTeamMembers {
		return nil, fault("limit", "destination team membership limit reached")
	}
	source.Members = without(source.Members, a.ID)
	if source.PendingOwner == a.ID {
		source.PendingOwner = ""
	}
	source.Version++
	target.Version++
	if err = save(ctx, q, "team", source.ID, source.Owner, "", "", source); err != nil {
		return nil, internal(err)
	}
	if err = save(ctx, q, "team", target.ID, target.Owner, "", "", target); err != nil {
		return nil, internal(err)
	}
	if err = advanceBoundaryRevision(ctx, q); err != nil {
		return nil, internal(err)
	}
	if err = event(ctx, q, a, "team.move", source.ID, map[string]any{"agent": a.ID, "to_id": target.ID, "version": source.Version, "to_version": target.Version}); err != nil {
		return nil, internal(err)
	}
	return map[string]any{"team": source.Team, "destination": target.Team}, nil
}
