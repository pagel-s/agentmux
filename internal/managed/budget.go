package managed

import (
	"context"
	"encoding/json"
	"errors"
	"math"
)

// BudgetCharge is private cumulative admission accounting. It deliberately
// does not subtract provider-reported usage: failed/unknown/cancelled attempts
// may already have incurred costs. Retrying always reserves a new turn.
type BudgetCharge struct {
	Tokens       int64 `json:"tokens"`
	CostMicrousd int64 `json:"cost_microusd"`
	Turns        int64 `json:"turns"`
}

func addCharge(a, b BudgetCharge) (BudgetCharge, error) {
	if a.Tokens < 0 || a.CostMicrousd < 0 || a.Turns < 0 || b.Tokens < 0 || b.CostMicrousd < 0 || b.Turns < 0 || a.Tokens > math.MaxInt64-b.Tokens || a.CostMicrousd > math.MaxInt64-b.CostMicrousd || a.Turns > math.MaxInt64-b.Turns {
		return BudgetCharge{}, errors.New("budget accounting overflow")
	}
	return BudgetCharge{a.Tokens + b.Tokens, a.CostMicrousd + b.CostMicrousd, a.Turns + b.Turns}, nil
}

func (s *Store) budgetLineage(ctx context.Context, q queryer, id string) ([]AgentInfo, error) {
	var out []AgentInfo
	seen := map[string]bool{}
	for id != "" && id != ownerID {
		if seen[id] || len(out) > s.cfg.Limits.MaxSpawnDepth+1 {
			return nil, errors.New("invalid budget ancestry")
		}
		seen[id] = true
		a, err := s.agent(ctx, q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
		id = a.Parent
	}
	return out, nil
}

func (s *Store) budgetCharges(ctx context.Context, q queryer) (map[string]BudgetCharge, BudgetCharge, error) {
	rows, err := q.QueryContext(ctx, "SELECT target,json_extract(data,'$.budget_charge') FROM records WHERE kind='task' AND json_extract(data,'$.budget_charge') IS NOT NULL")
	if err != nil {
		return nil, BudgetCharge{}, err
	}
	defer rows.Close()
	byAgent := map[string]BudgetCharge{}
	var session BudgetCharge
	for rows.Next() {
		var id, data string
		if err = rows.Scan(&id, &data); err != nil {
			return nil, session, err
		}
		var charge BudgetCharge
		if err = json.Unmarshal([]byte(data), &charge); err != nil {
			return nil, session, err
		}
		byAgent[id], err = addCharge(byAgent[id], charge)
		if err != nil {
			return nil, session, err
		}
		session, err = addCharge(session, charge)
		if err != nil {
			return nil, session, err
		}
	}
	return byAgent, session, rows.Err()
}

func (s *Store) subtreeCharge(ctx context.Context, q queryer, root string, charges map[string]BudgetCharge) (BudgetCharge, error) {
	var out BudgetCharge
	for id, charge := range charges {
		lineage, err := s.budgetLineage(ctx, q, id)
		if err != nil {
			return out, err
		}
		for _, a := range lineage {
			if a.ID == root {
				out, err = addCharge(out, charge)
				if err != nil {
					return out, err
				}
				break
			}
		}
	}
	return out, nil
}

func withinBudget(used, extra, limit int64) bool {
	return limit == 0 || (used <= limit && extra <= limit-used)
}

// turnBudgetAllowance checks admission without reserving or changing any usage.
// Both read-only readiness and the transactional lease claim use these rules.
func (s *Store) turnBudgetAllowance(ctx context.Context, q queryer, id string) (BudgetCharge, *Fault) {
	lineage, err := s.budgetLineage(ctx, q, id)
	if err != nil {
		return BudgetCharge{}, internal(err)
	}
	b := lineage[0].Config.Budget
	extra := BudgetCharge{Tokens: b.MaxTurnTokens, CostMicrousd: b.MaxTurnCostMicrousd, Turns: 1}
	needsTokens, needsCost := s.cfg.Budget.MaxTokens > 0, s.cfg.Budget.MaxCostMicrousd > 0
	for _, a := range lineage {
		needsTokens = needsTokens || a.Config.Budget.MaxSubtreeTokens > 0
		needsCost = needsCost || a.Config.Budget.MaxSubtreeCostMicrousd > 0
	}
	if needsTokens && extra.Tokens == 0 || needsCost && extra.CostMicrousd == 0 {
		return BudgetCharge{}, fault("limit", "turn requires explicit token/cost reservation under its ancestor/session budget")
	}
	if extra.Tokens == 0 && extra.CostMicrousd == 0 && !needsTokens && !needsCost {
		return BudgetCharge{}, nil
	}
	charges, session, err := s.budgetCharges(ctx, q)
	if err != nil {
		return BudgetCharge{}, internal(err)
	}
	if !withinBudget(session.Tokens, extra.Tokens, s.cfg.Budget.MaxTokens) || !withinBudget(session.CostMicrousd, extra.CostMicrousd, s.cfg.Budget.MaxCostMicrousd) {
		return BudgetCharge{}, fault("limit", "session admission budget exhausted")
	}
	for _, a := range lineage {
		used, err := s.subtreeCharge(ctx, q, a.ID, charges)
		if err != nil {
			return BudgetCharge{}, internal(err)
		}
		limits := a.Config.Budget
		if !withinBudget(used.Tokens, extra.Tokens, limits.MaxSubtreeTokens) || !withinBudget(used.CostMicrousd, extra.CostMicrousd, limits.MaxSubtreeCostMicrousd) {
			return BudgetCharge{}, fault("limit", "agent ancestry admission budget exhausted")
		}
	}
	return extra, nil
}

// reserveTurnBudget must run inside the same transaction as the lease claim.
// No remote model call occurs here. This limits declared admissions, not
// actual provider tokens/billing, which native CLI protocols cannot bound.
func (s *Store) reserveTurnBudget(ctx context.Context, q queryer, t *taskRecord) *Fault {
	extra, problem := s.turnBudgetAllowance(ctx, q, t.To)
	if problem != nil {
		return problem
	}
	if extra == (BudgetCharge{}) {
		return nil
	}
	prior := BudgetCharge{}
	if t.BudgetCharge != nil {
		prior = *t.BudgetCharge
	}
	total, err := addCharge(prior, extra)
	if err != nil {
		return internal(err)
	}
	t.BudgetCharge = &total
	return nil
}

func (s *Store) budgetStatus(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	var in struct {
		Agent string `json:"agent"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	if !a.owner() {
		if in.Agent != "" && in.Agent != a.ID {
			return nil, forbidden()
		}
		in.Agent = a.ID
	}
	charges, session, err := s.budgetCharges(ctx, q)
	if err != nil {
		return nil, internal(err)
	}
	result := map[string]any{"enforcement": "reservation", "provider_hard_cap": false, "note": "Every attempt permanently charges its full declared allowance; provider usage observations do not establish a hard token or billing cap."}
	if in.Agent == "" {
		result["used"] = session
		result["limit"] = s.cfg.Budget
		return result, nil
	}
	info, err := s.agent(ctx, q, in.Agent)
	if err != nil {
		return nil, fault("not_found", "agent not found")
	}
	used, err := s.subtreeCharge(ctx, q, in.Agent, charges)
	if err != nil {
		return nil, internal(err)
	}
	result["agent"] = in.Agent
	result["used"] = used
	result["limit"] = info.Config.Budget
	return result, nil
}
