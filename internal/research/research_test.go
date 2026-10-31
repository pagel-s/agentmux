package research

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

var testActors = []string{"alice", "bob", "charlie"}

func gameOptions() GameOptions {
	return GameOptions{Kind: GameVersion, Rounds: 3, Credits: 4, PerRoundCap: 2, Threshold: 3, Dividend: 3}
}
func project(g Episode) int {
	var cases []goodsCase
	if err := json.Unmarshal(g.GroundTruth(), &cases); err != nil {
		panic(err)
	}
	return cases[g.(*goodsEpisode).round].Productive
}
func action(p, contribution int) Input {
	a := Action{Contribute: contribution}
	if contribution > 0 {
		a.Project = &p
	}
	raw, _ := json.Marshal(a)
	return Input{Raw: string(raw)}
}

func TestPrivateObservationsAndReproducibleCases(t *testing.T) {
	a, _ := (PublicGoods{}).New(11, testActors, gameOptions())
	b, _ := (PublicGoods{}).New(11, []string{"charlie", "alice", "bob"}, gameOptions())
	if string(a.GroundTruth()) != string(b.GroundTruth()) {
		t.Fatal("actor ordering changed paired world")
	}
	observation, err := a.Observe("alice")
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	json.Unmarshal(observation, &data)
	if _, ok := data["private_bit"]; !ok {
		t.Fatal("missing own clue")
	}
	for _, hidden := range []string{"seed", "private_bits", "productive_project", "cases"} {
		if _, ok := data[hidden]; ok {
			t.Fatalf("hidden data leaked: %s", hidden)
		}
	}
	if _, err := a.Observe("unknown"); err == nil {
		t.Fatal("unknown observer accepted")
	}
}

func TestActualCostsFeedbackAndRewardMechanisms(t *testing.T) {
	for _, mode := range []string{"shared", "individual", "winner"} {
		t.Run(mode, func(t *testing.T) {
			g, _ := (PublicGoods{}).New(22, testActors, gameOptions())
			p := project(g)
			out, err := g.Resolve(map[string]Input{"alice": action(p, 1), "bob": action(p, 2), "charlie": action(0, 0)}, mode)
			if err != nil {
				t.Fatal(err)
			}
			if !out.Funded || out.TotalCost != 3 || out.Credits["bob"] != 2 || out.SocialWelfare != 6 || out.FreeRiders != 1 {
				t.Fatalf("wrong objective outcome: %+v", out)
			}
			want := map[string]float64{"alice": 2, "bob": 1, "charlie": 3}
			if mode == "shared" {
				want = map[string]float64{"alice": 2, "bob": 2, "charlie": 2}
			}
			if mode == "winner" {
				want = map[string]float64{"alice": 0, "bob": 0, "charlie": 1}
			}
			if !reflect.DeepEqual(out.Payoffs, want) {
				t.Fatalf("payoffs %v want %v", out.Payoffs, want)
			}
			obs, _ := g.Observe("bob")
			var feedback struct {
				Credits int     `json:"remaining_credits"`
				Payoff  float64 `json:"cumulative_payoff"`
			}
			json.Unmarshal(obs, &feedback)
			if feedback.Credits != 2 || feedback.Payoff != want["bob"] {
				t.Fatal("next-round observation omitted actual credit/payoff feedback")
			}
		})
	}
}

func TestStrictActionsBudgetAndInvalidPenalty(t *testing.T) {
	for _, raw := range []string{`{}`, `{"project":0,"contribute":1,"other":0}`, `{"project":0,"project":1,"contribute":1}`, `{"project":0,"contribute":1.5}`, `{"project":0,"contribute":null}`, `{"project":null,"contribute":1}`, `{"project":1,"contribute":0}`, `{"project":0,"contribute":1} {}`, "```json\n{}\n```"} {
		if _, err := parseAction(raw); err == nil {
			t.Fatalf("accepted malformed action: %s", raw)
		}
	}
	g, _ := (PublicGoods{}).New(11, testActors, gameOptions())
	p := project(g)
	first, err := g.Resolve(map[string]Input{"alice": action(1-p, 2), "bob": action(1-p, 2), "charlie": action(0, 0)}, "individual")
	if err != nil {
		t.Fatal(err)
	}
	if first.Funded || first.Utilities["alice"] != -2 || first.Credits["alice"] != 2 {
		t.Fatal("wrong-project spending was not charged")
	}
	second, err := g.Resolve(map[string]Input{"alice": action(0, 3), "bob": {Raw: `{}`, Failure: "failed"}, "charlie": action(0, 0)}, "individual")
	if err != nil {
		t.Fatal(err)
	}
	if second.InvalidActions != 2 || second.Actions["alice"].Cost != 1 || second.Credits["alice"] != 1 || second.Utilities["alice"] != -1 {
		t.Fatalf("invalid penalty/budget wrong: %+v", second)
	}
	third, err := g.Resolve(map[string]Input{"alice": action(0, 2), "bob": action(0, 0), "charlie": action(0, 0)}, "individual")
	if err != nil {
		t.Fatal(err)
	}
	if third.Actions["alice"].Valid || third.Credits["alice"] != 0 || !g.Done() {
		t.Fatal("remaining balance not enforced")
	}
	if _, err := g.Observe("alice"); err == nil {
		t.Fatal("completed episode observable as new round")
	}
	if _, err := g.Resolve(map[string]Input{}, "shared"); err == nil {
		t.Fatal("completed episode resolved twice")
	}
}

func TestBaselinesReplaySameScarceWorld(t *testing.T) {
	for _, policy := range []string{"all-defect", "random", "oracle-cooperative"} {
		a, err := Baseline(11, testActors, gameOptions(), "individual", policy)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := Baseline(11, testActors, gameOptions(), "individual", policy)
		if !reflect.DeepEqual(a, b) || len(a) != 3 {
			t.Fatal("baseline not reproducible")
		}
		for _, r := range a {
			for _, credits := range r.Credits {
				if credits < 0 {
					t.Fatal("baseline created negative credits")
				}
			}
			if policy == "all-defect" && (r.Funded || r.TotalCost != 0 || r.SocialWelfare != 0) {
				t.Fatal("defection baseline mismatch")
			}
			if policy == "oracle-cooperative" && r.SocialWelfare < 0 {
				t.Fatal("oracle comparator spent on negative welfare")
			}
		}
	}
}

func testManifest() Manifest {
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "research-test", Agents: map[string]managedconfig.Agent{}, Policy: managedconfig.Policy{Default: "deny", Grants: []managedconfig.Grant{{From: []string{"*"}, To: []string{"*"}, Actions: []string{"message.send", "ping.send", "instruct"}}}}}
	for _, id := range testActors {
		cfg.Agents[id] = managedconfig.Agent{Harness: "command", Command: []string{"/bin/cat"}, Sandbox: "local", Env: map[string]string{"EXPLICIT": "value"}}
	}
	return normalize(Manifest{Version: 1, ID: "research-test", Trials: 2, Seeds: []int64{11, 22}, RandomizationSeed: 741, Game: gameOptions(), Conditions: []Condition{{ID: "shared", RewardMode: "shared", Visibility: "board"}, {ID: "teams", RewardMode: "individual", Visibility: "direct", Teams: true}}, Config: cfg})
}

func TestBalancedPlanningAndControlledConfiguration(t *testing.T) {
	m := testManifest()
	plan, err := Plan(m)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Plan(m)
	if !reflect.DeepEqual(plan, again) || len(plan.Trials) != 8 || plan.TotalTasks != 144 {
		t.Fatalf("plan mismatch: %+v", plan)
	}
	counts := map[string]int{}
	for _, trial := range plan.Trials {
		key := trial.Condition + "/" + string(rune(trial.Seed)) + "/" + string(rune(trial.Repetition))
		counts[key]++
	}
	for _, n := range counts {
		if n != 1 {
			t.Fatal("unbalanced matrix")
		}
	}
	a := m.Config.Agents["alice"]
	a.Autonomy = managedconfig.Autonomy{Enabled: true, Boards: []string{"*"}}
	m.Config.Agents["alice"] = a
	cfg := trialConfig(m, m.Conditions[1], "/tmp/private-test-state")
	if cfg.Can("alice", "instruct", "bob") || !cfg.Can("alice", "message.send", "bob") || !cfg.Teams.Enabled || cfg.Spawn.Enabled || cfg.Agents["alice"].Autonomy.Enabled {
		t.Fatal("research controls leaked extra execution authority")
	}
	cfg.Agents["alice"].Env["EXPLICIT"] = "changed"
	if m.Config.Agents["alice"].Env["EXPLICIT"] != "value" {
		t.Fatal("trial mutated source config")
	}
	isolated := trialConfig(m, Condition{Visibility: "isolated"}, "/tmp/private-state-2")
	if len(isolated.Boards) != 0 || len(isolated.Memory) != 0 || len(isolated.Policy.Grants) != 0 {
		t.Fatal("isolated condition retained shared channels")
	}
	for _, agent := range cfg.Agents {
		if _, exists := agent.Env["AGENTMUX_EXPERIMENT_SEED"]; exists {
			t.Fatal("owner world seed exposed")
		}
	}
}

func TestManifestStrictnessAndModelOptIn(t *testing.T) {
	dir := t.TempDir()
	team := filepath.Join(dir, "team.yaml")
	os.WriteFile(team, []byte("version: 2\nmode: managed\nsession: test\nagents:\n  alice: {harness: command, command: [/bin/cat]}\n  bob: {harness: command, command: [/bin/cat]}\npolicy: {default: deny}\n"), 0600)
	base := "version: 1\nid: strict\nteam: team.yaml\nconditions:\n  - {id: a, reward_mode: shared, visibility: board}\n"
	for _, tail := range []string{"unknown: true\n", "---\nversion: 1\n", "game: {kind: public-goods-v1, unknown: true}\n"} {
		path := filepath.Join(dir, "manifest.yaml")
		os.WriteFile(path, []byte(base+tail), 0600)
		if _, err := Load(path); err == nil {
			t.Fatalf("accepted invalid manifest: %s", tail)
		}
	}
	m := testManifest()
	a := m.Config.Agents["alice"]
	a.Harness = "claude"
	a.Command = nil
	a.Sandbox = "bubblewrap"
	a.Network = "host"
	m.Config.Agents["alice"] = a
	if _, err := Run(context.Background(), m, RunOptions{}); err == nil || !strings.Contains(err.Error(), "allow-models") {
		t.Fatalf("model opt-in missing: %v", err)
	}
}

func TestNativeAuthTrialLeavesPrivateHomeToSandbox(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			m := testManifest()
			a := m.Config.Agents["alice"]
			a.Harness, a.Command, a.Sandbox, a.Network = harness, nil, "bubblewrap", "host"
			a.Auth.File = "/private/owner-native-auth.json"
			a.Conversation = "persistent"
			if harness == "pi" {
				a.Auth.Provider = "provider"
			}
			m.Config.Agents["alice"] = a
			cfg := trialConfig(m, m.Conditions[0], "/tmp/research-auth-regression")
			if err := cfg.Validate(); err != nil {
				t.Fatalf("native auth trial config rejected: %v", err)
			}
			for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
				if _, exists := cfg.Agents["alice"].Env[key]; exists {
					t.Fatalf("sandbox home override %s leaked into native auth configuration", key)
				}
			}
			if cfg.Agents["alice"].Conversation != "persistent" {
				t.Fatal("explicit within-trial persistence was discarded")
			}
			if cfg.Agents["bob"].Env["HOME"] != "/tmp/research-auth-regression/workspaces/bob/.home" {
				t.Fatal("trusted local fixture lost its dedicated home")
			}
		})
	}
}

func TestResumePreservesInterruptedAttempt(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "attempt-001"), 0700)
	if _, _, err := resume(dir, false); err == nil {
		t.Fatal("silently retried interrupted run")
	}
	n, prior, err := resume(dir, true)
	if err != nil || n != 2 || prior != nil {
		t.Fatal("explicit rerun did not preserve old attempt")
	}
	result := Result{Status: "completed", ConfigHash: "hash"}
	writeJSON(filepath.Join(dir, "attempt-001", "result.json"), result)
	n, prior, err = resume(dir, false)
	if err != nil || n != 1 || prior == nil {
		t.Fatal("completed run not reused")
	}
}

func TestPairedAnalysisUsesSameWorldBlocks(t *testing.T) {
	report := Report{Research: "test", ManifestHash: "hash"}
	for _, row := range []struct {
		c string
		s int64
		w float64
	}{{"a", 1, 2}, {"b", 1, 5}, {"a", 2, 10}, {"b", 2, 13}} {
		report.Results = append(report.Results, Result{Status: "completed", Trial: Trial{Condition: row.c, Seed: row.s, Repetition: 1}, Rounds: []Outcome{{SocialWelfare: row.w, Funded: true}}})
	}
	analysis := Analyze(report)
	if len(analysis.PairedContrasts) != 1 || analysis.PairedContrasts[0].Difference.N != 2 || analysis.PairedContrasts[0].Difference.Mean != 3 || *analysis.PairedContrasts[0].Difference.StandardError != 0 {
		t.Fatalf("unpaired contrast: %+v", analysis)
	}
}

func TestScriptedRoundsAndConsentedTeamsIntegration(t *testing.T) {
	if os.Getenv("AGENTMUX_RESEARCH_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_RESEARCH_INTEGRATION=1 and AGENTMUX_TEST_BINARY for real isolated fixture")
	}
	binary := os.Getenv("AGENTMUX_TEST_BINARY")
	if binary == "" {
		t.Fatal("AGENTMUX_TEST_BINARY required")
	}
	m, err := Load(filepath.Join("..", "..", "examples", "research", "public-goods.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m.Conditions = []Condition{m.Conditions[1]}
	m.Seeds = []int64{11}
	m.Trials = 1
	dir, err := os.MkdirTemp("/tmp", "amx-r-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	report, err := Run(context.Background(), m, RunOptions{OutputDir: dir, Executable: binary})
	if err != nil {
		t.Fatalf("integration: %v (private trace %s)", err, dir)
	}
	if len(report.Results) != 1 || report.Results[0].Status != "completed" || len(report.Results[0].Rounds) != 2 {
		t.Fatal("finite multi-round run incomplete")
	}
	for _, out := range report.Results[0].Rounds {
		if !out.Funded || out.InvalidActions != 0 || out.TotalCost != 4 {
			t.Fatalf("invalid scripted outcome: %+v", out)
		}
	}
	formed := false
	for _, p := range report.Results[0].Phases {
		for _, team := range p.Teams {
			if team.Owner == "alice" && reflect.DeepEqual(team.Members, []string{"alice", "bob"}) && len(team.Invited) == 0 {
				formed = true
			}
		}
	}
	if !formed {
		t.Fatal("authenticated invitation and voluntary join not observed")
	}
	resumed, err := Run(context.Background(), m, RunOptions{OutputDir: dir, Executable: binary})
	if err != nil || !reflect.DeepEqual(report, resumed) {
		t.Fatal("completed trial was replayed or changed")
	}
}
