package research

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func exchangeOptions() GameOptions {
	return GameOptions{Kind: ExchangeVersion, Rounds: 2, Credits: 6, PerRoundCap: 2, Multiplier: 3}
}
func transfer(to string, amount int) Input {
	a := InvestmentAction{Amount: amount}
	if amount > 0 {
		a.To = &to
	}
	raw, _ := json.Marshal(a)
	return Input{Raw: string(raw)}
}
func repayment(returns map[string]int, team string) Input {
	raw, _ := json.Marshal(ReturnAction{Returns: returns, RewardTeam: team})
	return Input{Raw: string(raw)}
}

func TestTrustExchangeActualTransfersAndMutualRewardPool(t *testing.T) {
	episode, err := (TrustExchange{}).New(11, testActors, exchangeOptions())
	if err != nil {
		t.Fatal(err)
	}
	g := episode.(*exchangeEpisode)
	if err := g.Invest(map[string]Input{"alice": transfer("bob", 2), "bob": transfer("alice", 1), "charlie": transfer("", 0)}); err != nil {
		t.Fatal(err)
	}
	obs, _ := g.Observe("alice")
	var observation struct {
		Credits  int        `json:"remaining_credits"`
		Received []Transfer `json:"received_escrows"`
	}
	json.Unmarshal(obs, &observation)
	if observation.Credits != 4 || len(observation.Received) != 1 || observation.Received[0].To != "alice" {
		t.Fatal("private escrow observation is wrong")
	}
	g.SetRewardTeams("pool", []RewardTeam{{"team_test", []string{"alice", "bob"}}})
	out, err := g.Resolve(map[string]Input{"alice": repayment(map[string]int{"bob": 1}, "team_test"), "bob": repayment(map[string]int{"alice": 2}, "team_test"), "charlie": repayment(map[string]int{}, "")}, "individual")
	if err != nil {
		t.Fatal(err)
	}
	ma, mb := g.cases[0].Productivity["alice"], g.cases[0].Productivity["bob"]
	if out.Utilities["alice"] != float64(ma-1) || out.Utilities["bob"] != float64(2*mb-2) || out.Exchange.TotalInvested != 3 || out.Exchange.TotalReturned != 3 || out.Exchange.TotalBurned != 0 {
		t.Fatalf("incorrect actual exchange accounting: %+v", out)
	}
	if out.SocialWelfare != float64(ma+2*mb-3) || len(out.RewardPools) != 1 || out.Payoffs["alice"] != out.Payoffs["bob"] || out.Credits["alice"] != 6+ma-1 {
		t.Fatal("pooling changed wallets/welfare or failed to pool mutually selected points")
	}
}

func TestTrustExchangeOverReturnBurnsEscrowAndNoDoublePenalty(t *testing.T) {
	episode, _ := (TrustExchange{}).New(22, testActors, exchangeOptions())
	g := episode.(*exchangeEpisode)
	g.Invest(map[string]Input{"alice": transfer("bob", 2), "bob": transfer("", 0), "charlie": transfer("", 0)})
	out, err := g.Resolve(map[string]Input{"alice": repayment(map[string]int{}, ""), "bob": repayment(map[string]int{"alice": 1000}, ""), "charlie": repayment(map[string]int{}, "")}, "individual")
	if err != nil {
		t.Fatal(err)
	}
	if out.InvalidActions != 1 || out.Exchange.TotalBurned != 2*g.cases[0].Productivity["bob"] || out.Credits["alice"] != 4 || out.Credits["bob"] != 5 || out.SocialWelfare != -3 {
		t.Fatalf("invalid escrow action minted value or double-penalized: %+v", out)
	}
	if _, err := parseReturn(`{"returns":{"alice":1,"alice":2}}`, testActors); err == nil {
		t.Fatal("duplicate sender accepted")
	}
	if _, err := parseInvestment(`{"to":"alice","amount":null}`); err == nil {
		t.Fatal("null amount accepted")
	}
	if _, err := parseAction(`{"project":null,"contribute":0,"reward_team":null}`); err == nil {
		t.Fatal("null reward team accepted")
	}
}

func TestPopulationEntryConservesCreditsAndCases(t *testing.T) {
	episode, _ := (PublicGoods{}).New(11, testActors, gameOptions())
	g := episode.(*goodsEpisode)
	before := g.cases[0].Bits["alice"]
	changes, err := g.setPopulation([]populationMember{{"alice", "", 0}, {"bob", "", 0}, {"charlie", "", 0}, {"child", "alice", 1}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].CreditsTransferred != 1 || g.credits["alice"] != 3 || g.credits["child"] != 1 || g.cases[0].Bits["alice"] != before {
		t.Fatal("join minted credits or changed a prior private clue")
	}
	total := 0
	xor := 0
	for _, id := range g.actors {
		total += g.credits[id]
		xor ^= g.cases[0].Bits[id]
	}
	if total != 12 || xor != g.cases[0].Productive {
		t.Fatal("population resource/case accounting wrong")
	}
	obs, err := g.Observe("child")
	if err != nil || !strings.Contains(string(obs), `"remaining_credits":1`) {
		t.Fatal("new actor missing funded private observation")
	}
}

func TestPopulationAdmissionPreparesNewCodexWorkspaceWithoutAgentHOME(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git required for native Codex workspace preparation")
	}
	dir := t.TempDir()
	m := testManifest()
	cfg := trialConfig(m, m.Conditions[0], filepath.Join(dir, "state"))
	a := cfg.Agents["bob"]
	a.Harness, a.Sandbox, a.Env = "codex", "bubblewrap", nil
	cfg.Agents["bob"] = a
	if err := prepareWorkspaces(context.Background(), cfg, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.Workdir, ".git")); err != nil {
		t.Fatal("initial Codex actor has no repository")
	}
	child := a
	child.Workdir = filepath.Join(dir, "state", "workspaces", "child")
	cfg.Agents["child"] = child
	if err := prepareWorkspaces(context.Background(), cfg, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(child.Workdir, ".git")); err != nil {
		t.Fatal("new Codex child was not prepared at its admission boundary")
	}
	if cfg.Agents["child"].Env["HOME"] != "" || cfg.Agents["bob"].Env["HOME"] != "" {
		t.Fatal("controller Git HOME leaked into native agent configuration")
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".git-controller-home-*"))
	if err != nil || len(matches) != 0 {
		t.Fatal("controller Git HOME not cleaned")
	}
}

func TestDurableNativeBudgetFailsClosedAcrossResume(t *testing.T) {
	dir := t.TempDir()
	b, err := loadBudget(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testManifest().Config
	a := cfg.Agents["alice"]
	a.Harness = "claude"
	cfg.Agents["alice"] = a
	if err := b.reserve(map[string]string{"alice": "task-one"}, cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadBudget(dir, 2)
	if err != nil || len(reloaded.Reserved) != 1 {
		t.Fatal("reservation not durable")
	}
	if err := reloaded.reserve(map[string]string{"alice": "task-one"}, cfg); err == nil {
		t.Fatal("replayed model turn reservation")
	}
	if err := reloaded.reserve(map[string]string{"alice": "task-two"}, cfg); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.reserve(map[string]string{"alice": "task-three"}, cfg); err == nil {
		t.Fatal("exceeded native task budget")
	}
	info, _ := os.Stat(filepath.Join(dir, "model-budget.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("budget ledger is not private")
	}
}

func TestMissingStudyBudgetCannotResetSpending(t *testing.T) {
	m := factorialManifest()
	m.Trials = 1
	m.Seeds = []int64{11}
	m.Game.Rounds = 1
	m.Budget.MaxModelTasks = 64
	a := m.Config.Agents["alice"]
	a.Harness = "claude"
	a.Command = nil
	a.Sandbox = "bubblewrap"
	a.Network = "host"
	m.Config.Agents["alice"] = a
	m = normalize(m)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "manifest.json"), map[string]any{"sha256": digest(m)}); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), m, RunOptions{OutputDir: dir, Executable: "/bin/true", AllowModels: true})
	if err == nil || !strings.Contains(err.Error(), "lacks its durable budget") {
		t.Fatalf("missing spending ledger silently reset: %v", err)
	}
}

func factorialManifest() Manifest {
	m := testManifest()
	m.Conditions = nil
	for _, reward := range []string{"individual", "shared"} {
		for _, teams := range []bool{false, true} {
			id := reward + "-off"
			if teams {
				id = reward + "-on"
			}
			m.Conditions = append(m.Conditions, Condition{ID: id, RewardMode: reward, Visibility: "direct", Teams: teams, TeamRewards: "none"})
		}
	}
	m.Preregistration = &Preregistration{Question: "A declared pilot", PrimaryOutcome: "total_social_welfare", Hypotheses: []string{"Shared incentives may change welfare."}, Factors: map[string][]string{"reward_mode": {"individual", "shared"}, "teams": {"false", "true"}}, FixedControls: []string{"Same actors/roles/tools/worlds"}, MinimumBlocks: 20, Analysis: "Paired factorial effects, descriptive intervals; underpowered pilot."}
	return normalize(m)
}
func TestFactorialContractAndPairedEffects(t *testing.T) {
	m := factorialManifest()
	if _, err := Plan(m); err != nil {
		t.Fatal(err)
	}
	bad := normalize(m)
	bad.Conditions[0].Instructions = "Unregistered instruction treatment"
	if bad.Validate() == nil {
		t.Fatal("undeclared treatment accepted")
	}
	report := Report{Research: "factorial", ManifestHash: "hash", Preregistration: m.Preregistration, Conditions: m.Conditions}
	for _, seed := range []int64{1, 2} {
		for _, c := range m.Conditions {
			reward, teams := 0.0, 0.0
			if c.RewardMode == "shared" {
				reward = 1
			}
			if c.Teams {
				teams = 1
			}
			w := float64(seed) + 2*reward + 3*teams + reward*teams
			report.Results = append(report.Results, Result{Status: "completed", Trial: Trial{Condition: c.ID, Seed: seed, Repetition: 1}, Rounds: []Outcome{{SocialWelfare: w}}})
		}
	}
	a := Analyze(report)
	if len(a.FactorialEffects) != 3 || a.PowerWarning == "" {
		t.Fatal("missing factorial/power metadata")
	}
	means := []float64{}
	for _, effect := range a.FactorialEffects {
		means = append(means, effect.Difference.Mean)
		if effect.Difference.CI95 == nil || effect.Difference.N != 2 {
			t.Fatal("missing paired-block uncertainty")
		}
	}
	if !reflect.DeepEqual(means, []float64{2.5, 1, 3.5}) {
		t.Fatalf("wrong factorial estimands: %v", means)
	}
	// A world with only some completed cells is not a paired factorial block.
	report.Results[len(report.Results)-1].Status = "interrupted"
	a = Analyze(report)
	if a.FactorialEffects[0].Difference.N != 1 || !strings.Contains(a.PowerWarning, "1 complete paired blocks") {
		t.Fatal("incomplete treatment matrix counted as a complete paired block")
	}
}

func TestBehaviorIDsAndUncertaintyUseRunsNotRounds(t *testing.T) {
	report := Report{Research: "behavior", ManifestHash: "hash", Results: []Result{{Status: "completed", Trial: Trial{ID: "trial", Condition: "shared"}, ActorMetadata: map[string]AgentLabel{"alice": {Harness: "claude", Model: "pinned"}}, Rounds: []Outcome{{Round: 1, Actions: map[string]ActionResult{"alice": {Valid: true}}, Utilities: map[string]float64{"alice": 1}}, {Round: 2, Actions: map[string]ActionResult{"alice": {Valid: true}}, Utilities: map[string]float64{"alice": 3}}}}}}
	rows := Behaviors(report)
	if len(rows) != 2 || rows[0].ID == rows[1].ID || rows[0].Harness != "claude" || !reflect.DeepEqual(rows, Behaviors(report)) {
		t.Fatal("behavior identity/provenance wrong")
	}
	summary := Analyze(report).PerAgent[0]
	if summary.Utility.N != 1 || summary.Utility.Mean != 2 || summary.Utility.CI95 != nil {
		t.Fatal("correlated rounds were treated as independent runs")
	}
}

func TestResearchExchangeAndDynamicIntegration(t *testing.T) {
	if os.Getenv("AGENTMUX_RESEARCH_INTEGRATION") != "1" {
		t.Skip("opt in to actual isolated command environments")
	}
	binary := os.Getenv("AGENTMUX_TEST_BINARY")
	if binary == "" {
		t.Fatal("AGENTMUX_TEST_BINARY required")
	}
	for _, name := range []string{"trust-exchange.yaml", "dynamic-population.yaml"} {
		t.Run(name, func(t *testing.T) {
			m, err := Load(filepath.Join("..", "..", "examples", "research", name))
			if err != nil {
				t.Fatal(err)
			}
			m.Seeds = []int64{11}
			dir, err := os.MkdirTemp("/tmp", "amx-rx-")
			if err != nil {
				t.Fatal(err)
			}
			report, err := Run(context.Background(), m, RunOptions{OutputDir: dir, Executable: binary})
			if err != nil {
				t.Fatalf("%v; private evidence %s", err, dir)
			}
			defer os.RemoveAll(dir)
			for _, r := range report.Results {
				if r.Status != "completed" || len(r.Rounds) != 2 {
					t.Fatal("incomplete staged environment")
				}
				for _, out := range r.Rounds {
					if out.InvalidActions != 0 {
						t.Fatalf("fixture produced invalid decisions: %+v", out)
					}
				}
				if name == "trust-exchange.yaml" {
					if r.Rounds[0].Exchange.TotalReturned <= 0 {
						t.Fatal("no actual discretionary repayments")
					}
					if r.Trial.Condition == "pooled" && len(r.Rounds[0].RewardPools) != 1 {
						t.Fatal("consented points pool not applied")
					}
				} else if len(r.Population[0].Actors) != 2 || len(r.Population[1].Actors) != 3 || len(r.Population[1].Changes) != 1 || r.Population[1].Changes[0].CreditsTransferred != 1 {
					t.Fatalf("next-round child/resource handoff missing: %+v", r.Population)
				}
			}
		})
	}
}
