package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func privateJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("research evidence must be a private regular file without symlinks")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func checkStoredManifest(dir, expected string) error {
	var stored struct {
		SHA256   string          `json:"sha256"`
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := privateJSON(filepath.Join(dir, "manifest.json"), &stored); err != nil {
		return err
	}
	actual, err := compactDigest(stored.Manifest)
	if err != nil || actual != expected || stored.SHA256 != expected {
		return errors.New("saved research manifest contents do not match the frozen hash")
	}
	return nil
}

func checkStoredPlan(dir string, expected PlanResult) error {
	var actual PlanResult
	if err := privateJSON(filepath.Join(dir, "plan.json"), &actual); err != nil || !reflect.DeepEqual(actual, expected) {
		return errors.New("saved research plan differs from the frozen manifest")
	}
	return nil
}

func attemptNumbers(dir string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	numbers := []int{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "attempt-") {
			continue
		}
		number, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), "attempt-"))
		if err != nil || number < 1 || number > 100000 || entry.Name() != fmt.Sprintf("attempt-%03d", number) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("malformed or unsafe research attempt directory")
		}
		if _, err := privateDir(filepath.Join(dir, entry.Name())); err != nil {
			return nil, err
		}
		numbers = append(numbers, number)
	}
	return numbers, nil
}

type plannedAttempt struct {
	number int
	prior  *Result
}

func inspectAttempts(m Manifest, plan PlanResult, dir string, rerun bool, budget *modelBudget, strict bool) (map[string]plannedAttempt, bool, error) {
	results := map[string]plannedAttempt{}
	pending := false
	seenTasks := map[string]bool{}
	for _, trial := range plan.Trials {
		runDir, err := privateDir(filepath.Join(dir, trial.ID))
		if err != nil {
			return nil, false, err
		}
		number, prior, err := resume(runDir, rerun)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", trial.ID, err)
		}
		if prior != nil {
			if err := checkCachedResult(m, trial, number, filepath.Join(runDir, fmt.Sprintf("attempt-%03d", number)), *prior, budget, seenTasks, strict); err != nil {
				return nil, false, fmt.Errorf("%s: %w", trial.ID, err)
			}
		} else {
			pending = true
		}
		results[trial.ID] = plannedAttempt{number, prior}
	}
	return results, pending, nil
}

func checkCachedResult(m Manifest, trial Trial, attempt int, dir string, result Result, budget *modelBudget, seen map[string]bool, strict bool) error {
	if result.Status != "completed" || result.Trial != trial || result.Attempt != attempt || result.ConfigHash == "" || len(result.Rounds) != m.Game.Rounds || len(result.Phases) != 2*m.Game.Rounds {
		return errors.New("completed research result is incomplete or differs from its plan/attempt")
	}
	if strict && result.Protocol != decisionProtocol || !strict && result.Protocol != "" {
		return errors.New("research result protocol differs from collection execution evidence")
	}
	var saved struct {
		SHA256 string          `json:"sha256"`
		Config json.RawMessage `json:"config"`
	}
	if err := privateJSON(filepath.Join(dir, "config.json"), &saved); err != nil {
		return errors.New("completed research configuration evidence is unavailable")
	}
	hash, err := compactDigest(saved.Config)
	if err != nil || hash != saved.SHA256 || hash != result.ConfigHash {
		return errors.New("completed research configuration evidence differs from its frozen hash")
	}
	var cfg managedconfig.Config
	if json.Unmarshal(saved.Config, &cfg) != nil {
		return errors.New("invalid completed research configuration")
	}
	if strict && digest(cfg) != digest(trialConfig(m, condition(m, trial.Condition), filepath.Join(dir, "state")).Normalized()) {
		return errors.New("completed research configuration differs from its planned trial")
	}
	definitions := cfg.Agents
	parents := map[string]string{}
	if m.Population.Mode == "dynamic" {
		store, err := managed.Open(filepath.Join(dir, "state"))
		if err != nil {
			return errors.New("completed dynamic research registry is unavailable")
		}
		cursor := ""
		for {
			page, err := store.Export(context.Background(), cursor, 200)
			if err != nil {
				store.Close()
				return err
			}
			for _, record := range page.Records {
				if record.Kind != "agent" {
					continue
				}
				var info managed.AgentInfo
				if json.Unmarshal(record.Data, &info) != nil {
					store.Close()
					return errors.New("invalid cached research actor registry")
				}
				if _, initial := cfg.Agents[info.ID]; initial {
					continue
				}
				template, ok := cfg.Spawn.Templates[info.Template]
				if !ok || info.Config.Harness != template.Harness || info.Config.Model != template.Model || info.Config.Trace != template.Trace || info.Parent == "" {
					store.Close()
					return errors.New("cached child actor differs from its approved template")
				}
				definitions[info.ID] = info.Config
				parents[info.ID] = info.Parent
			}
			if len(page.Records) == 0 {
				break
			}
			if page.Cursor == "" || page.Cursor == cursor {
				store.Close()
				return errors.New("cached registry export did not advance")
			}
			cursor = page.Cursor
		}
		if err := store.Close(); err != nil {
			return err
		}
	}
	reserved := map[string]bool{}
	for _, id := range budget.Reserved {
		reserved[id] = true
	}
	for index, round := range result.Rounds {
		if round.Round != index+1 {
			return errors.New("completed research rounds are missing or reordered")
		}
		first, second := "coordinate", "act"
		if m.Game.Kind == ExchangeVersion {
			first, second = "invest", "return"
		}
		actors := actors(m.Config)
		if m.Population.Mode == "dynamic" {
			if len(result.Population) != m.Game.Rounds || result.Population[index].Round != round.Round {
				return errors.New("completed dynamic research population evidence is incomplete")
			}
			actors = result.Population[index].Actors
		}
		for offset, kind := range []string{first, second} {
			phase := result.Phases[2*index+offset]
			if phase.Round != round.Round || phase.Kind != kind || len(phase.Tasks) != len(actors) || len(phase.Instructions) != len(actors) {
				return errors.New("completed research phase evidence is incomplete")
			}
			for _, actor := range actors {
				task, ok := phase.Tasks[actor]
				if !ok || task.ID == "" || seen[task.ID] || task.From != "$owner" || task.To != actor || task.Text != phase.Instructions[actor] || task.Text == "" || !terminalResearchTask(task) {
					return errors.New("completed research task provenance is inconsistent")
				}
				seen[task.ID] = true
				label, known := result.ActorMetadata[actor]
				definition, configured := definitions[actor]
				if !known || !configured || label.Harness != definition.Harness || label.Model != definition.Model || label.Parent != parents[actor] {
					return errors.New("completed research actor metadata is missing")
				}
				if definition.Harness != "command" && !reserved[task.ID] {
					return errors.New("completed native task is absent from the durable budget ledger")
				}
				if strict && definition.Harness != "command" {
					if !phase.TraceVerified[actor] {
						return errors.New("completed native task lacks recorded trace verification")
					}
					if err := verifyNativeTaskTrace(filepath.Join(dir, "state"), actor, task, definition); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func terminalResearchTask(task managed.Task) bool {
	switch task.State {
	case "submitted", "verified", "failed":
		return task.Delivery != "dispatching" && task.Delivery != "uncertain" && task.Delivery != "cancel_requested" && task.Delivery != "cancel_uncertain"
	case "cancelled":
		return task.Delivery == "stopped"
	}
	return false
}

// A previous attempt must not have a live broker/worker when an explicit fresh
// attempt is requested. Holding the study lock does not cover manually started
// processes using an older state directory.
func checkAttemptStopped(dir string) error {
	state := filepath.Join(dir, "state")
	if _, err := os.Lstat(state); os.IsNotExist(err) {
		return nil
	}
	if _, err := privateDir(state); err != nil {
		return errors.New("previous research state cannot be safely inspected")
	}
	paths, err := filepath.Glob(filepath.Join(state, "worker-*.lock"))
	if err != nil {
		return err
	}
	paths = append(paths, filepath.Join(state, "broker.lock"))
	for _, path := range paths {
		unlock, err := lockOutput(path)
		if err != nil {
			return errors.New("previous research broker/worker is still running or its lock is unavailable; stop it before a fresh attempt")
		}
		unlock()
	}
	return nil
}

// Broker records cover a crash between durable model reservation and the phase
// JSON checkpoint. A shortened ledger must not forget already claimed tasks.
func checkBudgetHistory(ctx context.Context, dir string, budget *modelBudget) error {
	if budget.Limit == 0 {
		return nil
	}
	reserved := map[string]bool{}
	for _, id := range budget.Reserved {
		reserved[id] = true
	}
	states, err := filepath.Glob(filepath.Join(dir, "run-*", "attempt-*", "state"))
	if err != nil {
		return err
	}
	for _, state := range states {
		if _, err := os.Lstat(filepath.Join(state, "state.sqlite")); os.IsNotExist(err) {
			continue // The process can stop before managed.Init creates the DB.
		}
		store, err := managed.Open(state)
		if err != nil {
			return errors.New("historical native budget records cannot be inspected; no new turns may start")
		}
		definitions := store.Config().Agents
		tasks := []managed.Task{}
		cursor := ""
		for {
			page, err := store.Export(ctx, cursor, 200)
			if err != nil {
				store.Close()
				return err
			}
			for _, record := range page.Records {
				switch record.Kind {
				case "agent":
					var agent managed.AgentInfo
					if json.Unmarshal(record.Data, &agent) != nil {
						store.Close()
						return errors.New("invalid historical research actor record")
					}
					definitions[agent.ID] = agent.Config
				case "task":
					var task managed.Task
					if json.Unmarshal(record.Data, &task) != nil {
						store.Close()
						return errors.New("invalid historical research task record")
					}
					tasks = append(tasks, task)
				}
			}
			if len(page.Records) == 0 {
				break
			}
			if page.Cursor == "" || page.Cursor == cursor {
				store.Close()
				return errors.New("historical research export did not advance")
			}
			cursor = page.Cursor
		}
		if err := store.Close(); err != nil {
			return err
		}
		for _, task := range tasks {
			agent, known := definitions[task.To]
			if !known {
				return errors.New("historical research task has an unknown recipient")
			}
			if agent.Harness != "command" && task.Version > 1 && !reserved[task.ID] {
				return errors.New("claimed historical native task is absent from durable budget ledger; no new turns may start")
			}
		}
	}
	return nil
}
