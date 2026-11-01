package research

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func treatmentManifest() Manifest {
	m := testManifest()
	m.Counterbalancing = "latin_square"
	m.RoleAssignments = map[string]map[string]string{
		"forward": {"alice": "Analyst", "bob": "Critic"},
		"reverse": {"alice": "Critic", "bob": "Analyst"},
	}
	m.PersonalityProfiles = map[string]map[string]string{
		"cautious": {"alice": "Check evidence before acting."},
		"bold":     {"alice": "Explore uncertain options."},
	}
	m.Conditions = nil
	for _, role := range []string{"forward", "reverse"} {
		for _, personality := range []string{"cautious", "bold"} {
			m.Conditions = append(m.Conditions, Condition{ID: role + "-" + personality, RewardMode: "individual", Visibility: "direct", Teams: true, TeamRewards: "none", RoleAssignment: role, Personality: personality})
		}
	}
	m.Preregistration = &Preregistration{Question: "Prompt treatments and consented teams", PrimaryOutcome: "teams_with_multiple_members", Hypotheses: []string{"Prompt treatments may change team formation."}, Factors: map[string][]string{"role_assignment": {"forward", "reverse"}, "personality": {"cautious", "bold"}}, FixedControls: []string{"Same initial actors, harnesses, worlds, permissions and game."}, MinimumBlocks: 8, Analysis: "Run-level paired factorial; descriptive pilot, no trait inference."}
	return normalize(m)
}

func TestTreatmentProfilesCompileRolesWithoutMutatingSource(t *testing.T) {
	m := treatmentManifest()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	before := digest(m)
	first := trialConfig(m, m.Conditions[0], "/tmp/design-forward")
	last := trialConfig(m, m.Conditions[3], "/tmp/design-reverse")
	if !strings.HasPrefix(first.Agents["alice"].Role, "Analyst\n") || !strings.Contains(first.Agents["alice"].Role, "Check evidence") || !strings.HasPrefix(last.Agents["alice"].Role, "Critic\n") || !strings.Contains(last.Agents["alice"].Role, "Explore uncertain") {
		t.Fatal("condition-specific role/personality not compiled into participant config")
	}
	if strings.Contains(first.Agents["bob"].Role, "Research personality") || !strings.HasPrefix(first.Agents["bob"].Role, "Critic\n") {
		t.Fatal("partial personality assignment changed another actor")
	}
	if before != digest(m) {
		t.Fatal("compiling a trial mutated frozen treatments")
	}
	copy := normalize(m)
	copy.RoleAssignments["forward"]["alice"] = "Changed"
	copy.PersonalityProfiles["cautious"]["alice"] = "Changed"
	if digest(m) != before {
		t.Fatal("normalization aliases treatment profiles")
	}
	if digest(first) == digest(last) {
		t.Fatal("treatment configuration hashes do not distinguish prompts")
	}
}

func TestLatinSquareBalancesConditionPositionsAndKeepsPairedCells(t *testing.T) {
	m := treatmentManifest()
	plan, err := Plan(m)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Plan(m)
	if !reflect.DeepEqual(plan, again) || plan.Counterbalancing != "latin_square" {
		t.Fatal("schedule not deterministic or labeled")
	}
	positions := map[string][]int{}
	blocks := map[string]map[string]bool{}
	for index, trial := range plan.Trials {
		if positions[trial.Condition] == nil {
			positions[trial.Condition] = make([]int, len(m.Conditions))
		}
		positions[trial.Condition][index%len(m.Conditions)]++
		key := string(rune(trial.Seed)) + "/" + string(rune(trial.Repetition))
		if blocks[key] == nil {
			blocks[key] = map[string]bool{}
		}
		if blocks[key][trial.Condition] {
			t.Fatal("repeated paired cell")
		}
		blocks[key][trial.Condition] = true
	}
	for _, counts := range positions {
		for _, n := range counts {
			if n != 1 {
				t.Fatalf("unbalanced positions: %v", positions)
			}
		}
	}
	for _, cells := range blocks {
		if len(cells) != 4 {
			t.Fatal("incomplete paired block")
		}
	}
	m.Seeds = []int64{11}
	if _, err := Plan(m); err == nil || !strings.Contains(err.Error(), "condition-order cycles") {
		t.Fatal("partial Latin square silently accepted")
	}
}

func TestResearchDesignRejectsUndeclaredConfoundedOrInvalidTreatments(t *testing.T) {
	for name, mutate := range map[string]func(*Manifest){
		"unknown profile":        func(m *Manifest) { m.Conditions[0].RoleAssignment = "missing" },
		"unknown actor":          func(m *Manifest) { m.RoleAssignments["forward"]["intruder"] = "Role" },
		"empty prompt":           func(m *Manifest) { m.PersonalityProfiles["bold"]["alice"] = " " },
		"unknown outcome":        func(m *Manifest) { m.Preregistration.PrimaryOutcome = "agent_personality" },
		"wrong environment":      func(m *Manifest) { m.Preregistration.PrimaryOutcome = "escrow_return_fraction" },
		"conflicting outcome":    func(m *Manifest) { m.PrimaryOutcome = "total_social_welfare" },
		"undeclared personality": func(m *Manifest) { delete(m.Preregistration.Factors, "personality") },
		"unknown schedule":       func(m *Manifest) { m.Counterbalancing = "whatever" },
	} {
		t.Run(name, func(t *testing.T) {
			m := treatmentManifest()
			mutate(&m)
			if m.Validate() == nil {
				t.Fatal("invalid study accepted")
			}
		})
	}
}

func TestPrimaryTeamOutcomeDrivesPairedAndFactorialEffects(t *testing.T) {
	m := treatmentManifest()
	report := Report{Research: m.ID, ManifestHash: "hash", Preregistration: m.Preregistration, Conditions: m.Conditions, Counterbalancing: m.Counterbalancing}
	for _, seed := range []int64{11, 22} {
		for _, c := range m.Conditions {
			formed := 0
			if c.RoleAssignment == "reverse" {
				formed += 2
			}
			if c.Personality == "bold" {
				formed++
			}
			teams := []managed.Team{}
			for i := 0; i < formed; i++ {
				teams = append(teams, managed.Team{ID: string(rune('a' + i)), State: "active", Members: []string{"alice", "bob"}})
			}
			// Welfare moves in the opposite direction from the selected outcome.
			report.Results = append(report.Results, Result{Status: "completed", Trial: Trial{Condition: c.ID, Seed: seed, Repetition: 1}, Rounds: []Outcome{{SocialWelfare: -10 * float64(formed)}}, Phases: []Phase{{Teams: teams}, {Teams: teams}}, ActorMetadata: map[string]AgentLabel{"alice": {Harness: "command"}}})
		}
	}
	a := Analyze(report)
	if a.PrimaryOutcome != "teams_with_multiple_members" || a.Counterbalancing != "latin_square" || len(a.FactorialEffects) != 3 {
		t.Fatalf("missing design metadata: %+v", a)
	}
	means := map[string]float64{}
	for _, effect := range a.FactorialEffects {
		means[strings.Join(effect.Factors, "+")] = effect.Difference.Mean
		if effect.Outcome != a.PrimaryOutcome || effect.Difference.N != 2 {
			t.Fatal("wrong estimand or block count")
		}
	}
	if means["personality"] != 1 || means["role_assignment"] != 2 || means["personality+role_assignment"] != 0 {
		t.Fatalf("used welfare or duplicated team snapshots: %v", means)
	}
	for _, c := range a.Conditions {
		if c.PrimaryEstimate.Mean != c.ConsentedTeams.Mean || c.PrimaryEstimate.N != 2 {
			t.Fatal("primary summary not selected")
		}
	}
	contrast := a.PairedContrasts[0]
	if contrast.PrimaryOutcome != a.PrimaryOutcome || contrast.Difference.Mean != -10*contrast.PrimaryDifference.Mean {
		t.Fatal("legacy welfare and selected primary contrasts were conflated")
	}
	warnings := strings.Join(a.EvidenceWarnings, " ")
	if !strings.Contains(warnings, "scripted command") || !strings.Contains(warnings, "Small evidence set") || a.PowerWarning == "" {
		t.Fatal("scripted/tiny pilot evidence presented without limits")
	}
	report.Preregistration = nil
	if !strings.Contains(strings.Join(Analyze(report).EvidenceWarnings, " "), "Unregistered exploratory") {
		t.Fatal("missing unregistered evidence disclosure")
	}
}

func TestPrimaryReturnFractionExcludesUnavailableDenominators(t *testing.T) {
	report := Report{Research: "exchange", ManifestHash: "hash", PrimaryOutcome: "escrow_return_fraction", Conditions: []Condition{{ID: "a"}, {ID: "b"}}}
	for _, c := range []string{"a", "b"} {
		for _, seed := range []int64{1, 2} {
			escrow := &ExchangeOutcome{TotalEscrow: 4, TotalReturned: 2}
			if c == "b" && seed == 2 {
				escrow = &ExchangeOutcome{}
			}
			report.Results = append(report.Results, Result{Status: "completed", Trial: Trial{Condition: c, Seed: seed, Repetition: 1}, Rounds: []Outcome{{Exchange: escrow}}})
		}
	}
	a := Analyze(report)
	if a.Conditions[0].PrimaryEstimate.N != 2 || a.Conditions[1].PrimaryEstimate.N != 1 || a.PairedContrasts[0].PrimaryDifference.N != 1 || !strings.Contains(strings.Join(a.EvidenceWarnings, " "), "zero received escrow") {
		t.Fatalf("missing denominator treated as zero or disclosure lost: %+v", a)
	}
}

func TestThreeFactorDesignIncludesHigherOrderInteraction(t *testing.T) {
	m := treatmentManifest()
	m.Counterbalancing = ""
	m.Preregistration.PrimaryOutcome = defaultOutcome
	m.Preregistration.Factors = map[string][]string{"reward_mode": {"individual", "shared"}, "teams": {"false", "true"}, "role_assignment": {"forward", "reverse"}}
	m.Conditions = nil
	report := Report{Research: "three-factor", ManifestHash: "hash", Preregistration: m.Preregistration}
	for reward := 0; reward < 2; reward++ {
		for teams := 0; teams < 2; teams++ {
			for role := 0; role < 2; role++ {
				c := Condition{ID: string(rune('a' + len(m.Conditions))), RewardMode: []string{"individual", "shared"}[reward], Visibility: "direct", Teams: teams == 1, TeamRewards: "none", RoleAssignment: []string{"forward", "reverse"}[role]}
				m.Conditions = append(m.Conditions, c)
				for _, seed := range []int64{11, 22} {
					welfare := float64(2*reward + 3*teams + 4*role + 5*reward*teams + 7*reward*teams*role)
					report.Results = append(report.Results, Result{Status: "completed", Trial: Trial{Condition: c.ID, Seed: seed, Repetition: 1}, Rounds: []Outcome{{SocialWelfare: welfare}}})
				}
			}
		}
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	report.Conditions = m.Conditions
	a := Analyze(report)
	if len(a.FactorialEffects) != 7 {
		t.Fatalf("missing factorial interactions: %+v", a)
	}
	found := false
	for _, effect := range a.FactorialEffects {
		if len(effect.Factors) == 3 {
			found = true
			if effect.Difference.Mean != 7 || effect.Difference.N != 2 {
				t.Fatalf("wrong higher-order contrast: %+v", effect)
			}
		}
	}
	if !found {
		t.Fatal("no three-factor interaction")
	}
}

func TestRolePersonalityExamplePlansWithoutCalls(t *testing.T) {
	m, err := Load(filepath.Join("..", "..", "examples", "research", "roles-personality.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(m)
	if err != nil || len(plan.Trials) != 16 || plan.ModelTasks != 0 || plan.Counterbalancing != "latin_square" {
		t.Fatalf("example plan: %+v %v", plan, err)
	}
}

func TestLegacyManifestJSONAndHashRemainUnchanged(t *testing.T) {
	m := testManifest()
	// The original manifest fields in their original serialized order.
	legacy := struct {
		Version             int                  `json:"version"`
		ID                  string               `json:"id"`
		Team                string               `json:"team"`
		Trials              int                  `json:"trials"`
		Seeds               []int64              `json:"seeds"`
		RandomizationSeed   int64                `json:"randomization_seed"`
		TimeoutSeconds      int                  `json:"timeout_seconds"`
		PhaseTimeoutSeconds int                  `json:"phase_timeout_seconds"`
		BrokerSandbox       string               `json:"broker_sandbox"`
		Game                GameOptions          `json:"game"`
		Conditions          []Condition          `json:"conditions"`
		Budget              Budget               `json:"budget"`
		Population          Population           `json:"population"`
		Preregistration     *Preregistration     `json:"preregistration,omitempty"`
		Config              managedconfig.Config `json:"resolved_team"`
	}{m.Version, m.ID, m.Team, m.Trials, m.Seeds, m.RandomizationSeed, m.TimeoutSeconds, m.PhaseTimeoutSeconds, m.BrokerSandbox, m.Game, m.Conditions, m.Budget, m.Population, m.Preregistration, m.Config}
	if digest(m) != digest(legacy) {
		t.Fatal("zero-value design extensions changed historical manifest hash")
	}
	data, _ := json.Marshal(m)
	var reread Manifest
	if err := json.Unmarshal(data, &reread); err != nil {
		t.Fatal(err)
	}
	oldPlan, _ := Plan(m)
	newPlan, _ := Plan(reread)
	if !reflect.DeepEqual(oldPlan, newPlan) {
		t.Fatal("historical saved plan changed on decode")
	}
}

func TestResearchTreatmentStudyIntegration(t *testing.T) {
	if os.Getenv("AGENTMUX_RESEARCH_INTEGRATION") != "1" {
		t.Skip("opt in to actual isolated command study")
	}
	binary := os.Getenv("AGENTMUX_TEST_BINARY")
	if binary == "" {
		t.Fatal("AGENTMUX_TEST_BINARY required")
	}
	m, err := Load(filepath.Join("..", "..", "examples", "research", "public-goods.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m.ID, m.Trials, m.Game.Rounds = "research-treatment-acceptance", 1, 1
	m.Counterbalancing = "latin_square"
	m.RoleAssignments = map[string]map[string]string{"forward": {"alice": "Analyst", "bob": "Critic"}, "reverse": {"alice": "Critic", "bob": "Analyst"}}
	m.PersonalityProfiles = map[string]map[string]string{"cautious": {"alice": "Check evidence before acting."}}
	m.Conditions = []Condition{{ID: "forward", RewardMode: "individual", Visibility: "direct", Teams: true, TeamRewards: "none", RoleAssignment: "forward", Personality: "cautious"}, {ID: "reverse", RewardMode: "individual", Visibility: "direct", Teams: true, TeamRewards: "none", RoleAssignment: "reverse", Personality: "cautious"}}
	m.Preregistration = &Preregistration{Question: "Command protocol acceptance, not model behavior", PrimaryOutcome: "teams_with_multiple_members", Hypotheses: []string{"Programmed strategy forms one team."}, Factors: map[string][]string{"role_assignment": {"forward", "reverse"}}, FixedControls: []string{"Same command strategies and personality prompts"}, MinimumBlocks: 2, Analysis: "Descriptive paired team counts; protocol fixture only."}
	dir, err := os.MkdirTemp("/tmp", "amx-treatment-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	report, err := Run(context.Background(), m, RunOptions{OutputDir: dir, Executable: binary})
	if err != nil {
		t.Fatalf("study: %v; evidence %s", err, dir)
	}
	if len(report.Results) != 4 {
		t.Fatal("wrong completed trial matrix")
	}
	for _, result := range report.Results {
		if result.Status != "completed" || len(result.Rounds) != 1 {
			t.Fatal("incomplete study")
		}
	}
	loaded, err := ReadReport(dir)
	if err != nil {
		t.Fatalf("new design invalidated frozen evidence: %v", err)
	}
	a := Analyze(loaded)
	if a.PrimaryOutcome != "teams_with_multiple_members" || a.Counterbalancing != "latin_square" || len(a.FactorialEffects) != 1 || a.FactorialEffects[0].Difference.N != 2 || a.FactorialEffects[0].Difference.Mean != 0 {
		t.Fatalf("wrong completed-study analysis: %+v", a)
	}
	for _, c := range a.Conditions {
		if c.PrimaryEstimate.Mean != 1 || c.PrimaryEstimate.N != 2 {
			t.Fatal("actual consented teams not scored")
		}
	}
}
