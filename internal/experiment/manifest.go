// Package experiment runs bounded, independently scored research pilots. It
// records conditions and infrastructure outcomes, not inferred agent motives.
package experiment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"agentmux/internal/managedconfig"
	"gopkg.in/yaml.v3"
)

type Manifest struct {
	Version           int                  `yaml:"version" json:"version"`
	ID                string               `yaml:"id" json:"id"`
	Team              string               `yaml:"team" json:"team"`
	Trials            int                  `yaml:"trials" json:"trials"`
	Seeds             []int64              `yaml:"seeds" json:"seeds"`
	RandomizationSeed int64                `yaml:"randomization_seed" json:"randomization_seed"`
	TimeoutSeconds    int                  `yaml:"timeout_seconds" json:"timeout_seconds"`
	MaxConcurrency    int                  `yaml:"max_concurrency" json:"max_concurrency"`
	Conditions        []Condition          `yaml:"conditions" json:"conditions"`
	Assertions        []Assertion          `yaml:"assertions" json:"assertions,omitempty"`
	Config            managedconfig.Config `yaml:"-" json:"resolved_team"`
}

type Condition struct {
	ID                 string            `yaml:"id" json:"id"`
	RewardMode         string            `yaml:"reward_mode" json:"reward_mode"`
	RewardInstructions string            `yaml:"reward_instructions" json:"reward_instructions,omitempty"`
	Visibility         string            `yaml:"visibility" json:"visibility"`
	Instructions       map[string]string `yaml:"instructions" json:"instructions"`
	Assertions         []Assertion       `yaml:"assertions" json:"assertions,omitempty"`
}

// Assertion compares an initial task's output against an owner-defined oracle.
// Exactly one comparison is required. These oracles remain outside agent homes.
type Assertion struct {
	Agent    string  `yaml:"agent" json:"agent"`
	Exact    *string `yaml:"exact" json:"exact,omitempty"`
	Contains *string `yaml:"contains" json:"contains,omitempty"`
	Weight   float64 `yaml:"weight" json:"weight"`
}

type Trial struct {
	ID         string `json:"id"`
	Condition  string `json:"condition"`
	Seed       int64  `json:"seed"`
	Repetition int    `json:"repetition"`
	Order      int    `json:"order"`
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
	if err := d.Decode(&m); err != nil {
		return m, fmt.Errorf("experiment manifest: %w", err)
	}
	var extra yaml.Node
	if err := d.Decode(&extra); err != io.EOF {
		return m, fmt.Errorf("experiment requires exactly one YAML document")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return m, err
	}
	if !filepath.IsAbs(m.Team) {
		m.Team = filepath.Join(base, m.Team)
	}
	if m.Team == "" {
		return m, fmt.Errorf("experiment requires team")
	}
	m.Config, err = managedconfig.Load(m.Team)
	if err != nil {
		return m, err
	}
	// Configured source workspaces are deliberately discarded per trial. Only
	// executable/dependency references are resolved against the team YAML.
	for id, agent := range m.Config.Agents {
		if strings.HasPrefix(agent.Auth.File, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return m, fmt.Errorf("resolve native auth home directory")
			}
			agent.Auth.File = filepath.Join(home, strings.TrimPrefix(agent.Auth.File, "~/"))
		}
		if agent.Auth.File != "" && !filepath.IsAbs(agent.Auth.File) {
			agent.Auth.File = filepath.Join(filepath.Dir(m.Team), agent.Auth.File)
		}
		if len(agent.Command) > 0 && strings.ContainsRune(agent.Command[0], filepath.Separator) && !filepath.IsAbs(agent.Command[0]) {
			agent.Command[0] = filepath.Join(filepath.Dir(m.Team), agent.Command[0])
		}
		for i, resource := range agent.ReadOnlyPaths {
			if !filepath.IsAbs(resource) {
				agent.ReadOnlyPaths[i] = filepath.Join(filepath.Dir(m.Team), resource)
			}
		}
		m.Config.Agents[id] = agent
	}
	m = normalize(m)
	return m, m.Validate()
}

func normalize(m Manifest) Manifest {
	m.Config = m.Config.Normalized()
	m.Assertions = append([]Assertion(nil), m.Assertions...)
	m.Conditions = append([]Condition(nil), m.Conditions...)
	for i, condition := range m.Conditions {
		condition.Assertions = append([]Assertion(nil), condition.Assertions...)
		instructions := make(map[string]string, len(condition.Instructions))
		for id, text := range condition.Instructions {
			instructions[id] = text
		}
		condition.Instructions = instructions
		m.Conditions[i] = condition
	}
	if m.Trials == 0 {
		m.Trials = 1
	}
	if len(m.Seeds) == 0 {
		m.Seeds = []int64{0}
	}
	if m.TimeoutSeconds == 0 {
		m.TimeoutSeconds = 300
	}
	if m.MaxConcurrency == 0 {
		m.MaxConcurrency = 1
	}
	for i := range m.Assertions {
		if m.Assertions[i].Weight == 0 {
			m.Assertions[i].Weight = 1
		}
	}
	for i := range m.Conditions {
		for j := range m.Conditions[i].Assertions {
			if m.Conditions[i].Assertions[j].Weight == 0 {
				m.Conditions[i].Assertions[j].Weight = 1
			}
		}
	}
	return m
}

func (m Manifest) Validate() error {
	if m.Version != 1 || !identifier.MatchString(m.ID) {
		return fmt.Errorf("experiment requires version 1 and a conservative id of at most 64 characters")
	}
	if err := m.Config.Validate(); err != nil {
		return err
	}
	if len(m.Config.Agents) > m.Config.Limits.MaxConcurrentTasks {
		return fmt.Errorf("experiment population exceeds the configured concurrent-task limit")
	}
	if m.Trials < 1 || m.Trials > 1000 || len(m.Seeds) < 1 || len(m.Seeds) > 1000 || len(m.Conditions) < 1 || len(m.Conditions) > 128 {
		return fmt.Errorf("experiment requires bounded positive trials, seeds, and conditions")
	}
	if m.Trials*len(m.Seeds)*len(m.Conditions) > 10000 {
		return fmt.Errorf("experiment matrix exceeds 10000 trials")
	}
	if m.MaxConcurrency != 1 {
		return fmt.Errorf("this runner supports max_concurrency: 1 (agents within a trial still run concurrently)")
	}
	if m.TimeoutSeconds < 1 || m.TimeoutSeconds > 86400 {
		return fmt.Errorf("timeout_seconds must be between 1 and 86400")
	}
	seeds := map[int64]bool{}
	for _, seed := range m.Seeds {
		if seeds[seed] {
			return fmt.Errorf("duplicate seed %d", seed)
		}
		seeds[seed] = true
	}
	conditions := map[string]bool{}
	for _, c := range m.Conditions {
		if !identifier.MatchString(c.ID) || conditions[c.ID] {
			return fmt.Errorf("condition ids must be unique conservative identifiers")
		}
		conditions[c.ID] = true
		if c.RewardMode != "shared" && c.RewardMode != "individual" && c.RewardMode != "winner" {
			return fmt.Errorf("condition %s: reward_mode must be shared, individual, or winner", c.ID)
		}
		if c.Visibility != "board-only" && c.Visibility != "direct" {
			return fmt.Errorf("condition %s: visibility must be board-only or direct", c.ID)
		}
		if len(c.Instructions) != len(m.Config.Agents) {
			return fmt.Errorf("condition %s: provide exactly one initial instruction per configured agent", c.ID)
		}
		for agent, instruction := range c.Instructions {
			if _, exists := m.Config.Agents[agent]; !exists || strings.TrimSpace(instruction) == "" || len(instruction) > m.Config.Limits.MaxMessageBytes {
				return fmt.Errorf("condition %s: invalid instruction for agent %s", c.ID, agent)
			}
		}
		assertions := m.assertions(c)
		if len(assertions) == 0 {
			return fmt.Errorf("condition %s: independent assertions are required", c.ID)
		}
		for _, a := range assertions {
			if _, exists := m.Config.Agents[a.Agent]; !exists {
				return fmt.Errorf("assertion references unknown agent %s", a.Agent)
			}
			if (a.Exact == nil) == (a.Contains == nil) || (a.Contains != nil && *a.Contains == "") {
				return fmt.Errorf("assertion requires exactly one exact or nonempty contains value")
			}
			if math.IsNaN(a.Weight) || math.IsInf(a.Weight, 0) || a.Weight <= 0 || a.Weight > 1000000 {
				return fmt.Errorf("assertion weight must be positive and at most 1000000")
			}
		}
	}
	return nil
}

func (m Manifest) assertions(c Condition) []Assertion {
	if len(c.Assertions) > 0 {
		return c.Assertions
	}
	return m.Assertions
}

// Plan randomizes balanced condition blocks. Every condition occurs exactly
// once for each seed/repetition pair; order is deterministic for the manifest.
func Plan(m Manifest) ([]Trial, error) {
	m = normalize(m)
	if err := m.Validate(); err != nil {
		return nil, err
	}
	type block struct {
		seed       int64
		repetition int
	}
	blocks := []block{}
	for rep := 1; rep <= m.Trials; rep++ {
		for _, seed := range m.Seeds {
			blocks = append(blocks, block{seed, rep})
		}
	}
	rng := rand.New(rand.NewSource(m.RandomizationSeed))
	rng.Shuffle(len(blocks), func(i, j int) { blocks[i], blocks[j] = blocks[j], blocks[i] })
	var trials []Trial
	for _, b := range blocks {
		order := rng.Perm(len(m.Conditions))
		for _, i := range order {
			c := m.Conditions[i]
			trials = append(trials, Trial{ID: fmt.Sprintf("trial-%06d", len(trials)+1), Condition: c.ID, Seed: b.seed, Repetition: b.repetition, Order: len(trials) + 1})
		}
	}
	return trials, nil
}

func digest(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
