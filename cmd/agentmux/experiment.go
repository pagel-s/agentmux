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
	"agentmux/internal/managedlaunch"
)

func runExperiment(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runExperimentIO(ctx, args, os.Stdout, os.Stderr)
}

func runExperimentIO(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := func() {
		fmt.Fprintln(stderr, "Usage: amx experiment plan <manifest.yaml>\n       amx experiment run <manifest.yaml> --output <private-dir> [--allow-models] [--rerun-interrupted]")
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		usage()
		return exitSuccess
	}
	if len(args) == 2 && (args[0] == "plan" || args[0] == "run") && helpArgument(args[1]) {
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
	if err := flags.Parse(args[2:]); err != nil {
		if err == flag.ErrHelp {
			usage()
			return exitSuccess
		}
		return exitValidation
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "experiment accepts one manifest path followed by flags; run amx experiment --help")
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
		result, err = experiment.Run(ctx, m, experiment.RunOptions{OutputDir: *output, Executable: executable, AllowModels: *allowModels, RerunInterrupted: *rerun, OnLive: func(state string) { printExperimentLive(stderr, executable, state) }})
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

func printExperimentLive(out io.Writer, executable, state string) {
	fmt.Fprintln(out, "Active single-task pilot state:", state)
	for _, command := range []string{"dashboard", "web", "activity"} {
		fmt.Fprintln(out, managedlaunch.ShellCommand([]string{executable, command, "--state", state}))
	}
	fmt.Fprintln(out, "Observe Tasks/Events and use task cancellation, boards and memory in the owner console. These workers have no native terminal. Owner steering is recorded in broker events; this legacy pilot does not independently classify pristine versus intervened results.")
}
