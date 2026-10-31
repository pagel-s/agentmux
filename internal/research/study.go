package research

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"agentmux/internal/managedconfig"
)

type Budget struct {
	MaxModelTasks int `yaml:"max_model_tasks" json:"max_model_tasks"`
}
type Population struct {
	Mode         string `yaml:"mode" json:"mode"`
	MaxAgents    int    `yaml:"max_agents" json:"max_agents"`
	EntryCredits int    `yaml:"entry_credits" json:"entry_credits"`
}

func (p Population) validate(m Manifest) error {
	if p.Mode != "fixed" && p.Mode != "dynamic" {
		return errors.New("population.mode must be fixed or dynamic")
	}
	if p.MaxAgents < len(m.Config.Agents) || p.MaxAgents > 16 || p.EntryCredits < 1 || p.EntryCredits > m.Game.Credits {
		return errors.New("population requires initial count <= max_agents <=16 and entry_credits within initial budget")
	}
	if p.Mode == "fixed" && p.MaxAgents != len(m.Config.Agents) {
		return errors.New("fixed population max_agents must match the team")
	}
	if p.Mode == "dynamic" && (!m.Config.Spawn.Enabled || len(m.Config.Spawn.Templates) == 0) {
		return errors.New("dynamic research population requires explicit managed spawn templates/allow policy")
	}
	if p.Mode == "dynamic" && p.MaxAgents == len(m.Config.Agents) {
		return errors.New("dynamic population needs at least one additional declared slot")
	}
	return nil
}

type Preregistration struct {
	Question       string              `yaml:"question" json:"question"`
	PrimaryOutcome string              `yaml:"primary_outcome" json:"primary_outcome"`
	Hypotheses     []string            `yaml:"hypotheses" json:"hypotheses"`
	Factors        map[string][]string `yaml:"factors" json:"factors"`
	FixedControls  []string            `yaml:"fixed_controls" json:"fixed_controls"`
	MinimumBlocks  int                 `yaml:"minimum_blocks" json:"minimum_blocks"`
	Analysis       string              `yaml:"analysis" json:"analysis"`
}

func (p Preregistration) validate(m Manifest) error {
	if strings.TrimSpace(p.Question) == "" || len(p.Question) > 4096 || len(p.Hypotheses) < 1 || len(p.Hypotheses) > 32 || len(p.FixedControls) < 1 || len(p.FixedControls) > 64 || p.MinimumBlocks < 2 || p.MinimumBlocks > 10000 || p.PrimaryOutcome != "total_social_welfare" || strings.TrimSpace(p.Analysis) == "" {
		return errors.New("preregistration requires question, hypotheses, fixed_controls, primary_outcome:total_social_welfare, minimum_blocks>=2, and analysis")
	}
	for _, list := range [][]string{p.Hypotheses, p.FixedControls} {
		for _, text := range list {
			if strings.TrimSpace(text) == "" || len(text) > 4096 {
				return errors.New("preregistration entries must contain bounded text")
			}
		}
	}
	if len(p.Factors) < 1 || len(p.Factors) > 2 {
		return errors.New("preregister one or two binary treatment factors")
	}
	product := 1
	keys := []string{}
	for name, levels := range p.Factors {
		if name != "reward_mode" && name != "visibility" && name != "teams" && name != "team_rewards" {
			return errors.New("supported factors: reward_mode, visibility, teams, team_rewards")
		}
		if len(levels) != 2 || levels[0] == levels[1] || levels[0] == "" || levels[1] == "" {
			return errors.New("each preregistered factor requires two distinct ordered levels")
		}
		product *= 2
		keys = append(keys, name)
	}
	if product != len(m.Conditions) {
		return errors.New("conditions must cover the complete preregistered factorial exactly once")
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	for _, c := range m.Conditions {
		var parts []string
		for _, name := range keys {
			value := factor(c, name)
			levels := p.Factors[name]
			if value != levels[0] && value != levels[1] {
				return fmt.Errorf("condition %s not in preregistered factor levels", c.ID)
			}
			parts = append(parts, value)
		}
		key := strings.Join(parts, "\x00")
		if seen[key] {
			return errors.New("duplicate preregistered factorial cell")
		}
		seen[key] = true
	}
	first := m.Conditions[0]
	for _, c := range m.Conditions[1:] {
		for _, name := range []string{"reward_mode", "visibility", "teams", "team_rewards"} {
			if _, declared := p.Factors[name]; !declared && factor(c, name) != factor(first, name) {
				return errors.New("preregistered study changes an undeclared treatment factor")
			}
		}
		if c.Instructions != first.Instructions {
			return errors.New("preregistered factorial must hold additional instructions constant")
		}
	}
	return nil
}
func factor(c Condition, name string) string {
	switch name {
	case "reward_mode":
		return c.RewardMode
	case "visibility":
		return c.Visibility
	case "teams":
		return strconv.FormatBool(c.Teams)
	case "team_rewards":
		return c.TeamRewards
	}
	return ""
}

type modelBudget struct {
	Limit     int      `json:"limit"`
	Reserved  []string `json:"reserved_task_ids"`
	path      string
	fresh     bool
	execution *executionGuard
}

func loadBudget(dir string, limit int) (*modelBudget, error) {
	b := &modelBudget{Limit: limit, path: filepath.Join(dir, "model-budget.json")}
	data, err := os.ReadFile(b.path)
	if err == nil {
		if json.Unmarshal(data, b) != nil || b.Limit != limit || len(b.Reserved) > b.Limit {
			return nil, errors.New("invalid or mismatched durable native task budget")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		b.fresh = true
	}
	seen := map[string]bool{}
	for _, id := range b.Reserved {
		if id == "" || seen[id] {
			return nil, errors.New("corrupt native task reservations")
		}
		seen[id] = true
	}
	return b, nil
}
func (b *modelBudget) reserve(tasks map[string]string, cfg managedconfig.Config) error {
	var added []string
	for _, actor := range actors(cfg) {
		if cfg.Agents[actor].Harness != "command" {
			added = append(added, tasks[actor])
		}
	}
	if len(added) == 0 {
		return nil
	}
	if b.Limit == 0 || len(b.Reserved)+len(added) > b.Limit {
		return errors.New("durable native task budget exhausted; no further model turns started")
	}
	seen := map[string]bool{}
	for _, id := range b.Reserved {
		seen[id] = true
	}
	for _, id := range added {
		if id == "" || seen[id] {
			return errors.New("native task budget cannot replay a prior reservation")
		}
		seen[id] = true
	}
	b.Reserved = append(b.Reserved, added...)
	return writeJSON(b.path, b)
}
