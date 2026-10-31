package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentmux/internal/managed"
)

func liveFixture(t *testing.T) (*liveRunner, func(string, int), func() managed.ResearchStatus) {
	t.Helper()
	m, err := Load(filepath.Join("..", "..", "examples", "research", "public-goods.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "amx-live-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := managed.Init(filepath.Join(dir, "state"), m.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	proof, err := s.InitResearchRunner()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := managed.LoadCredential(managed.CredentialPath(filepath.Join(dir, "state"), ""))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	rpc := func(ctx context.Context, op string, args any) (json.RawMessage, error) {
		n++
		data, _ := json.Marshal(args)
		raw, problem := s.Call(ctx, owner.Token, managed.Request{Operation: op, Args: data, ResearchRunnerProof: proof, RequestID: fmt.Sprintf("runner-%d", n)})
		if problem != nil {
			return raw, problem
		}
		return raw, nil
	}
	l := &liveRunner{call: rpc, path: filepath.Join(dir, "live.json"), budget: &modelBudget{}, progress: managed.ResearchProgress{Trial: "run-001", Attempt: 1, Condition: "board", StateDir: filepath.Join(dir, "state"), State: "starting", Tasks: map[string]string{}, Population: []string{"alice", "bob"}, AckVersion: 1, Deadline: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}}
	if _, err := l.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	control := func(action string, version int) {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"action": action, "version": version, "reason": "inspect the bounded test"})
		if _, problem := s.Call(context.Background(), owner.Token, managed.Request{Operation: "research.control", RequestID: fmt.Sprintf("human-%d", version), Args: data}); problem != nil {
			t.Fatal(problem)
		}
	}
	status := func() managed.ResearchStatus {
		t.Helper()
		raw, problem := s.Call(context.Background(), owner.Token, managed.Request{Operation: "research.status"})
		if problem != nil {
			t.Fatal(problem)
		}
		var response struct {
			Research managed.ResearchStatus `json:"research"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		return response.Research
	}
	return l, control, status
}
func waitLiveState(t *testing.T, status func() managed.ResearchStatus, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if status().Progress.State == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("research never reached %s", want)
}
func TestLiveBarriersGrantExactlyOnePhaseAndKeepDeadline(t *testing.T) {
	l, control, status := liveFixture(t)
	deadline := l.progress.Deadline
	control("pause", 1)
	done := make(chan error, 1)
	go func() { done <- l.barrier(context.Background(), 1, "coordinate", []string{"alice", "bob"}) }()
	waitLiveState(t, status, "paused")
	select {
	case err := <-done:
		t.Fatalf("paused barrier advanced: %v", err)
	default:
	}
	control("step", 2)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	go func() { done <- l.barrier(context.Background(), 1, "act", []string{"alice", "bob"}) }()
	waitLiveState(t, status, "paused")
	if state := status(); state.Progress.Deadline != deadline || state.Progress.AckVersion != 3 || len(state.Progress.TaskIDs) != 0 || state.Progress.ModelTasksReserved != 0 {
		t.Fatalf("pause reset deadline/spent work: %+v", state)
	}
	control("step", 3)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	control("stop", 4)
	if err := l.barrier(context.Background(), 2, "coordinate", []string{"alice", "bob"}); !errors.Is(err, ErrResearchStopped) {
		t.Fatalf("stop did not fence next phase: %v", err)
	}
	if state := status(); state.Progress.State != "stopped" || state.Control.Version != 5 || len(state.Control.Interventions) != 4 {
		t.Fatalf("lost intervention provenance: %+v", state)
	}
}
func TestLivePauseHonorsExistingDeadline(t *testing.T) {
	l, control, status := liveFixture(t)
	control("pause", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := l.barrier(ctx, 1, "coordinate", []string{"alice", "bob"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("paused barrier ignored deadline: %v", err)
	}
	if status().Progress.ModelTasksReserved != 0 {
		t.Fatal("paused study reserved model work")
	}
}

func TestLiveStatusReopensExplicitStateWithoutBroker(t *testing.T) {
	l, _, _ := liveFixture(t)
	l.progress.State = "completed"
	if _, err := l.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := LiveStatus(context.Background(), l.progress.StateDir)
	if err != nil || view.Live || view.Research == nil || view.Research.Progress.State != "completed" || view.Research.RunnerHeartbeatAt != "" {
		t.Fatalf("explicit STATE could not reopen final saved metadata: %+v %v", view, err)
	}
}

// Successful responses advance only runner metadata; injection points below
// deterministically place context cancellation on either side of each RPC.
func liveRPCFixture(t *testing.T, mode string) *liveRunner {
	t.Helper()
	l := &liveRunner{path: filepath.Join(t.TempDir(), "live.json"), budget: &modelBudget{}, progress: managed.ResearchProgress{Trial: "run-context", Attempt: 1, Revision: 1, AckVersion: 1, State: "starting"}}
	l.call = func(_ context.Context, operation string, args any) (json.RawMessage, error) {
		p := l.progress
		if operation == "research.publish" {
			p = args.(managed.ResearchProgress)
		}
		return json.Marshal(map[string]any{"research": managed.ResearchStatus{Progress: p, Control: managed.ResearchControl{Mode: mode, Version: 2}}})
	}
	return l
}

func TestLiveRPCContextCancellationRetainsCallerError(t *testing.T) {
	for _, stage := range []string{"publish", "status", "paused_publish"} {
		t.Run(stage, func(t *testing.T) {
			l := liveRPCFixture(t, "paused")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := l.call
			injected := false
			l.call = func(ctx context.Context, op string, args any) (json.RawMessage, error) {
				matches := stage == "publish" && op == "research.publish" || stage == "status" && op == "research.status"
				if stage == "paused_publish" && op == "research.publish" {
					matches = args.(managed.ResearchProgress).State == "paused"
				}
				if matches {
					injected = true
					cancel()
					return nil, &managed.Fault{Code: "cancelled", Message: "request cancelled"}
				}
				return original(ctx, op, args)
			}
			if err := l.barrier(ctx, 1, "coordinate", []string{"alice", "bob"}); !injected || !errors.Is(err, context.Canceled) {
				t.Fatalf("RPC cancellation lost caller context: injected=%v err=%v", injected, err)
			}
		})
	}
}

func TestLiveSuccessfulRPCAtCancellationNeverReleasesPhase(t *testing.T) {
	for _, mode := range []string{"running", "step"} {
		for _, stage := range []string{"status", "admission"} {
			t.Run(mode+"/"+stage, func(t *testing.T) {
				l := liveRPCFixture(t, mode)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				original := l.call
				calls := 0
				l.call = func(ctx context.Context, op string, args any) (json.RawMessage, error) {
					calls++
					raw, err := original(ctx, op, args)
					if stage == "status" && op == "research.status" || stage == "admission" && op == "research.publish" && args.(managed.ResearchProgress).State == "running" {
						cancel()
					}
					return raw, err
				}
				if err := l.barrier(ctx, 1, "coordinate", []string{"alice", "bob"}); !errors.Is(err, context.Canceled) {
					t.Fatalf("successful RPC released work after cancellation: %v", err)
				}
				if l.stepVersion != 0 {
					t.Fatal("expired step consumed admission")
				}
				if stage == "admission" && (calls != 3 || l.progress.Revision != 3 || l.progress.State != "running") {
					t.Fatalf("committed revision lost or admission retried: calls=%d progress=%+v", calls, l.progress)
				}
				if stage == "status" && (calls != 2 || l.progress.State != "barrier") {
					t.Fatal("status read admitted a phase after cancellation")
				}
			})
		}
	}
}

func TestLivePausedSelectAndCommittedOwnerStopKeepTheirMeaning(t *testing.T) {
	for _, mode := range []string{"paused", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			l := liveRPCFixture(t, mode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := l.call
			l.call = func(ctx context.Context, op string, args any) (json.RawMessage, error) {
				raw, err := original(ctx, op, args)
				if op == "research.publish" && args.(managed.ResearchProgress).State == mode {
					cancel()
				}
				return raw, err
			}
			want := error(context.Canceled)
			if mode == "stopped" {
				want = ErrResearchStopped
			}
			if err := l.barrier(ctx, 1, "coordinate", []string{"alice", "bob"}); !errors.Is(err, want) {
				t.Fatalf("paused select or committed owner stop changed meaning: %v", err)
			}
			if l.progress.State != mode {
				t.Fatal("terminal/paused publication was discarded")
			}
		})
	}
}

func TestLiveRPCFaultWithLiveContextIsNotRewrittenOrRetried(t *testing.T) {
	for _, operation := range []string{"research.publish", "research.status"} {
		for _, code := range []string{"cancelled", "internal", "conflict"} {
			t.Run(operation+"/"+code, func(t *testing.T) {
				l := liveRPCFixture(t, "paused")
				original := l.call
				problem := &managed.Fault{Code: code, Message: "definitive broker fault"}
				faults := 0
				l.call = func(ctx context.Context, op string, args any) (json.RawMessage, error) {
					if op == operation {
						faults++
						return nil, problem
					}
					return original(ctx, op, args)
				}
				if err := l.barrier(context.Background(), 1, "coordinate", []string{"alice", "bob"}); err != problem || faults != 1 {
					t.Fatalf("live-context fault rewritten/retried: faults=%d err=%v", faults, err)
				}
			})
		}
	}
}

func TestLiveAdmissionDoesNotCrossCommittedOwnerStop(t *testing.T) {
	for _, step := range []bool{false, true} {
		t.Run(fmt.Sprint("step=", step), func(t *testing.T) {
			l, control, status := liveFixture(t)
			version := 1
			if step {
				control("step", version)
				version++
			}
			original := l.call
			injected := false
			l.call = func(ctx context.Context, operation string, args any) (json.RawMessage, error) {
				raw, err := original(ctx, operation, args)
				if operation == "research.status" && err == nil && !injected {
					injected = true
					control("stop", version)
				}
				return raw, err
			}
			if err := l.barrier(context.Background(), 1, "invest", []string{"alice", "bob"}); !errors.Is(err, ErrResearchStopped) {
				t.Fatalf("phase admitted after owner stop committed: %v", err)
			}
			if got := status(); got.Progress.State != "stopped" || got.Progress.AckVersion != version+1 || l.stepVersion != 0 {
				t.Fatalf("stop was bypassed or step consumed: %+v", got)
			}
		})
	}
}
func TestLivePointerCannotSelectAnotherPrivateStudy(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	view := LiveView{Research: &managed.ResearchStatus{Progress: managed.ResearchProgress{StateDir: filepath.Join(t.TempDir(), "run-001", "attempt-001", "state")}}}
	if err := writeJSON(filepath.Join(dir, "live.json"), view); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLiveState(dir); err == nil {
		t.Fatal("live pointer escaped study root")
	}
}
func TestOwnerInterventionsAreExcludedFromDefaultEstimates(t *testing.T) {
	r := Report{Research: "test", Results: []Result{
		{Status: "completed", Trial: Trial{Condition: "shared", Seed: 1, Repetition: 1}, Rounds: []Outcome{{SocialWelfare: 3}}},
		{Status: "completed", Trial: Trial{Condition: "shared", Seed: 2, Repetition: 1}, Rounds: []Outcome{{SocialWelfare: 900}}, OwnerMutations: 1},
		{Status: "completed", Trial: Trial{Condition: "shared", Seed: 3, Repetition: 1}, Rounds: []Outcome{{SocialWelfare: 900}}, Interventions: []managed.ResearchIntervention{{Action: "pause"}}},
	}}
	a := Analyze(r)
	if a.IntervenedRuns != 2 || len(a.Conditions) != 1 || a.Conditions[0].CompletedRuns != 1 || a.Conditions[0].SocialWelfare.Mean != 3 {
		t.Fatalf("intervened runs mixed into estimates: %+v", a)
	}
}

func TestInterventionEvidenceCannotBeStrippedFromNewRuns(t *testing.T) {
	l, control, _ := liveFixture(t)
	control("pause", 1)
	l.progress.State = "completed"
	l.progress.AckVersion = 2
	status, err := l.publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(l.path)
	r := Result{Status: "completed", Trial: Trial{ID: l.progress.Trial}, Attempt: 1, InterventionProtocol: interventionProtocol, Interventions: status.Control.Interventions}
	artifact := interventionEvidence{Protocol: interventionProtocol, Interventions: r.Interventions}
	if err := writeJSON(filepath.Join(dir, "interventions.json"), artifact); err != nil {
		t.Fatal(err)
	}
	if err := verifyInterventions(dir, r); err != nil {
		t.Fatal(err)
	}
	r.Interventions = nil
	if err := verifyInterventions(dir, r); err == nil {
		t.Fatal("stripped result became clean evidence")
	}
	artifact.Interventions = nil
	writeJSON(filepath.Join(dir, "interventions.json"), artifact)
	if err := verifyInterventions(dir, r); err == nil {
		t.Fatal("matching stripped result and artifact bypassed durable broker intervention")
	}
	m, trial, cacheDir, cached, budget := cacheFixture(t, false)
	if err := checkCachedResult(m, trial, 1, cacheDir, cached, budget, map[string]bool{}, true, interventionProtocol); err == nil {
		t.Fatal("new execution collection accepted missing intervention protocol")
	}
}

func TestExchangeStepDoesNotAdvanceThroughReturnAndNextInvestment(t *testing.T) {
	l, control, status := liveFixture(t)
	control("step", 1)
	if err := l.barrier(context.Background(), 1, "invest", []string{"alice", "bob"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- l.barrier(context.Background(), 1, "return", []string{"alice", "bob"}) }()
	waitLiveState(t, status, "paused")
	control("step", 2)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	go func() { done <- l.barrier(context.Background(), 2, "invest", []string{"alice", "bob"}) }()
	waitLiveState(t, status, "paused")
	if p := status().Progress; p.Round != 2 || p.Phase != "invest" || len(p.TaskIDs) != 0 {
		t.Fatalf("exchange phase step leaked into next round: %+v", p)
	}
	control("stop", 3)
	if err := <-done; !errors.Is(err, ErrResearchStopped) {
		t.Fatal(err)
	}
}
func TestResearchRejectsNativeInteractiveParticipantsBeforeExecution(t *testing.T) {
	m, err := Load(filepath.Join("..", "..", "examples", "research", "public-goods.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	a := m.Config.Agents["alice"]
	a.Interaction = "interactive"
	m.Config.Agents["alice"] = a
	if _, err := Plan(m); err == nil {
		t.Fatal("headless research admitted unsupported interactive participant")
	}
}

func TestResearchStartPausedControlIntegration(t *testing.T) {
	if os.Getenv("AGENTMUX_RESEARCH_INTEGRATION") != "1" {
		t.Skip("opt in to real sandboxed model-free research with AGENTMUX_TEST_BINARY")
	}
	binary := os.Getenv("AGENTMUX_TEST_BINARY")
	if binary == "" {
		t.Fatal("AGENTMUX_TEST_BINARY required")
	}
	m, err := Load(filepath.Join("..", "..", "examples", "research", "public-goods.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m.Conditions = m.Conditions[:1]
	m.Seeds = []int64{11}
	m.Game.Rounds = 1
	dir, err := os.MkdirTemp("/tmp", "amx-live-run-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := make(chan string, 1)
	type outcome struct {
		report Report
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		report, err := Run(ctx, m, RunOptions{OutputDir: dir, Executable: binary, StartPaused: true, OnLive: func(state string) { started <- state }})
		finished <- outcome{report, err}
	}()
	select {
	case <-started:
	case result := <-finished:
		t.Fatalf("runner exited before live state: %v", result.err)
	case <-ctx.Done():
		t.Fatal("runner did not publish live state")
	}
	wait := func(phase string) LiveView {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			view, err := LiveStatus(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			if view.Live && view.Research != nil && view.Research.Progress.State == "paused" && view.Research.Progress.Phase == phase {
				return view
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("runner never paused before %s", phase)
		return LiveView{}
	}
	view := wait("coordinate")
	if len(view.Research.Progress.TaskIDs) != 0 || view.Research.Progress.ModelTasksReserved != 0 {
		t.Fatal("start-paused queued or reserved work")
	}
	if _, err := ControlLive(ctx, dir, "step", "observe exactly one coordination phase", view.Research.Control.Version); err != nil {
		t.Fatal(err)
	}
	view = wait("act")
	if _, err := ControlLive(ctx, dir, "stop", "end controlled smoke before decision phase", view.Research.Control.Version); err != nil {
		t.Fatal(err)
	}
	result := <-finished
	if !errors.Is(result.err, ErrResearchStopped) || len(result.report.Results) != 1 {
		t.Fatalf("unexpected stopped outcome: %+v", result)
	}
	r := result.report.Results[0]
	if len(r.Interventions) != 3 || r.OwnerMutations != 0 || r.Error != "owner stopped this attempt at a phase barrier; evidence and budget retained; no automatic rerun" {
		t.Fatalf("lost intentional intervention evidence: %+v", r)
	}
	view, err = LiveStatus(context.Background(), dir)
	if err != nil || view.Live || view.Research.Progress.State != "stopped" {
		t.Fatalf("stopped state was not retained: %+v %v", view, err)
	}
}
