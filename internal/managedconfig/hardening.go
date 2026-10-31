package managedconfig

import (
	"agentmux/internal/egress"
	"fmt"
)

type Egress struct {
	Allow []egress.Endpoint `yaml:"allow" json:"allow,omitempty"`
}

// Disk enables sized tmpfs workspace, private home/history and temporary
// storage plus bounded, controlled checkpoints. Every size must be explicit.
type Disk struct {
	WorkspaceBytes int64 `yaml:"workspace_bytes" json:"workspace_bytes,omitempty"`
	HistoryBytes   int64 `yaml:"history_bytes" json:"history_bytes,omitempty"`
	TempBytes      int64 `yaml:"temp_bytes" json:"temp_bytes,omitempty"`
	MaxEntries     int   `yaml:"max_entries" json:"max_entries,omitempty"`
}

func (d Disk) Enabled() bool {
	return d.WorkspaceBytes != 0 || d.HistoryBytes != 0 || d.TempBytes != 0 || d.MaxEntries != 0
}

// Budget limits conservative admissions, not actual provider billing. Values
// are tokens and millionths of a US dollar. Every attempt charges its full
// declared maximum even if it fails, is cancelled, or has unknown usage.
type Budget struct {
	MaxTokens       int64 `yaml:"max_tokens" json:"max_tokens,omitempty"`
	MaxCostMicrousd int64 `yaml:"max_cost_microusd" json:"max_cost_microusd,omitempty"`
}
type AgentBudget struct {
	MaxTurnTokens          int64  `yaml:"max_turn_tokens" json:"max_turn_tokens,omitempty"`
	MaxTurnCostMicrousd    int64  `yaml:"max_turn_cost_microusd" json:"max_turn_cost_microusd,omitempty"`
	MaxSubtreeTokens       int64  `yaml:"max_subtree_tokens" json:"max_subtree_tokens,omitempty"`
	MaxSubtreeCostMicrousd int64  `yaml:"max_subtree_cost_microusd" json:"max_subtree_cost_microusd,omitempty"`
	Enforcement            string `yaml:"enforcement" json:"enforcement,omitempty"`
}

func (c Config) validateHardening() error {
	if c.Budget.MaxTokens < 0 || c.Budget.MaxCostMicrousd < 0 {
		return fmt.Errorf("session budget limits must not be negative")
	}
	for id, a := range c.Agents {
		if err := validateAgentHardening(a, c.Budget); err != nil {
			return fmt.Errorf("agent %q: %w", id, err)
		}
	}
	for id, a := range c.Spawn.Templates {
		if err := validateAgentHardening(a, c.Budget); err != nil {
			return fmt.Errorf("template %q: %w", id, err)
		}
	}
	return nil
}
func validateAgentHardening(a Agent, b Budget) error {
	if a.Disk.Enabled() {
		if a.Sandbox != "bubblewrap" {
			return fmt.Errorf("disk capsules require bubblewrap")
		}
		for _, n := range []int64{a.Disk.WorkspaceBytes, a.Disk.HistoryBytes, a.Disk.TempBytes} {
			if n < 1<<20 || n > 1<<40 {
				return fmt.Errorf("disk sizes must each be 1 MiB–1 TiB")
			}
		}
		if a.Disk.MaxEntries < 1 || a.Disk.MaxEntries > 100000 {
			return fmt.Errorf("disk max_entries must be 1–100000")
		}
	}
	if a.Network == "allowlist" {
		if a.Sandbox != "bubblewrap" {
			return fmt.Errorf("egress allowlists require bubblewrap")
		}
		if err := egress.Validate(a.Egress.Allow); err != nil {
			return err
		}
	} else if len(a.Egress.Allow) != 0 {
		return fmt.Errorf("egress origins require network: allowlist")
	}
	v := a.Budget
	if v.MaxTurnTokens < 0 || v.MaxTurnCostMicrousd < 0 || v.MaxSubtreeTokens < 0 || v.MaxSubtreeCostMicrousd < 0 {
		return fmt.Errorf("budget limits must not be negative")
	}
	if v.Enforcement != "" && v.Enforcement != "reservation" {
		return fmt.Errorf("unsupported hard provider budget: native harnesses cannot enforce a total token or billing ceiling; reservation limits conservative admission only")
	}
	if (b.MaxTokens > 0 || v.MaxSubtreeTokens > 0) && v.MaxTurnTokens == 0 {
		return fmt.Errorf("token budget requires explicit max_turn_tokens")
	}
	if (b.MaxCostMicrousd > 0 || v.MaxSubtreeCostMicrousd > 0) && v.MaxTurnCostMicrousd == 0 {
		return fmt.Errorf("cost budget requires explicit max_turn_cost_microusd")
	}
	return nil
}
func cloneEgress(v Egress) Egress {
	v.Allow = append([]egress.Endpoint(nil), v.Allow...)
	for i := range v.Allow {
		v.Allow[i].IPs = append([]string(nil), v.Allow[i].IPs...)
	}
	return v
}
