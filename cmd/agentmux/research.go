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
		fmt.Fprintln(stderr, "Usage: agentmux research plan <manifest.yaml>\n       agentmux research run <manifest.yaml> --output <private-dir> [--allow-models] [--rerun-interrupted] [--start-paused]\n       agentmux research analyze <output-dir> [--csv <new-file>] [--behavior-csv <new-file>]\n       agentmux research status|watch <output-dir-or-state>\n       agentmux research pause|resume|step|stop <output-dir-or-state> --version N --reason TEXT")
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		usage()
		return exitSuccess
	}
	if len(args) == 2 && helpArgument(args[1]) {
		switch args[0] {
		case "plan", "run", "analyze", "status", "watch", "pause", "resume", "step", "stop":
			usage()
			return exitSuccess
		}
	}
	if len(args) >= 2 {
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
		fmt.Fprintln(stderr, "research accepts one manifest/output path followed by flags; run agentmux research --help")
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
					fmt.Fprintf(stderr, "Research live state: %s\nObserve: agentmux research watch %s\nTerminal: agentmux dashboard --state %s --view research\nWeb: agentmux web --state %s\n", state, shellQuote(*output), shellQuote(state), shellQuote(state))
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

func writeResearchBehaviorCSV(path string, rows []research.Behavior) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	w := csv.NewWriter(file)
	if err := w.Write([]string{"behavior_id", "trial", "condition", "trial_status", "round", "actor", "harness", "model", "valid", "execution_failed", "contribution", "invested", "returned", "retained", "utility", "payoff", "remaining_credits", "intervened"}); err != nil {
		return err
	}
	for _, r := range rows {
		row := []string{r.ID, r.Trial, r.Condition, r.TrialStatus, strconv.Itoa(r.Round), r.Actor, r.Harness, r.Model, strconv.FormatBool(r.Valid), strconv.FormatBool(r.ExecutionFailed), strconv.Itoa(r.Contribution), strconv.Itoa(r.Invested), strconv.Itoa(r.Returned), strconv.Itoa(r.Retained), strconv.FormatFloat(r.Utility, 'g', -1, 64), strconv.FormatFloat(r.Payoff, 'g', -1, 64), strconv.Itoa(r.RemainingCredits), strconv.FormatBool(r.Intervened)}
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
	if err := w.Write([]string{"condition", "completed_runs", "mean_total_social_welfare", "mean_funding_rate", "mean_credits_spent", "mean_invalid_actions", "mean_consented_teams"}); err != nil {
		return err
	}
	for _, c := range a.Conditions {
		row := []string{c.Condition, strconv.Itoa(c.CompletedRuns), strconv.FormatFloat(c.SocialWelfare.Mean, 'g', -1, 64), strconv.FormatFloat(c.FundingRate.Mean, 'g', -1, 64), strconv.FormatFloat(c.Cost.Mean, 'g', -1, 64), strconv.FormatFloat(c.InvalidActions.Mean, 'g', -1, 64), strconv.FormatFloat(c.ConsentedTeams.Mean, 'g', -1, 64)}
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
	if (mutating && (*version < 1 || *reason == "")) || (!mutating && (*version != 0 || *reason != "")) {
		fmt.Fprintln(stderr, "research interventions require --version N --reason TEXT; status/watch accept no mutation flags")
		return exitValidation
	}
	previous := ""
	for {
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
