package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"agentmux/internal/research"
)

func runResearch(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runResearchIO(ctx, args, os.Stdout, os.Stderr)
}
func runResearchIO(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := func() {
		fmt.Fprintln(stderr, "Usage: amx research plan <manifest.yaml>\n       amx research run <manifest.yaml> --output <private-dir> [--allow-models] [--rerun-interrupted] [--start-paused]\n       amx research analyze <output-dir> [--csv <new-file>] [--behavior-csv <new-file>]\n       amx research status|watch <output-dir-or-state> [--follow]\n       amx research study <output-dir>\n       amx research history <output-dir> [--actor ID] [--type TYPE] [--query TEXT] [--after CURSOR] [--limit N]\n       amx research pause|resume|step|stop <output-dir-or-state> --version N --reason TEXT")
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		usage()
		return exitSuccess
	}
	if len(args) == 2 && helpArgument(args[1]) {
		switch args[0] {
		case "plan", "run", "analyze", "status", "watch", "study", "history", "pause", "resume", "step", "stop":
			usage()
			return exitSuccess
		}
	}
	if len(args) >= 2 {
		if args[0] == "study" {
			if len(args) != 2 {
				usage()
				return exitValidation
			}
			view, err := research.StudyStatus(ctx, args[1])
			if err != nil {
				fmt.Fprintln(stderr, "research:", err)
				return exitRuntime
			}
			_ = json.NewEncoder(stdout).Encode(map[string]any{"ok": true, "study": view})
			return exitSuccess
		}
		if args[0] == "history" {
			return runResearchHistory(args[1:], stdout, stderr)
		}
		switch args[0] {
		case "status", "watch", "pause", "resume", "step", "stop":
			return runResearchLiveIO(ctx, args, stdout, stderr)
		}
	}
	if len(args) < 2 || (args[0] != "plan" && args[0] != "run" && args[0] != "analyze") {
		usage()
		return exitValidation
	}
	flags := flag.NewFlagSet("research "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "private output directory")
	allow := flags.Bool("allow-models", false, "allow real model/provider calls")
	startPaused := flags.Bool("start-paused", false, "pause each attempt at its first phase barrier; records an exploratory intervention")
	rerun := flags.Bool("rerun-interrupted", false, "create a fresh attempt after stopping interrupted processes")
	csvPath := flags.String("csv", "", "new private descriptive analysis CSV")
	behaviorCSV := flags.String("behavior-csv", "", "new private per-decision behavior CSV")
	if err := flags.Parse(args[2:]); err != nil {
		if err == flag.ErrHelp {
			usage()
			return exitSuccess
		}
		return exitValidation
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "research accepts one manifest/output path followed by flags; run amx research --help")
		return exitValidation
	}
	if args[0] == "plan" && (*output != "" || *allow || *rerun || *startPaused || *csvPath != "" || *behaviorCSV != "") {
		fmt.Fprintln(stderr, "plan accepts no execution/export flags")
		return exitValidation
	}
	var result any
	var err error
	if args[0] == "analyze" {
		if *output != "" || *allow || *rerun || *startPaused {
			return exitValidation
		}
		var report research.Report
		report, err = research.ReadReport(args[1])
		if err == nil {
			analysis := research.Analyze(report)
			result = analysis
			if *csvPath != "" {
				err = writeResearchCSV(*csvPath, analysis)
			}
			if err == nil && *behaviorCSV != "" {
				err = writeResearchBehaviorCSV(*behaviorCSV, research.Behaviors(report))
			}
		}
	} else {
		if *csvPath != "" || *behaviorCSV != "" {
			return exitValidation
		}
		var manifest research.Manifest
		manifest, err = research.Load(args[1])
		if err != nil {
			fmt.Fprintf(stderr, "research: %v\n", err)
			return exitConfigParse
		}
		if args[0] == "plan" {
			result, err = research.Plan(manifest)
		} else {
			if *output == "" {
				usage()
				return exitValidation
			}
			binary, e := os.Executable()
			if e != nil {
				err = e
			} else {
				var report research.Report
				report, err = research.Run(ctx, manifest, research.RunOptions{OutputDir: *output, Executable: binary, AllowModels: *allow, RerunInterrupted: *rerun, StartPaused: *startPaused, OnLive: func(state string) {
					fmt.Fprintf(stderr, "Research live state: %s\nObserve: amx research watch %s --follow\nTerminal: amx dashboard --state %s --view research\nWeb: amx web --state %s\n", state, shellQuote(*output), shellQuote(state), shellQuote(state))
				}})
				result = research.Analyze(report)
			}
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "research: %v\n", err)
		return exitRuntime
	}
	if err := json.NewEncoder(stdout).Encode(map[string]any{"schema_version": 1, "kind": "research", "operation": args[0], "ok": true, "result": result}); err != nil {
		return exitRuntime
	}
	return exitSuccess
}

func runResearchHistory(args []string, out, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("research history", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	actor := flags.String("actor", "", "authenticated actor")
	kind := flags.String("type", "", "record kind or event type")
	query := flags.String("query", "", "search recorded evidence")
	after := flags.String("after", "", "history cursor")
	limit := flags.Int("limit", 100, "page size, maximum200")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return exitValidation
	}
	page, err := research.ReadHistory(args[0], research.HistoryFilter{Actor: *actor, Type: *kind, Query: *query, After: *after, Limit: *limit})
	if err != nil {
		fmt.Fprintln(diagnostics, "research:", err)
		return exitRuntime
	}
	if err := json.NewEncoder(out).Encode(map[string]any{"ok": true, "history": page}); err != nil {
		return exitRuntime
	}
	return exitSuccess
}

func writeResearchBehaviorCSV(path string, rows []research.Behavior) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	w := csv.NewWriter(file)
	if err := w.Write([]string{"behavior_id", "trial", "condition", "trial_status", "round", "actor", "harness", "model", "valid", "execution_failed", "contribution", "invested", "returned", "retained", "utility", "payoff", "remaining_credits", "intervened", "research", "manifest_sha256", "seed", "repetition", "order", "attempt", "config_sha256", "environment", "schema_version", "parent", "project", "productive_project", "invested_to", "received_escrow", "reward_team", "reward_pools", "phase_task_ids", "transfers"}); err != nil {
		return err
	}
	for _, r := range rows {
		row := []string{r.ID, r.Trial, r.Condition, r.TrialStatus, strconv.Itoa(r.Round), r.Actor, r.Harness, r.Model, strconv.FormatBool(r.Valid), strconv.FormatBool(r.ExecutionFailed), strconv.Itoa(r.Contribution), strconv.Itoa(r.Invested), strconv.Itoa(r.Returned), strconv.Itoa(r.Retained), strconv.FormatFloat(r.Utility, 'g', -1, 64), strconv.FormatFloat(r.Payoff, 'g', -1, 64), strconv.Itoa(r.RemainingCredits), strconv.FormatBool(r.Intervened), r.Research, r.ManifestHash, strconv.FormatInt(r.Seed, 10), strconv.Itoa(r.Repetition), strconv.Itoa(r.Order), strconv.Itoa(r.Attempt), r.ConfigHash, r.Environment}
		if r.SchemaVersion > 0 {
			project, to := "", ""
			if r.Project != nil {
				project = strconv.Itoa(*r.Project)
			}
			if r.InvestedTo != nil {
				to = *r.InvestedTo
			}
			pools, _ := json.Marshal(r.RewardPools)
			tasks, _ := json.Marshal(r.PhaseTaskIDs)
			transfers, _ := json.Marshal(r.Transfers)
			row = append(row, strconv.Itoa(r.SchemaVersion), r.Parent, project, strconv.Itoa(r.ProductiveProject), to, strconv.Itoa(r.ReceivedEscrow), r.RewardTeam, string(pools), string(tasks), string(transfers))
		} else {
			row = append(row, make([]string, 10)...)
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return file.Sync()
}
func writeResearchCSV(path string, a research.Analysis) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	w := csv.NewWriter(file)
	if err := w.Write([]string{"condition", "completed_runs", "mean_total_social_welfare", "mean_funding_rate", "mean_credits_spent", "mean_invalid_actions", "mean_consented_teams", "research", "manifest_sha256", "intervened_runs", "interrupted_runs", "interpretation", "power_warning", "analysis_json", "primary_outcome", "mean_primary_outcome", "n_primary_outcome"}); err != nil {
		return err
	}
	// Keep all descriptive estimates, warnings and preregistration portable,
	// including when no condition has an estimate. Analysis excludes prompts.
	metadata, err := json.Marshal(a)
	if err != nil {
		return err
	}
	provenance := []string{a.Research, a.ManifestHash, strconv.Itoa(a.IntervenedRuns), strconv.Itoa(a.InterruptedRuns), a.Interpretation, a.PowerWarning, string(metadata)}
	if len(a.Conditions) == 0 {
		row := append(make([]string, 7), provenance...)
		row = append(row, a.PrimaryOutcome, "", "")
		if err := w.Write(row); err != nil {
			return err
		}
	}
	for _, c := range a.Conditions {
		row := []string{c.Condition, strconv.Itoa(c.CompletedRuns), strconv.FormatFloat(c.SocialWelfare.Mean, 'g', -1, 64), strconv.FormatFloat(c.FundingRate.Mean, 'g', -1, 64), strconv.FormatFloat(c.Cost.Mean, 'g', -1, 64), strconv.FormatFloat(c.InvalidActions.Mean, 'g', -1, 64), strconv.FormatFloat(c.ConsentedTeams.Mean, 'g', -1, 64)}
		row = append(row, provenance...)
		primaryMean := ""
		if c.PrimaryEstimate.N > 0 {
			primaryMean = strconv.FormatFloat(c.PrimaryEstimate.Mean, 'g', -1, 64)
		}
		row = append(row, a.PrimaryOutcome, primaryMean, strconv.Itoa(c.PrimaryEstimate.N))
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return file.Sync()
}

func runResearchLiveIO(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("research "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	version := flags.Int("version", 0, "expected research control version from status")
	reason := flags.String("reason", "", "auditable reason for this exploratory intervention")
	follow := flags.Bool("follow", false, "follow the whole study across trial transitions (watch only)")
	if err := flags.Parse(args[2:]); err != nil {
		if err == flag.ErrHelp {
			return exitSuccess
		}
		return exitValidation
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "research "+args[0]+" accepts one output/state path followed by flags")
		return exitValidation
	}
	mutating := args[0] != "status" && args[0] != "watch"
	if *follow && args[0] != "watch" {
		fmt.Fprintln(stderr, "--follow applies only to research watch")
		return exitValidation
	}
	if (mutating && (*version < 1 || *reason == "")) || (!mutating && (*version != 0 || *reason != "")) {
		fmt.Fprintln(stderr, "research interventions require --version N --reason TEXT; status/watch accept no mutation flags")
		return exitValidation
	}
	previous := ""
	for {
		if *follow {
			callCtx, stop := context.WithTimeout(ctx, 3*time.Second)
			study, err := research.StudyStatus(callCtx, args[1])
			stop()
			if err != nil {
				fmt.Fprintln(stderr, "research:", err)
				return exitRuntime
			}
			encoded, _ := json.Marshal(study)
			if string(encoded) != previous {
				if json.NewEncoder(stdout).Encode(map[string]any{"schema_version": 1, "kind": "research", "operation": "watch", "ok": true, "study": study}) != nil {
					return exitRuntime
				}
				previous = string(encoded)
			}
			if study.State == "completed" {
				return exitSuccess
			}
			select {
			case <-ctx.Done():
				return exitSuccess
			case <-time.After(time.Second):
				continue
			}
		}
		callCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		var view research.LiveView
		var err error
		if mutating {
			view, err = research.ControlLive(callCtx, args[1], args[0], *reason, *version)
		} else {
			view, err = research.LiveStatus(callCtx, args[1])
		}
		stop()
		if err != nil {
			fmt.Fprintf(stderr, "research: %v\n", err)
			return exitRuntime
		}
		encoded, _ := json.Marshal(view)
		if string(encoded) != previous {
			if err := json.NewEncoder(stdout).Encode(map[string]any{"schema_version": 1, "kind": "research", "operation": args[0], "ok": true, "result": view}); err != nil {
				return exitRuntime
			}
			previous = string(encoded)
		}
		if args[0] != "watch" {
			return exitSuccess
		}
		if !view.Live || view.Research == nil {
			// A study watch can explicitly continue across finite attempts.
			return exitSuccess
		}
		switch view.Research.Progress.State {
		case "completed", "interrupted", "stopped":
			return exitSuccess
		}
		select {
		case <-ctx.Done():
			return exitSuccess
		case <-time.After(time.Second):
		}
	}
}
