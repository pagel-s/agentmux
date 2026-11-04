package managed

import (
	"context"
	"encoding/json"
	"errors"
)

// TaskPairRequest binds one owner review to the permission revision and both
// native conversations/policies. It changes task delegation, not shared scopes.
type TaskPairRequest struct {
	Version int             `json:"version"`
	Mode    string          `json:"mode"`
	Agents  []TaskPairAgent `json:"agents"`
}

type TaskPairAgent struct {
	ID            string `json:"id"`
	PolicyVersion int    `json:"policy_version"`
	NativeSession string `json:"native_session"`
}

// ValidateTaskPairAgents is shared by the owner preview and transaction. The
// broker additionally rechecks current policies and native conversation IDs.
func ValidateTaskPairAgents(agents []AgentInfo, control ControlState) error {
	if len(agents) != 2 || agents[0].ID == agents[1].ID {
		return errors.New("pair requires two distinct active owned native sessions")
	}
	for _, agent := range agents {
		if agent.State != "active" || agent.External || agent.Config.NativeOnly || agent.Config.Interaction != "interactive" || agent.Parent != "" && !agent.OwnerManaged {
			return errors.New("pair requires active owned integrated native sessions")
		}
		if agent.Config.Harness != "claude" && agent.Config.Harness != "codex" && agent.Config.Harness != "pi" {
			return errors.New("pair requires a supported Claude, Codex or Pi native delivery adapter")
		}
		if contains(control.Independent, agent.ID) {
			return errors.New("an isolated session cannot be paired; review its reconnection separately")
		}
	}
	for _, edge := range control.Edges {
		if edge.Source == "isolation" && edge.Action == "instruct" &&
			(edge.From == agents[0].ID && edge.To == agents[1].ID || edge.From == agents[1].ID && edge.To == agents[0].ID) {
			return errors.New("the pair is isolated; review its reconnection separately")
		}
	}
	return nil
}

func (s *Store) taskPair(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	if !a.owner() {
		return nil, forbidden()
	}
	var in TaskPairRequest
	if err := strictJSON(r.Args, &in); err != nil {
		return nil, invalid(err)
	}
	if len(in.Agents) != 2 || in.Agents[0].ID == in.Agents[1].ID || in.Mode != "review" && in.Mode != "auto" {
		return nil, fault("invalid", "pair requires two distinct native sessions and review or auto delivery")
	}
	control, err := controls(ctx, q)
	if err != nil {
		return nil, internal(err)
	}
	if in.Version != control.Version {
		return nil, fault("conflict", "permissions changed; refresh and review the pair")
	}
	agents := make([]AgentInfo, 2)
	statuses := make([]NativeStatus, 2)
	for i, target := range in.Agents {
		if !s.ownerControllable(ctx, q, target.ID) {
			return nil, fault("invalid", "pair requires active owned native sessions")
		}
		agents[i], err = s.agent(ctx, q, target.ID)
		if err != nil {
			return nil, internal(err)
		}
		statuses[i], err = s.nativeStatusWithPending(ctx, q, target.ID, false)
		if err != nil {
			return nil, internal(err)
		}
		if target.PolicyVersion != statuses[i].Policy.Version || target.NativeSession != statuses[i].Session {
			return nil, fault("conflict", "native session or delivery policy changed; refresh and review the pair")
		}
		if statuses[i].Adapter != "" && (!statuses[i].Capabilities.Instructions || !statuses[i].Capabilities.Observations || !statuses[i].Capabilities.Completion) {
			return nil, fault("unsupported", "the current native adapter does not support tasks and returned results")
		}
	}
	if err := ValidateTaskPairAgents(agents, control); err != nil {
		return nil, invalid(err)
	}

	// Store.Call supplies one transaction and receipt for this whole operation.
	// Reuse existing permission/native-policy validation; no intermediate grant
	// or mode is visible, and any fault rolls every nested write back together.
	for i, target := range in.Agents {
		alreadyAllowed := false
		for _, edge := range control.Edges {
			if edge.From == target.ID && edge.To == in.Agents[1-i].ID && edge.Action == "instruct" {
				alreadyAllowed = edge.Source == "" && edge.Allow
				break
			}
		}
		if alreadyAllowed {
			continue
		}
		args, _ := json.Marshal(map[string]any{"version": control.Version, "kind": "edge", "from": target.ID, "to": in.Agents[1-i].ID, "action": "instruct", "allow": true})
		updated, problem := s.control(ctx, q, a, Request{Operation: "control.update", Args: args})
		if problem != nil {
			return nil, problem
		}
		control = updated.(ControlState)
	}
	policies := make(map[string]NativePolicy, 2)
	for i, target := range in.Agents {
		policy := statuses[i].Policy
		if policy.Mode != in.Mode || !policy.Results {
			args, _ := json.Marshal(map[string]any{"agent": target.ID, "version": policy.Version, "mode": in.Mode, "results": true})
			updated, problem := s.native(ctx, q, a, Request{Operation: "native.configure", Args: args})
			if problem != nil {
				return nil, problem
			}
			policy = updated.(map[string]any)["policy"].(NativePolicy)
		}
		policies[target.ID] = policy
	}
	if err := event(ctx, q, a, "control.pair", in.Agents[0].ID, map[string]any{
		"agents": []string{in.Agents[0].ID, in.Agents[1].ID}, "mode": in.Mode, "permission_version": control.Version,
		"policy_versions": map[string]int{in.Agents[0].ID: policies[in.Agents[0].ID].Version, in.Agents[1].ID: policies[in.Agents[1].ID].Version},
	}); err != nil {
		return nil, internal(err)
	}
	return map[string]any{"version": control.Version, "agents": []string{in.Agents[0].ID, in.Agents[1].ID}, "mode": in.Mode, "policies": policies}, nil
}
