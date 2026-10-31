package experiment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentmux/internal/harness"
	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

type RunOptions struct {
	OutputDir        string
	Executable       string
	RerunInterrupted bool
	AllowModels      bool
	OnLive           func(state string)
}

// LiveState locates the current single-task pilot. It carries no credentials,
// observations, instructions, or research phase-control authority.
type LiveState struct {
	Kind     string `json:"kind"`
	Trial    string `json:"trial"`
	Attempt  int    `json:"attempt"`
	StateDir string `json:"state_dir"`
	Deadline string `json:"deadline"`
	State    string `json:"state"`
}

type AssertionResult struct {
	Agent      string  `json:"agent"`
	Passed     bool    `json:"passed"`
	Weight     float64 `json:"weight"`
	Comparison string  `json:"comparison"`
}
type Result struct {
	Trial             Trial                   `json:"trial"`
	Attempt           int                     `json:"attempt"`
	Status            string                  `json:"status"`
	StartedAt         string                  `json:"started_at"`
	FinishedAt        string                  `json:"finished_at"`
	ConfigHash        string                  `json:"config_sha256"`
	Instructions      map[string]string       `json:"instructions"`
	Tasks             map[string]managed.Task `json:"tasks"`
	Assertions        []AssertionResult       `json:"assertions"`
	Scores            map[string]float64      `json:"objective_scores"`
	Payoffs           map[string]float64      `json:"payoffs"`
	RewardMode        string                  `json:"reward_mode"`
	RewardEnforcement string                  `json:"reward_enforcement"`
	Error             string                  `json:"error,omitempty"`
}
type Report struct {
	Experiment   string   `json:"experiment"`
	ManifestHash string   `json:"manifest_sha256"`
	Results      []Result `json:"results"`
}
type snapshot struct{ Agent, Harness, Model, Executable, SHA256, Version, Preflight string }

// Run never reuses an agent workspace or broker memory between trials. Local
// mode remains a trusted process, so those directories are not a hostile-agent
// confidentiality boundary. Use a supported isolated backend for that property.
func Run(ctx context.Context, m Manifest, opts RunOptions) (Report, error) {
	m = normalize(m)
	trials, err := Plan(m)
	if err != nil {
		return Report{}, err
	}
	for _, a := range m.Config.Agents {
		if a.Harness != "command" && !opts.AllowModels {
			return Report{}, errors.New("model harnesses require --allow-models; a run can incur provider charges")
		}
	}
	dir, err := privateDir(opts.OutputDir)
	if err != nil {
		return Report{}, err
	}
	unlock, err := lockOutput(filepath.Join(dir, ".runner.lock"))
	if err != nil {
		return Report{}, errors.New("another runner owns this output directory or its lock is unavailable")
	}
	defer unlock()
	manifestHash := digest(m)
	report := Report{Experiment: m.ID, ManifestHash: manifestHash, Results: []Result{}}
	manifestPath := filepath.Join(dir, "manifest.json")
	var prior struct {
		Hash     string   `json:"sha256"`
		Manifest Manifest `json:"manifest"`
	}
	if data, err := os.ReadFile(manifestPath); err == nil {
		if json.Unmarshal(data, &prior) != nil || prior.Hash != manifestHash {
			return report, errors.New("output belongs to a different manifest or resolved team configuration")
		}
	} else if !os.IsNotExist(err) {
		return report, err
	} else {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return report, err
		}
		for _, entry := range entries {
			if entry.Name() != ".runner.lock" {
				return report, errors.New("new experiment output directory must be empty")
			}
		}
		if err := writeJSON(manifestPath, map[string]any{"sha256": manifestHash, "manifest": m, "observation_scope": "coordination records and runner calls; full native model context and reads are not captured"}); err != nil {
			return report, err
		}
		if err := writeJSON(filepath.Join(dir, "plan.json"), trials); err != nil {
			return report, err
		}
	}
	for _, trial := range trials {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		trialDir := filepath.Join(dir, trial.ID)
		if err := os.MkdirAll(trialDir, 0700); err != nil {
			return report, err
		}
		attempt, completed, err := resumeTrial(trialDir, opts.RerunInterrupted)
		if err != nil {
			return report, fmt.Errorf("%s: %w", trial.ID, err)
		}
		if completed != nil {
			if completed.Trial != trial || completed.ConfigHash == "" {
				return report, errors.New("completed trial metadata does not match the plan")
			}
			report.Results = append(report.Results, *completed)
			continue
		}
		condition := conditionByID(m, trial.Condition)
		result, err := runTrial(ctx, m, trial, condition, attempt, filepath.Join(trialDir, fmt.Sprintf("attempt-%03d", attempt)), opts.Executable, opts.OnLive)
		report.Results = append(report.Results, result)
		if writeErr := writeJSON(filepath.Join(dir, "results.json"), report); writeErr != nil {
			return report, writeErr
		}
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

func resumeTrial(dir string, rerun bool) (int, *Result, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil, err
	}
	last := 0
	for _, entry := range entries {
		var attempt int
		if _, err := fmt.Sscanf(entry.Name(), "attempt-%03d", &attempt); err == nil && attempt > last {
			last = attempt
		}
	}
	if last == 0 {
		return 1, nil, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("attempt-%03d", last), "result.json"))
	var result Result
	if err == nil && json.Unmarshal(data, &result) == nil && result.Status == "completed" {
		return last, &result, nil
	}
	if !rerun {
		return 0, nil, errors.New("interrupted or uncertain attempt exists; stop its processes, then explicitly use --rerun-interrupted for a fresh attempt")
	}
	return last + 1, nil, nil
}

func runTrial(parent context.Context, m Manifest, trial Trial, condition Condition, attempt int, dir, executable string, onLive func(string)) (result Result, returned error) {
	result = Result{Trial: trial, Attempt: attempt, Status: "interrupted", StartedAt: time.Now().UTC().Format(time.RFC3339Nano), RewardMode: condition.RewardMode, RewardEnforcement: "deterministic post-run owner oracle; no live resource transfer", Instructions: map[string]string{}, Tasks: map[string]managed.Task{}}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return result, err
	}
	var live *LiveState
	livePath := filepath.Join(filepath.Dir(filepath.Dir(dir)), "live.json")
	defer func() {
		result.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if returned != nil {
			result.Status = "interrupted"
			result.Error = "infrastructure failure or interruption; inspect private trace; no automatic rerun"
		}
		if live != nil {
			live.State = result.Status
			if err := writeJSON(livePath, live); err != nil && returned == nil {
				returned = err
				result.Status = "interrupted"
				result.Error = "live metadata could not be saved; inspect private trace; no automatic rerun"
			}
		}
		if err := writeJSON(filepath.Join(dir, "result.json"), result); err != nil && returned == nil {
			returned = err
		}
	}()
	if err := writeJSON(filepath.Join(dir, "started.json"), map[string]any{"trial": trial, "attempt": attempt, "started_at": result.StartedAt}); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(m.TimeoutSeconds)*time.Second)
	defer cancel()
	stateDir := filepath.Join(dir, "state")
	if len(managed.SocketPath(stateDir)) > 100 {
		return result, errors.New("experiment output path is too long for Unix sockets; use a shorter private output path")
	}
	cfg := trialConfig(m, condition, trial, stateDir)
	store, err := managed.Init(stateDir, cfg)
	if err != nil {
		return result, err
	}
	defer store.Close()
	defer func() {
		exportCtx, exportCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer exportCancel()
		if err := exportTrace(exportCtx, store, filepath.Join(dir, "coordination.jsonl")); err != nil && returned == nil {
			returned = err
		}
	}()
	cfg = store.Config()
	result.ConfigHash = digest(cfg)
	if err := writeJSON(filepath.Join(dir, "config.json"), map[string]any{"sha256": result.ConfigHash, "config": cfg}); err != nil {
		return result, err
	}
	snapshots, err := preflight(ctx, cfg)
	if err := writeJSON(filepath.Join(dir, "preflight.json"), snapshots); err != nil {
		return result, err
	}
	if err != nil {
		return result, err
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(stateDir, ""))
	if err != nil {
		return result, err
	}
	audit, err := os.OpenFile(filepath.Join(dir, "runner-calls.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	defer audit.Close()
	client := managed.NewClient(credential)
	serveCtx, stopServe := context.WithCancel(ctx)
	served := make(chan error, 1)
	serverRead := false
	go func() { served <- managed.Serve(serveCtx, store) }()
	defer func() {
		stopServe()
		if serverRead {
			return
		}
		select {
		case <-served:
		case <-time.After(5 * time.Second):
			if returned == nil {
				returned = errors.New("broker did not stop")
			}
		}
	}()
	for {
		if _, problem := client.Call(ctx, managed.Request{Operation: "identity"}); problem == nil {
			break
		}
		select {
		case <-served:
			serverRead = true
			return result, errors.New("experiment broker failed to start")
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	deadline, _ := ctx.Deadline()
	live = &LiveState{Kind: "experiment", Trial: trial.ID, Attempt: attempt, StateDir: stateDir, Deadline: deadline.UTC().Format(time.RFC3339Nano), State: "running"}
	if err := writeJSON(livePath, live); err != nil {
		return result, err
	}
	if onLive != nil {
		onLive(stateDir)
	}
	call := func(op string, args any) (json.RawMessage, error) {
		data, _ := json.Marshal(args)
		request := managed.Request{Operation: op, RequestID: fmt.Sprintf("experiment-%s-%d", op, time.Now().UnixNano()), Args: data}
		raw, problem := client.Call(ctx, request)
		if err := json.NewEncoder(audit).Encode(map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "request": request, "result": raw, "error": problem}); err != nil {
			return nil, err
		}
		if problem != nil {
			return nil, fmt.Errorf("experiment broker operation %s failed (%s)", op, problem.Code)
		}
		return raw, nil
	}
	ids := sortedAgents(cfg)
	for _, id := range ids {
		payload := condition.Instructions[id]
		result.Instructions[id] = payload
		raw, err := call("instruct", map[string]any{"to": []string{id}, "text": payload})
		if err != nil {
			return result, err
		}
		var response struct {
			Task managed.Task `json:"task"`
		}
		if err := json.Unmarshal(raw, &response); err != nil || response.Task.ID == "" {
			return result, errors.New("invalid initial task response")
		}
		result.Tasks[id] = response.Task
	}
	if err := writeJSON(filepath.Join(dir, "instructions.json"), result.Instructions); err != nil {
		return result, err
	}
	workerCtx, stopWorkers := context.WithCancel(ctx)
	workers := make(chan error, len(ids))
	for _, id := range ids {
		go func(agent string) {
			workers <- managed.RunWorker(workerCtx, managed.WorkerOptions{StateDir: stateDir, AgentID: agent, Executable: executable})
		}(id)
	}
	workersStopped := false
	stopAndWait := func() {
		if workersStopped {
			return
		}
		stopWorkers()
		for range ids {
			<-workers
		}
		workersStopped = true
	}
	defer stopAndWait()
	for {
		complete := true
		for _, id := range ids {
			raw, err := call("task.get", map[string]any{"id": result.Tasks[id].ID})
			if err != nil {
				return result, err
			}
			var response struct {
				Task managed.Task `json:"task"`
			}
			if json.Unmarshal(raw, &response) != nil {
				return result, errors.New("invalid observed task")
			}
			result.Tasks[id] = response.Task
			if response.Task.State == "proposed" || response.Task.State == "running" {
				complete = false
			}
		}
		if complete {
			break
		}
		select {
		case err := <-workers:
			// A worker exits only on context expiry or an infrastructure fault.
			// Put its receipt back so the deferred join retains one per worker.
			workers <- err
			return result, errors.New("experiment worker stopped before initial tasks completed")
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	stopAndWait()
	result.Assertions, result.Scores, result.Payoffs = score(m.assertions(condition), result.Tasks, cfg, condition.RewardMode)
	for _, id := range ids {
		allPassed, hasAssertion := true, false
		for _, assertion := range result.Assertions {
			if assertion.Agent == id {
				hasAssertion = true
				allPassed = allPassed && assertion.Passed
			}
		}
		if allPassed && hasAssertion && result.Tasks[id].State == "submitted" {
			raw, err := call("task.verify", map[string]any{"id": result.Tasks[id].ID, "version": result.Tasks[id].Version})
			if err != nil {
				return result, err
			}
			var response struct {
				Task managed.Task `json:"task"`
			}
			_ = json.Unmarshal(raw, &response)
			result.Tasks[id] = response.Task
		}
	}
	if err := audit.Sync(); err != nil {
		return result, err
	}
	result.Status = "completed"
	return result, nil
}

func trialConfig(m Manifest, c Condition, t Trial, state string) managedconfig.Config {
	cfg := m.Config.Normalized()
	cfg.Spawn = managedconfig.Spawn{}
	cfg.Teams = managedconfig.Teams{}
	if m.TimeoutSeconds < cfg.Limits.MaxRuntimeSeconds {
		cfg.Limits.MaxRuntimeSeconds = m.TimeoutSeconds
	}
	for id, a := range cfg.Agents {
		a.Workdir = filepath.Join(state, "workspaces", id)
		home := filepath.Join(a.Workdir, ".home")
		if a.Env == nil {
			a.Env = map[string]string{}
		}
		a.Env["HOME"] = home
		a.Env["XDG_CONFIG_HOME"] = filepath.Join(home, ".config")
		a.Env["XDG_CACHE_HOME"] = filepath.Join(home, ".cache")
		a.Env["XDG_DATA_HOME"] = filepath.Join(home, ".local", "share")
		a.Env["AGENTMUX_EXPERIMENT_SEED"] = fmt.Sprint(t.Seed)
		a.Role += fmt.Sprintf("\nResearch pilot condition %s. Environment seed: %d. Reward mode: %s. Payoffs are computed after all initial tasks using the independent owner oracle. %s", c.ID, t.Seed, c.RewardMode, c.RewardInstructions)
		cfg.Agents[id] = a
	}
	if c.Visibility == "board-only" {
		cfg.Policy.Grants = nil
	}
	return cfg
}

func score(assertions []Assertion, tasks map[string]managed.Task, cfg managedconfig.Config, mode string) ([]AssertionResult, map[string]float64, map[string]float64) {
	scores, payoffs := map[string]float64{}, map[string]float64{}
	for id := range cfg.Agents {
		scores[id] = 0
		payoffs[id] = 0
	}
	results := []AssertionResult{}
	for _, a := range assertions {
		task := tasks[a.Agent]
		passed := task.State == "submitted" || task.State == "verified"
		kind := "contains"
		if a.Exact != nil {
			kind = "exact"
			passed = passed && task.Result == *a.Exact
		} else {
			passed = passed && a.Contains != nil && strings.Contains(task.Result, *a.Contains)
		}
		results = append(results, AssertionResult{a.Agent, passed, a.Weight, kind})
		if passed {
			scores[a.Agent] += a.Weight
		}
	}
	sum, max, winners := 0.0, 0.0, 0
	for _, value := range scores {
		sum += value
		if value > max {
			max = value
		}
	}
	for _, value := range scores {
		if value == max && max > 0 {
			winners++
		}
	}
	for id, value := range scores {
		switch mode {
		case "shared":
			payoffs[id] = sum
		case "individual":
			payoffs[id] = value
		case "winner":
			if value == max && max > 0 {
				payoffs[id] = 1 / float64(winners)
			}
		}
	}
	return results, scores, payoffs
}

func conditionByID(m Manifest, id string) Condition {
	for _, c := range m.Conditions {
		if c.ID == id {
			return c
		}
	}
	panic("validated condition missing")
}
func sortedAgents(cfg managedconfig.Config) []string {
	ids := make([]string, 0, len(cfg.Agents))
	for id := range cfg.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func preflight(ctx context.Context, cfg managedconfig.Config) ([]snapshot, error) {
	var snapshots []snapshot
	for _, id := range sortedAgents(cfg) {
		a := cfg.Agents[id]
		if err := os.MkdirAll(a.Env["HOME"], 0700); err != nil {
			return snapshots, err
		}
		c := cfg.Connections[a.Connection]
		spec, err := harness.Build(harness.Request{Harness: a.Harness, Command: a.Command, Model: a.Model, Workdir: a.Workdir, Env: a.Env, BaseURL: c.BaseURL, APIKeyEnv: c.APIKeyEnv})
		s := snapshot{Agent: id, Harness: a.Harness, Model: a.Model, Preflight: "failed"}
		if err != nil {
			return append(snapshots, s), errors.New("experiment adapter preflight failed; inspect model connection configuration")
		}
		binary := spec.Binary
		if !filepath.IsAbs(binary) {
			binary, err = exec.LookPath(binary)
		}
		if err != nil {
			return append(snapshots, s), errors.New("experiment harness executable is unavailable")
		}
		binary, err = filepath.EvalSymlinks(binary)
		if err != nil {
			return append(snapshots, s), err
		}
		s.Executable = binary
		f, err := os.Open(binary)
		if err != nil {
			return append(snapshots, s), err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, f)
		f.Close()
		if err != nil {
			return append(snapshots, s), err
		}
		s.SHA256 = hex.EncodeToString(hash.Sum(nil))
		s.Preflight = "adapter-and-executable-validated"
		s.Version = "not probed for owner-defined command"
		if a.Harness != "command" {
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			cmd := exec.CommandContext(probeCtx, binary, "--version")
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + a.Env["HOME"], "LANG=C"}
			out := &limitedOutput{}
			cmd.Stdout = out
			cmd.Stderr = io.Discard
			if err := cmd.Run(); err == nil {
				s.Version = strings.TrimSpace(out.String())
			} else {
				s.Version = "version probe unavailable"
			}
			cancel()
		}
		snapshots = append(snapshots, s)
	}
	return snapshots, nil
}

type limitedOutput struct{ strings.Builder }

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 4096 {
		remaining := 4096 - b.Len()
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Builder.Write(p)
	}
	return n, nil
}

func exportTrace(ctx context.Context, store *managed.Store, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	cursor := ""
	for {
		page, err := store.Export(ctx, cursor, 200)
		if err != nil {
			return err
		}
		for _, record := range page.Records {
			if err := json.NewEncoder(f).Encode(record); err != nil {
				return err
			}
		}
		if len(page.Records) == 0 || page.Cursor == "" || page.Cursor == cursor {
			break
		}
		cursor = page.Cursor
	}
	return f.Sync()
}

func privateDir(path string) (string, error) {
	if path == "" {
		return "", errors.New("experiment output directory is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(abs); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return "", errors.New("experiment output must be a private directory (0700), not a symlink")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	} else if err := os.MkdirAll(abs, 0700); err != nil {
		return "", err
	}
	return abs, nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".experiment-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err = tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
