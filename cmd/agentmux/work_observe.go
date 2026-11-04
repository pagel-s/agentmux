package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
)

// Observation is an owner read, never a health assertion about a model. The
// broker transaction and tmux inventory are independently timed observations.
type workObservation struct {
	SchemaVersion   int                          `json:"schema_version"`
	ObserverVersion string                       `json:"observer_version"`
	ObservedAt      string                       `json:"observed_at"`
	Workspace       string                       `json:"workspace"`
	State           string                       `json:"state"`
	Source          string                       `json:"source"`
	Complete        bool                         `json:"complete"`
	Broker          string                       `json:"broker"`
	BrokerError     string                       `json:"broker_error,omitempty"`
	Snapshot        *managed.ObservationSnapshot `json:"snapshot,omitempty"`
	Runtime         managedlaunch.Runtime        `json:"runtime"`
	RuntimeError    string                       `json:"runtime_error,omitempty"`
	Native          []workNativeObservation      `json:"native"`
	NativeMore      bool                         `json:"native_more"`
	NativeError     string                       `json:"native_error,omitempty"`
	Storage         workStorageObservation       `json:"storage"`
	Diagnostics     workObservationDiagnostics   `json:"diagnostics"`
}

type workStorageObservation struct {
	ObservedAt string                      `json:"observed_at"`
	Files      map[string]workObservedFile `json:"files"`
}

type workObservedFile struct {
	State   string `json:"state"`
	Bytes   *int64 `json:"bytes"`
	Problem string `json:"problem,omitempty"`
}

// Stat only known database files, outside the broker lock. Never follow links,
// read content or recursively walk traces/workspaces to build a status view.
func observeWorkStorage(state string) workStorageObservation {
	result := workStorageObservation{Files: map[string]workObservedFile{}}
	for label, filename := range map[string]string{"database": "state.sqlite", "wal": "state.sqlite-wal", "shared_memory": "state.sqlite-shm"} {
		var file workObservedFile
		info, err := os.Lstat(filepath.Join(state, filename))
		switch {
		case os.IsNotExist(err):
			zero := int64(0)
			file.State, file.Bytes = "absent", &zero
		case err != nil:
			file.State, file.Problem = "unknown", "file metadata unavailable"
		case !info.Mode().IsRegular():
			file.State, file.Problem = "unknown", "expected a regular file; links are not followed"
		default:
			size := info.Size()
			file.State, file.Bytes = "present", &size
		}
		result.Files[label] = file
	}
	result.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return result
}

type workNativeObservation struct {
	Agent               string                 `json:"agent"`
	State               string                 `json:"state"`
	Adapter             string                 `json:"adapter,omitempty"`
	Connected           bool                   `json:"connected"`
	ReadinessObservedAt string                 `json:"readiness_observed_at,omitempty"`
	PendingCount        *int                   `json:"pending_count"`
	Coverage            string                 `json:"coverage"`
	Active              *managed.NativeReceipt `json:"active,omitempty"`
}

func runWorkObserve(args []string, out, diagnostics io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	return runWorkObserveContext(ctx, args, out, diagnostics)
}

func runWorkObserveContext(ctx context.Context, args []string, out, diagnostics io.Writer) int {
	if workHelpRequested(args) {
		fmt.Fprintln(out, `Usage: amx observe [--session NAME | --state DIR] [--json]
                   [--limit 1..200] [--watch [--interval 1s]]

Read current broker, worker and native reporting evidence in one place.
Counts cover retained task history; attention items are bounded (default: 25).
Recorded delivery and effective uncertainty remain separate. No recent event
does not establish that a model is stalled. Task bodies and credentials are omitted.
--watch --json emits one JSON object per observation. Partial observations
contain explicit component errors; a one-shot partial observation exits nonzero.
Read-only: never starts, retries, resolves or changes a workspace.
History guide: https://pagel-s.github.io/agentmux/observability/#summarize-a-private-capture-offline
The offline Python reporter ships in the matching release archive's scripts/ folder;
the binary installer installs amx only.`)
		return exitSuccess
	}
	flags := flag.NewFlagSet("observe", flag.ContinueOnError)
	flags.SetOutput(workflowDiagnosticWriter{diagnostics})
	session := flags.String("session", "work", "workspace name")
	state := flags.String("state", "", "exact workspace state directory")
	asJSON := flags.Bool("json", false, "emit JSON (JSONL when watching)")
	watch := flags.Bool("watch", false, "repeat until interrupted")
	interval := flags.Duration("interval", time.Second, "delay between observations")
	limit := flags.Int("limit", 25, "maximum attention items")
	positionals, err := parseGuidedFlags(flags, args)
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", consoleContent(err.Error(), 1000))
		return exitValidation
	}
	var selectors []string
	intervalSpecified := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "session":
			selectors = append(selectors, "--session", *session)
		case "state":
			selectors = append(selectors, "--state", *state)
		case "interval":
			intervalSpecified = true
		}
	})
	if len(positionals) != 0 || *limit < 1 || *limit > 200 || *interval < 250*time.Millisecond || *interval > time.Minute || (intervalSpecified && !*watch) {
		fmt.Fprintln(diagnostics, "error: observe accepts workspace selectors, --json, --limit 1..200, and --watch with --interval 250ms..1m")
		return exitValidation
	}
	o, err := parseWorkOptions(selectors)
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", consoleContent(err.Error(), 1000))
		return exitValidation
	}
	for {
		result := collectWorkObservation(ctx, o, *limit)
		if *asJSON {
			err = json.NewEncoder(out).Encode(result)
		} else {
			err = formatWorkObservation(out, result)
		}
		if err != nil {
			return exitRuntime
		}
		if !*watch {
			if !result.Complete {
				return exitRuntime
			}
			return exitSuccess
		}
		timer := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return exitSuccess
		case <-timer.C:
		}
	}
}

func collectWorkObservation(ctx context.Context, o workOptions, limit int) workObservation {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	result := workObservation{SchemaVersion: 1, ObserverVersion: versionString(), Workspace: o.session, State: o.state, Source: o.selectionSource, Broker: "unavailable", Native: []workNativeObservation{}, Diagnostics: newWorkObservationDiagnostics()}
	// Each goroutine owns separate result fields; all reads wait for completion.
	var pending sync.WaitGroup
	pending.Add(3)
	go func() {
		defer pending.Done()
		defer beginWorkObservationComponent(&result.Diagnostics.Components.Storage)()
		result.Storage = observeWorkStorage(o.state)
		for _, file := range result.Storage.Files {
			if file.State == "unknown" {
				result.Diagnostics.Components.Storage.fail("metadata_unavailable")
			}
		}
	}()
	go func() {
		defer pending.Done()
		defer beginWorkObservationComponent(&result.Diagnostics.Components.Runtime)()
		runtime, errRuntime := describeWorkRuntime(ctx, o.state)
		result.Runtime = runtime
		if errRuntime != nil {
			if os.IsNotExist(errRuntime) {
				result.Runtime.State = "not_recorded"
				result.Runtime.Agents = map[string]managedlaunch.AgentPane{}
				result.Diagnostics.Components.Runtime.State = "not_recorded"
				return
			}
			result.RuntimeError = errRuntime.Error()
			result.Runtime.State = "unknown"
			result.Diagnostics.Components.Runtime.fail(workObservationErrorCode(ctx, errRuntime))
		}
		for id, pane := range result.Runtime.Agents {
			if errRuntime != nil || (pane.State == "running" && result.Runtime.State != "running" && result.Runtime.State != "starting") {
				pane.State = "unknown"
				result.Runtime.Agents[id] = pane
			}
		}
	}()
	go func() {
		defer pending.Done()
		client := collectWorkObservationBroker(ctx, &result, limit)
		if client == nil {
			return
		}
		defer beginWorkObservationComponent(&result.Diagnostics.Components.Native)()
		raw, err, code := workObservationCall(ctx, client, "native.status", map[string]any{"limit": 200, "summary": true})
		if err != nil {
			result.NativeError = err.Error()
			result.Diagnostics.Components.Native.failWithError(code, err)
			return
		}
		var native struct {
			Statuses []managed.NativeStatus `json:"statuses"`
			HasMore  bool                   `json:"has_more"`
		}
		if json.Unmarshal(raw, &native) != nil || native.Statuses == nil {
			result.NativeError = "invalid native reporting inventory"
			result.Diagnostics.Components.Native.fail("invalid_response")
			return
		}
		// native.status includes queued content for individual agents; its owner
		// inventory deliberately omits it. Keep that invariant in this facade.
		for _, status := range native.Statuses {
			item := workNativeObservation{Agent: status.Agent, State: status.State, Adapter: status.Adapter, Connected: status.Connected, ReadinessObservedAt: status.ReadinessObservedAt, Coverage: status.Coverage}
			if status.PendingKnown {
				count := status.PendingCount
				item.PendingCount = &count
			}
			if receipt := status.Active; receipt != nil {
				item.Active = &managed.NativeReceipt{ID: receipt.ID, Adapter: receipt.Adapter, Phase: receipt.Phase, UpdatedAt: receipt.UpdatedAt, Evidence: receipt.Evidence}
			}
			result.Native = append(result.Native, item)
		}
		result.NativeMore = native.HasMore
	}()
	pending.Wait()
	result.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if result.Runtime.Session != "" {
		result.Workspace = result.Runtime.Session
	}
	result.Complete = result.Snapshot != nil && result.BrokerError == "" && result.RuntimeError == "" && result.NativeError == ""
	for _, file := range result.Storage.Files {
		if file.State == "unknown" {
			result.Complete = false
		}
	}
	result.Diagnostics.CollectionDurationMS = float64(time.Since(started)) / float64(time.Millisecond)
	return result
}

func collectWorkObservationBroker(ctx context.Context, result *workObservation, limit int) *managed.Client {
	defer beginWorkObservationComponent(&result.Diagnostics.Components.Broker)()
	credential, err := managed.LoadCredential(managed.CredentialPath(result.State, ""))
	if err != nil {
		result.BrokerError = "owner credential unavailable; check amx context with the same selector"
		result.Diagnostics.Components.Broker.fail("credential_unavailable")
		return nil
	}
	client := managed.NewClient(credential)
	raw, err, code := workObservationCall(ctx, client, "observation.get", map[string]any{"limit": limit})
	if err != nil {
		result.BrokerError = err.Error()
		result.Diagnostics.Components.Broker.failWithError(code, err)
		var problem *managed.Fault
		if errors.As(err, &problem) && problem.Code == "unsupported" {
			result.BrokerError += "; workspace controller lacks observation.get; upgrade its controller explicitly after inspecting current work"
		}
		return nil
	}
	var snapshot managed.ObservationSnapshot
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		result.BrokerError = "invalid broker observation"
		result.Diagnostics.Components.Broker.fail("invalid_response")
		return nil
	}
	if _, err = time.Parse(time.RFC3339Nano, snapshot.ObservedAt); err != nil {
		result.BrokerError = "broker observation has no valid observation time; update the workspace controller"
		result.Diagnostics.Components.Broker.fail("invalid_response")
		return nil
	}
	result.Snapshot, result.Broker = &snapshot, "connected"
	return client
}

func formatWorkObservation(out io.Writer, r workObservation) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Observer: %s\n", consoleContent(r.ObserverVersion, 256))
	fmt.Fprintf(&b, "Observed: %s\nWorkspace: %s\nState: %s\nSource: %s\nBroker: %s\nWorkers: %s\n", r.ObservedAt, consoleContent(r.Workspace, 128), consoleContent(r.State, 4000), r.Source, r.Broker, consoleContent(r.Runtime.State, 100))
	for _, item := range []struct{ name, problem string }{{"Broker", r.BrokerError}, {"Workers", r.RuntimeError}, {"Native reporting", r.NativeError}} {
		if item.problem != "" {
			fmt.Fprintf(&b, "%s unavailable: %s\n", item.name, consoleContent(item.problem, 1000))
		}
	}
	if r.Runtime.AttemptID != "" {
		fmt.Fprintf(&b, "Attempt: %s\n", consoleContent(r.Runtime.AttemptID, 256))
	}
	if r.Runtime.Controller != "" {
		fmt.Fprintf(&b, "Controller: %s\n", consoleContent(r.Runtime.Controller, 4000))
	}
	if r.Runtime.Problem != "" {
		fmt.Fprintf(&b, "Runtime attention: %s\n", consoleContent(r.Runtime.Problem, 1000))
	}
	b.WriteString("Storage files:")
	for _, name := range []string{"database", "wal", "shared_memory"} {
		file, ok := r.Storage.Files[name]
		if !ok || file.Bytes == nil {
			fmt.Fprintf(&b, " %s=unknown", name)
		} else {
			fmt.Fprintf(&b, " %s=%dB", name, *file.Bytes)
		}
	}
	b.WriteString(" (file sizes; traces/workspaces not measured)\n")
	ids := make([]string, 0, len(r.Runtime.Agents))
	for id := range r.Runtime.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	workerStates := map[string]int{}
	for _, id := range ids {
		workerStates[r.Runtime.Agents[id].State]++
	}
	if len(ids) > 0 {
		counts, _ := json.Marshal(workerStates)
		fmt.Fprintf(&b, "Worker panes: %s\n", counts)
	}
	for _, id := range ids {
		if r.Runtime.Agents[id].State != "running" {
			fmt.Fprintf(&b, "  %s: worker %s\n", consoleContent(id, 128), consoleContent(r.Runtime.Agents[id].State, 80))
		}
	}
	if s := r.Snapshot; s != nil {
		c := s.Capacity
		fmt.Fprintf(&b, "\nRetained capacity: records %d/%d · requests %d/%d · messages %d/%d · tasks %d/%d\n", c.Records, c.MaxRecords, c.Requests, c.MaxRequests, c.Messages, c.MaxMessages, c.Tasks, c.MaxTasks)
		fmt.Fprintf(&b, "\nTasks: %d retained · %d live execution leases · %d recovery eligible · %d attention\n", s.Tasks, s.ActiveCount, s.RecoveryEligible, s.AttentionCount)
		for _, counts := range []struct {
			name   string
			values map[string]int
		}{{"Task states", s.TaskStates}, {"Recorded delivery", s.Deliveries}, {"Effective delivery", s.EffectiveDeliveries}} {
			data, _ := json.Marshal(counts.values)
			fmt.Fprintf(&b, "%s: %s\n", counts.name, data)
		}
		fmt.Fprintf(&b, "Broker observation: %s\n", consoleContent(s.ObservedAt, 100))
		if s.LastEventAt != "" {
			fmt.Fprintf(&b, "Last lifecycle event: %s\n", consoleContent(s.LastEventAt, 100))
		}
		if s.RuntimeDeadline != "" {
			fmt.Fprintf(&b, "Run deadline: %s\n", consoleContent(s.RuntimeDeadline, 100))
		}
		printObservationTasks(&b, r.State, "Attention", s.Attention)
		if s.AttentionMore {
			b.WriteString("\nAttention list is truncated; use amx tasks with the same workspace selector for complete history.\n")
		}
		printObservationTasks(&b, r.State, "Live execution leases", s.Active)
		if s.ActiveMore {
			b.WriteString("Live execution lease list is truncated; use amx tasks --status running with the same workspace selector.\n")
		}
	}
	if len(r.Native) > 0 {
		reporting, absent, unknown := 0, 0, 0
		for _, status := range r.Native {
			switch {
			case status.ReadinessObservedAt == "":
				unknown++
			case status.Connected:
				reporting++
			default:
				absent++
			}
		}
		fmt.Fprintf(&b, "\nNative reporting: %d connected · %d not reporting · %d unknown\n", reporting, absent, unknown)
	}
	for _, status := range r.Native {
		if status.Active == nil && (status.PendingCount == nil || *status.PendingCount == 0) && (status.Connected || status.Adapter == "") {
			continue
		}
		fmt.Fprintf(&b, "\nNative %s: %s\n", consoleContent(status.Agent, 128), consoleContent(status.State, 80))
		b.WriteString("  " + nativeObservationSummary(managed.NativeStatus{ReadinessObservedAt: status.ReadinessObservedAt, Connected: status.Connected}) + "\n")
		if status.Active != nil {
			fmt.Fprintf(&b, "  Last receipt: %s · %s · %s\n", consoleContent(status.Active.ID, 256), consoleContent(status.Active.Phase, 80), consoleContent(status.Active.UpdatedAt, 100))
		}
	}
	if r.NativeMore {
		b.WriteString("Native inventory truncated at 200 agents; inspect further pages with managed call native.status.\n")
	}
	printWorkTaskCommand(&b, "Session inventory", []string{"amx", "sessions", "--state", r.State})
	b.WriteString("\nObservations describe reporting and transport, not model reasoning or task correctness.\n")
	_, err := io.WriteString(out, b.String())
	return err
}

func printObservationTasks(b *strings.Builder, state, label string, tasks []managed.ObservationTask) {
	if len(tasks) > 0 {
		fmt.Fprintln(b, "\n"+label+":")
	}
	for _, task := range tasks {
		fmt.Fprintf(b, "\n%s · to %s · v%d · %s / %s", consoleContent(task.ID, 256), consoleContent(task.To, 128), task.Version, consoleContent(task.State, 80), consoleContent(task.Delivery, 80))
		if task.EffectiveDelivery != task.Delivery {
			fmt.Fprintf(b, " (effective %s)", consoleContent(task.EffectiveDelivery, 80))
		}
		fmt.Fprintf(b, "\n  Updated: %s\n  %s\n", consoleContent(task.UpdatedAt, 100), consoleContent(task.Reason, 1000))
		if task.NativeReceipt != nil {
			fmt.Fprintf(b, "  Native receipt: %s · %s · %s\n", consoleContent(task.NativeReceipt.ID, 256), consoleContent(task.NativeReceipt.Phase, 80), consoleContent(task.NativeReceipt.UpdatedAt, 100))
		}
		if task.Progress != nil || task.Kind == "active" {
			fmt.Fprintln(b, "  "+taskProgressSummary(task.Progress))
		}
		printWorkTaskCommand(b, "Inspect", []string{"amx", "task", task.ID, "--state", state})
	}
}

func taskProgressSummary(p *managed.TaskProgress) string {
	if p == nil {
		return "Worker reporting: unknown; no check-in for this attempt (observations reset on broker restart)."
	}
	text := "Worker check-in: " + consoleContent(p.WorkerObservedAt, 100)
	if p.EventCount == 0 {
		return text + " · no native events observed."
	}
	return fmt.Sprintf("%s · %d native events · last %s at %s. Events do not establish task correctness.", text, p.EventCount, consoleContent(p.EventKind, 80), consoleContent(p.EventObservedAt, 100))
}

func nativeObservationSummary(s managed.NativeStatus) string {
	at, err := time.Parse(time.RFC3339Nano, s.ReadinessObservedAt)
	if err != nil || at.After(time.Now().Add(time.Second)) {
		return "Adapter reporting: unknown; no readiness observation recorded."
	}
	if !s.Connected {
		return "Adapter not reporting; last readiness " + consoleContent(s.ReadinessObservedAt, 100) + ". Retained delivery outcome is not confirmed by this observation."
	}
	return "Adapter reporting; readiness observed " + consoleContent(s.ReadinessObservedAt, 100) + "."
}
