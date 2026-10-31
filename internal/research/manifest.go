package research

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"agentmux/internal/managedconfig"
	"gopkg.in/yaml.v3"
)

type Manifest struct {
	Version             int                  `yaml:"version" json:"version"`
	ID                  string               `yaml:"id" json:"id"`
	Team                string               `yaml:"team" json:"team"`
	Trials              int                  `yaml:"trials" json:"trials"`
	Seeds               []int64              `yaml:"seeds" json:"seeds"`
	RandomizationSeed   int64                `yaml:"randomization_seed" json:"randomization_seed"`
	TimeoutSeconds      int                  `yaml:"timeout_seconds" json:"timeout_seconds"`
	PhaseTimeoutSeconds int                  `yaml:"phase_timeout_seconds" json:"phase_timeout_seconds"`
	BrokerSandbox       string               `yaml:"broker_sandbox" json:"broker_sandbox"`
	Game                GameOptions          `yaml:"game" json:"game"`
	Conditions          []Condition          `yaml:"conditions" json:"conditions"`
	Config              managedconfig.Config `yaml:"-" json:"resolved_team"`
}

type Condition struct {
	ID           string `yaml:"id" json:"id"`
	RewardMode   string `yaml:"reward_mode" json:"reward_mode"`
	Visibility   string `yaml:"visibility" json:"visibility"`
	Teams        bool   `yaml:"teams" json:"teams"`
	Instructions string `yaml:"instructions" json:"instructions,omitempty"`
}

type Trial struct {
	ID         string `json:"id"`
	Condition  string `json:"condition"`
	Seed       int64  `json:"seed"`
	Repetition int    `json:"repetition"`
	Order      int    `json:"order"`
}

type PlanResult struct {
	Trials        []Trial  `json:"trials"`
	Participants  []string `json:"participants"`
	TasksPerTrial int      `json:"tasks_per_trial"`
	TotalTasks    int      `json:"total_tasks"`
	Environment   string   `json:"environment"`
	SeedScope     string   `json:"seed_scope"`
}

var identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func Load(path string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	d := yaml.NewDecoder(f)
	d.KnownFields(true)
	var m Manifest
	if err = d.Decode(&m); err != nil {
		return m, fmt.Errorf("research manifest: %w", err)
	}
	var extra yaml.Node
	if d.Decode(&extra) != io.EOF {
		return m, errors.New("research requires exactly one YAML document")
	}
	if m.Team == "" {
		return m, errors.New("research requires a managed team file")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return m, err
	}
	if !filepath.IsAbs(m.Team) {
		m.Team = filepath.Join(base, m.Team)
	}
	m.Config, err = managedconfig.Load(m.Team)
	if err != nil {
		return m, err
	}
	for id, a := range m.Config.Agents {
		if strings.HasPrefix(a.Auth.File, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return m, err
			}
			a.Auth.File = filepath.Join(home, strings.TrimPrefix(a.Auth.File, "~/"))
		}
		if a.Auth.File != "" && !filepath.IsAbs(a.Auth.File) {
			a.Auth.File = filepath.Join(filepath.Dir(m.Team), a.Auth.File)
		}
		if len(a.Command) > 0 && strings.ContainsRune(a.Command[0], filepath.Separator) && !filepath.IsAbs(a.Command[0]) {
			a.Command[0] = filepath.Join(filepath.Dir(m.Team), a.Command[0])
		}
		for i, p := range a.ReadOnlyPaths {
			if !filepath.IsAbs(p) {
				a.ReadOnlyPaths[i] = filepath.Join(filepath.Dir(m.Team), p)
			}
		}
		m.Config.Agents[id] = a
	}
	m = normalize(m)
	return m, m.Validate()
}

func normalize(m Manifest) Manifest {
	m.Config = m.Config.Normalized()
	m.Seeds = append([]int64(nil), m.Seeds...)
	m.Conditions = append([]Condition(nil), m.Conditions...)
	if m.Trials == 0 {
		m.Trials = 1
	}
	if len(m.Seeds) == 0 {
		m.Seeds = []int64{0}
	}
	if m.TimeoutSeconds == 0 {
		m.TimeoutSeconds = 600
	}
	if m.PhaseTimeoutSeconds == 0 {
		m.PhaseTimeoutSeconds = 60
	}
	if m.BrokerSandbox == "" {
		m.BrokerSandbox = "bubblewrap"
	}
	if m.Game.Kind == "" {
		m.Game.Kind = GameVersion
	}
	if m.Game.Rounds == 0 {
		m.Game.Rounds = 4
	}
	if m.Game.Credits == 0 {
		m.Game.Credits = 8
	}
	if m.Game.PerRoundCap == 0 {
		m.Game.PerRoundCap = 2
	}
	if m.Game.Threshold == 0 {
		m.Game.Threshold = len(m.Config.Agents) + 1
	}
	if m.Game.Dividend == 0 {
		m.Game.Dividend = 3
	}
	return m
}

func (m Manifest) Validate() error {
	if m.Version != 1 || !identifier.MatchString(m.ID) {
		return errors.New("research requires version 1 and a conservative id")
	}
	if err := m.Config.Validate(); err != nil {
		return err
	}
	if err := m.Game.validate(len(m.Config.Agents)); err != nil {
		return err
	}
	if m.Trials < 1 || m.Trials > 100 || len(m.Seeds) < 1 || len(m.Seeds) > 100 || len(m.Conditions) < 1 || len(m.Conditions) > 64 || m.Trials*len(m.Seeds)*len(m.Conditions) > 10000 {
		return errors.New("research matrix requires bounded positive trials, seeds, and conditions (at most 10000 runs)")
	}
	if m.TimeoutSeconds < 1 || m.TimeoutSeconds > 86400 || m.PhaseTimeoutSeconds < 1 || m.PhaseTimeoutSeconds > 3600 || m.PhaseTimeoutSeconds > m.TimeoutSeconds {
		return errors.New("bounded phase_timeout_seconds <= timeout_seconds required")
	}
	if m.BrokerSandbox != "bubblewrap" && m.BrokerSandbox != "local" {
		return errors.New("broker_sandbox must be bubblewrap or explicitly trusted local")
	}
	if len(m.Config.Agents) > m.Config.Limits.MaxConcurrentTasks {
		return errors.New("population exceeds concurrent task limit")
	}
	if m.Game.Rounds*2*len(m.Config.Agents) > m.Config.Limits.MaxTasks {
		return errors.New("team max_tasks must cover two tasks per participant per round")
	}
	seeds := map[int64]bool{}
	for _, seed := range m.Seeds {
		if seeds[seed] {
			return errors.New("duplicate research seed")
		}
		seeds[seed] = true
	}
	conditions := map[string]bool{}
	for _, c := range m.Conditions {
		if !identifier.MatchString(c.ID) || conditions[c.ID] {
			return errors.New("condition ids must be unique conservative identifiers")
		}
		conditions[c.ID] = true
		if c.RewardMode != "shared" && c.RewardMode != "individual" && c.RewardMode != "winner" {
			return errors.New("reward_mode must be shared, individual, or winner")
		}
		if c.Visibility != "isolated" && c.Visibility != "board" && c.Visibility != "direct" {
			return errors.New("visibility must be isolated, board, or direct")
		}
		if c.Teams && c.Visibility != "direct" {
			return errors.New("teams require direct visibility so invitations have explicit message authority")
		}
		if len(c.Instructions) > 4096 {
			return errors.New("condition instructions exceed 4096 bytes")
		}
	}
	for _, a := range m.Config.Agents {
		if a.Harness != "command" && a.Sandbox != "bubblewrap" {
			return errors.New("research model participants require bubblewrap for private observations")
		}
	}
	return nil
}

func Plan(m Manifest) (PlanResult, error) {
	m = normalize(m)
	if err := m.Validate(); err != nil {
		return PlanResult{}, err
	}
	type block struct {
		seed       int64
		repetition int
	}
	var blocks []block
	for r := 1; r <= m.Trials; r++ {
		for _, seed := range m.Seeds {
			blocks = append(blocks, block{seed, r})
		}
	}
	rng := rand.New(rand.NewSource(m.RandomizationSeed))
	rng.Shuffle(len(blocks), func(i, j int) { blocks[i], blocks[j] = blocks[j], blocks[i] })
	plan := PlanResult{Participants: actors(m.Config), TasksPerTrial: 2 * m.Game.Rounds * len(m.Config.Agents), Environment: GameVersion, SeedScope: "paired environment cases and scheduling; native model sampling is not seeded"}
	for _, b := range blocks {
		for _, i := range rng.Perm(len(m.Conditions)) {
			plan.Trials = append(plan.Trials, Trial{ID: fmt.Sprintf("run-%05d", len(plan.Trials)+1), Condition: m.Conditions[i].ID, Seed: b.seed, Repetition: b.repetition, Order: len(plan.Trials) + 1})
		}
	}
	plan.TotalTasks = len(plan.Trials) * plan.TasksPerTrial
	return plan, nil
}

func actors(cfg managedconfig.Config) []string {
	ids := make([]string, 0, len(cfg.Agents))
	for id := range cfg.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func digest(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func condition(m Manifest, id string) Condition {
	for _, c := range m.Conditions {
		if c.ID == id {
			return c
		}
	}
	return Condition{}
}
