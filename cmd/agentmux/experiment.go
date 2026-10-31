package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"agentmux/internal/experiment"
)

func runExperiment(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runExperimentIO(ctx, args, os.Stdout, os.Stderr)
}

func runExperimentIO(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := func() {
		fmt.Fprintln(stderr, "Usage: agentmux experiment plan <manifest.yaml>\n       agentmux experiment run <manifest.yaml> --output <private-dir> [--allow-models] [--rerun-interrupted]")
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		usage()
		return exitSuccess
	}
	if len(args) < 2 || (args[0] != "plan" && args[0] != "run") {
		usage()
		return exitValidation
	}
	flags := flag.NewFlagSet("experiment "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "private result directory")
	allowModels := flags.Bool("allow-models", false, "allow provider-backed harnesses and their charges")
	rerun := flags.Bool("rerun-interrupted", false, "create fresh attempts after stopping interrupted trial processes")
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
		return exitValidation
	}
	if args[0] == "plan" && (*output != "" || *allowModels || *rerun) {
		fmt.Fprintln(stderr, "plan does not accept execution flags")
		return exitValidation
	}
	m, err := experiment.Load(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "experiment: %v\n", err)
		return exitConfigParse
	}
	var result any
	if args[0] == "plan" {
		result, err = experiment.Plan(m)
	} else {
		if *output == "" {
			usage()
			return exitValidation
		}
		executable, _ := os.Executable()
		result, err = experiment.Run(ctx, m, experiment.RunOptions{OutputDir: *output, Executable: executable, AllowModels: *allowModels, RerunInterrupted: *rerun})
	}
	if err != nil {
		fmt.Fprintf(stderr, "experiment: %v\n", err)
		return exitRuntime
	}
	if err := json.NewEncoder(stdout).Encode(map[string]any{"schema_version": 1, "kind": "experiment", "operation": args[0], "ok": true, "result": result}); err != nil {
		return exitRuntime
	}
	return exitSuccess
}
