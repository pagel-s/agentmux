package managed

import "context"

type boundaryReadKey struct{}

// Boundary evaluation is read-only and runs under the Store call lock. Reuse
// policy inputs only within this one evaluation; subsequent calls must read
// current revocation, membership and permission state again.
type boundaryReads struct {
	agents         map[string]boundaryAgentRead
	controlsLoaded bool
	controls       ControlState
	controlsErr    error
	teamsLoaded    bool
	teams          []Team
	teamsErr       error
	groupsLoaded   bool
	groups         []WorkspaceGroup
	groupsErr      error
}

type boundaryAgentRead struct {
	info AgentInfo
	err  error
}

func boundaryReadContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, boundaryReadKey{}, &boundaryReads{agents: make(map[string]boundaryAgentRead)})
}

func boundaryReadState(ctx context.Context) *boundaryReads {
	state, _ := ctx.Value(boundaryReadKey{}).(*boundaryReads)
	return state
}

func controls(ctx context.Context, q queryer) (ControlState, error) {
	state := boundaryReadState(ctx)
	if state == nil {
		return readControls(ctx, q)
	}
	if !state.controlsLoaded {
		state.controls, state.controlsErr = readControls(ctx, q)
		state.controlsLoaded = true
	}
	return state.controls, state.controlsErr
}

func (s *Store) agent(ctx context.Context, q queryer, id string) (AgentInfo, error) {
	state := boundaryReadState(ctx)
	if state == nil {
		return s.readAgent(ctx, q, id)
	}
	if prior, ok := state.agents[id]; ok {
		return prior.info, prior.err
	}
	info, err := s.readAgent(ctx, q, id)
	state.agents[id] = boundaryAgentRead{info: info, err: err}
	return info, err
}

func boundaryTeams(ctx context.Context, q queryer) ([]Team, error) {
	state := boundaryReadState(ctx)
	if state == nil {
		return list[Team](ctx, q, "team", "ORDER BY seq")
	}
	if !state.teamsLoaded {
		state.teams, state.teamsErr = list[Team](ctx, q, "team", "ORDER BY seq")
		state.teamsLoaded = true
	}
	return state.teams, state.teamsErr
}

func activeTeams(ctx context.Context, q queryer) ([]Team, error) {
	if boundaryReadState(ctx) == nil {
		return list[Team](ctx, q, "team", "AND json_extract(data,'$.state')='active' ORDER BY seq")
	}
	teams, err := boundaryTeams(ctx, q)
	if err != nil {
		return nil, err
	}
	active := []Team{}
	for _, team := range teams {
		if team.State == "active" {
			active = append(active, team)
		}
	}
	return active, nil
}
