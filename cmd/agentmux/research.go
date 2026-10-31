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

	"agentmux/internal/research"
)

func runResearch(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runResearchIO(ctx, args, os.Stdout, os.Stderr)
}
func runResearchIO(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := func() {
		fmt.Fprintln(stderr, "Usage: agentmux research plan <manifest.yaml>\n       agentmux research run <manifest.yaml> --output <private-dir> [--allow-models] [--rerun-interrupted]\n       agentmux research analyze <output-dir> [--csv <new-file>]")
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		usage()
		return exitSuccess
	}
	if len(args) < 2 || (args[0] != "plan" && args[0] != "run" && args[0] != "analyze") {
		usage()
		return exitValidation
	}
	flags := flag.NewFlagSet("research "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "private output directory")
	allow := flags.Bool("allow-models", false, "allow real model/provider calls")
	rerun := flags.Bool("rerun-interrupted", false, "create a fresh attempt after stopping interrupted processes")
	csvPath := flags.String("csv", "", "new private descriptive analysis CSV")
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
		return exitValidation
	}
	if args[0] == "plan" && (*output != "" || *allow || *rerun || *csvPath != "") {
		fmt.Fprintln(stderr, "plan accepts no execution/export flags")
		return exitValidation
	}
	var result any
	var err error
	if args[0] == "analyze" {
		if *output != "" || *allow || *rerun {
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
		}
	} else {
		if *csvPath != "" {
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
				report, err = research.Run(ctx, manifest, research.RunOptions{OutputDir: *output, Executable: binary, AllowModels: *allow, RerunInterrupted: *rerun})
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
