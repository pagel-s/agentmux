package managed

import (
	"agentmux/internal/managedconfig"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ResourceControls are live admission overlays. They never rewrite launch
// inputs, refund reservations, or claim to cap external provider billing.
type ResourceControls struct {
	Version           int                                  `json:"version"`
	SessionBudget     *managedconfig.Budget                `json:"session_budget,omitempty"`
	AgentBudgets      map[string]managedconfig.AgentBudget `json:"agent_budgets"`
	AdmissionDeadline string                               `json:"admission_deadline,omitempty"`
}

func readResourceControls(ctx context.Context, q queryer) (ResourceControls, error) {
	c := ResourceControls{Version: 1, AgentBudgets: map[string]managedconfig.AgentBudget{}}
	var raw json.RawMessage
	err := get(ctx, q, "resource_control", "live_resources", &raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	if e := checkStateRecoveryDigest(ctx, q, "resource_control", "live_resources", raw); e != nil {
		return c, e
	}
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err = strictJSON(raw, &c); err != nil {
		return c, err
	}
	if c.Version < 1 || c.AgentBudgets == nil {
		return c, errors.New("invalid persisted resource controls")
	}
	return c, nil
}
func (s *Store) effectiveBudgets(ctx context.Context, q queryer) (managedconfig.Budget, map[string]managedconfig.AgentBudget, int, error) {
	c, err := readResourceControls(ctx, q)
	budget := s.cfg.Budget
	if c.SessionBudget != nil {
		budget = *c.SessionBudget
	}
	return budget, c.AgentBudgets, c.Version, err
}
func (s *Store) admissionDeadline(ctx context.Context, q queryer) (time.Time, error) {
	d, err := s.deadline(ctx, q)
	if err != nil {
		return d, err
	}
	c, err := readResourceControls(ctx, q)
	if err != nil {
		return d, err
	}
	if c.AdmissionDeadline != "" {
		v, e := time.Parse(time.RFC3339Nano, c.AdmissionDeadline)
		if e != nil {
			return d, e
		}
		if d.IsZero() || v.Before(d) {
			d = v
		}
	}
	return d, nil
}
func validLiveAgentBudget(v managedconfig.AgentBudget, b managedconfig.Budget) error {
	if v.MaxTurnTokens < 0 || v.MaxTurnCostMicrousd < 0 || v.MaxSubtreeTokens < 0 || v.MaxSubtreeCostMicrousd < 0 {
		return fmt.Errorf("budget limits must not be negative")
	}
	if v.Enforcement != "" && v.Enforcement != "reservation" {
		return fmt.Errorf("only reservation enforcement is supported")
	}
	if (b.MaxTokens > 0 || v.MaxSubtreeTokens > 0) && v.MaxTurnTokens == 0 {
		return fmt.Errorf("token budget requires explicit max_turn_tokens")
	}
	if (b.MaxCostMicrousd > 0 || v.MaxSubtreeCostMicrousd > 0) && v.MaxTurnCostMicrousd == 0 {
		return fmt.Errorf("cost budget requires explicit max_turn_cost_microusd")
	}
	return nil
}
func (s *Store) updateCapacity(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	if !a.owner() {
		return nil, forbidden()
	}
	var in struct {
		Version          int                        `json:"version"`
		Agent            string                     `json:"agent"`
		SessionBudget    *managedconfig.Budget      `json:"session_budget"`
		AgentBudget      *managedconfig.AgentBudget `json:"agent_budget"`
		AdmissionSeconds *int64                     `json:"admission_seconds"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	c, err := readResourceControls(ctx, q)
	if err != nil {
		return nil, internal(err)
	}
	if in.Version != c.Version {
		return nil, fault("conflict", "resource limits changed; refresh before editing")
	}
	if in.SessionBudget == nil && in.AgentBudget == nil && in.AdmissionSeconds == nil {
		return nil, fault("invalid", "provide a session budget, agent budget or shorter admission deadline")
	}
	if (in.Agent != "") != (in.AgentBudget != nil) {
		return nil, fault("invalid", "agent and agent_budget must be supplied together")
	}
	charges, session, err := s.budgetCharges(ctx, q)
	if err != nil {
		return nil, internal(err)
	}
	budget := s.cfg.Budget
	if c.SessionBudget != nil {
		budget = *c.SessionBudget
	}
	if in.SessionBudget != nil {
		budget = *in.SessionBudget
		if budget.MaxTokens < 0 || budget.MaxCostMicrousd < 0 {
			return nil, fault("invalid", "session budget limits must not be negative")
		}
		if !withinBudget(session.Tokens, 0, budget.MaxTokens) || !withinBudget(session.CostMicrousd, 0, budget.MaxCostMicrousd) {
			return nil, fault("conflict", "new session budget is below permanently reserved usage")
		}
		c.SessionBudget = &budget
	}
	if in.AgentBudget != nil {
		if !s.knownAgent(ctx, q, in.Agent) {
			return nil, fault("not_found", "active agent not found")
		}
		used, e := s.subtreeCharge(ctx, q, in.Agent, charges)
		if e != nil {
			return nil, internal(e)
		}
		v := *in.AgentBudget
		if !withinBudget(used.Tokens, 0, v.MaxSubtreeTokens) || !withinBudget(used.CostMicrousd, 0, v.MaxSubtreeCostMicrousd) {
			return nil, fault("conflict", "new subtree budget is below permanently reserved usage")
		}
		c.AgentBudgets[in.Agent] = v
	}
	// Validate every current agent and every spawn template against the new
	// session limit, so adding a cap cannot silently strand a turn without a
	// declared reservation. Supply required agent limits in the same update.
	ids, e := s.controlAgentIDs(ctx, q)
	if e != nil {
		return nil, internal(e)
	}
	for _, id := range ids {
		info, e := s.agent(ctx, q, id)
		if e != nil {
			return nil, internal(e)
		}
		v := info.Config.Budget
		if override, ok := c.AgentBudgets[id]; ok {
			v = override
		}
		if err := validLiveAgentBudget(v, budget); err != nil {
			return nil, fault("invalid", id+": "+err.Error())
		}
	}
	for name, template := range s.cfg.Spawn.Templates {
		if err := validLiveAgentBudget(template.Budget, budget); err != nil {
			return nil, fault("invalid", "spawn template "+name+": "+err.Error())
		}
	}
	if in.AdmissionSeconds != nil {
		if *in.AdmissionSeconds < 0 || *in.AdmissionSeconds > 315360000 {
			return nil, fault("invalid", "admission_seconds must be 0–315360000")
		}
		proposed := time.Now().Add(time.Duration(*in.AdmissionSeconds) * time.Second)
		old, e := s.admissionDeadline(ctx, q)
		if e != nil {
			return nil, internal(e)
		}
		if !old.IsZero() && proposed.After(old) {
			return nil, fault("invalid", "live admission deadlines can only be shortened; launch a new run to extend runtime")
		}
		c.AdmissionDeadline = proposed.UTC().Format(time.RFC3339Nano)
	}
	c.Version++
	if err = save(ctx, q, "resource_control", "live_resources", ownerID, "", "owner", c); err != nil {
		return nil, internal(err)
	}
	if err = saveStateRecoveryDigest(ctx, q, "resource_control", "live_resources", c); err != nil {
		return nil, internal(err)
	}
	if err = event(ctx, q, a, "capacity.changed", "live_resources", map[string]any{"version": c.Version, "agent": in.Agent, "admission_deadline": c.AdmissionDeadline}); err != nil {
		return nil, internal(err)
	}
	return c, nil
}
