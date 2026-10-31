package managedconfig

import "fmt"

// Autonomy is an owner-approved observation subscription, never an instruction
// grant. Zero values leave all wake triggers disabled. Bounds cover every turn
// when enabled, including explicitly instructed turns.
type Autonomy struct {
	Enabled         bool     `yaml:"enabled" json:"enabled"`
	Boards          []string `yaml:"boards" json:"boards,omitempty"`
	DirectNotes     bool     `yaml:"direct_notes" json:"direct_notes"`
	Pings           bool     `yaml:"pings" json:"pings"`
	MaxTurns        int      `yaml:"max_turns" json:"max_turns"`
	CooldownSeconds int      `yaml:"cooldown_seconds" json:"cooldown_seconds"`
	MaxBacklog      int      `yaml:"max_backlog" json:"max_backlog"`
	BatchSize       int      `yaml:"batch_size" json:"batch_size"`
	MaxHops         int      `yaml:"max_hops" json:"max_hops"`
	MaxInFlight     int      `yaml:"max_inflight" json:"max_inflight"`
}

func (a Autonomy) normalized() Autonomy {
	a.Boards = append([]string(nil), a.Boards...)
	if a.MaxTurns == 0 {
		a.MaxTurns = 100
	}
	if a.MaxBacklog == 0 {
		a.MaxBacklog = 100
	}
	if a.BatchSize == 0 {
		a.BatchSize = 20
		if a.MaxBacklog < a.BatchSize {
			a.BatchSize = a.MaxBacklog
		}
	}
	if a.MaxHops == 0 {
		a.MaxHops = 8
	}
	if a.MaxInFlight == 0 {
		a.MaxInFlight = 1
	}
	return a
}

func (a Autonomy) validate(id string) error {
	a = a.normalized()
	for _, bound := range []struct {
		name            string
		value, min, max int
	}{
		{"max_turns", a.MaxTurns, 1, 100000}, {"cooldown_seconds", a.CooldownSeconds, 0, 86400},
		{"max_backlog", a.MaxBacklog, 1, 1000}, {"batch_size", a.BatchSize, 1, 100},
		{"max_hops", a.MaxHops, 1, 100}, {"max_inflight", a.MaxInFlight, 1, 1},
	} {
		if bound.value < bound.min || bound.value > bound.max {
			return fmt.Errorf("agent %q: autonomy.%s must be between %d and %d", id, bound.name, bound.min, bound.max)
		}
	}
	if a.BatchSize > a.MaxBacklog {
		return fmt.Errorf("agent %q: autonomy.batch_size exceeds max_backlog", id)
	}
	seen := map[string]bool{}
	for _, board := range a.Boards {
		if (board != "*" && !ValidID(board)) || seen[board] {
			return fmt.Errorf("agent %q: autonomy.boards requires distinct namespace names or an explicit wildcard", id)
		}
		seen[board] = true
	}
	return nil
}
