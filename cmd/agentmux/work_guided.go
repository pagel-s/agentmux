package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"agentmux/internal/egress"
	"agentmux/internal/workflow"
)

const guidedWorkUsage = `Usage: amx work "Describe the change" [options] -- CHECK [ARG...]
       amx work "Describe the change" [options]
       amx work status [RUN_ID|RUN_DIR|latest] [--repo DIR] [--catalog DIR] [--json]
       amx work list [--repo DIR] [--catalog DIR] [--limit N] [--json]
       amx work show [RUN_ID|RUN_DIR|latest] [--repo DIR] [--catalog DIR] [--json]
       amx work show RUN_ID|RUN_DIR|latest [--repo DIR] [--catalog DIR] --open
       amx work apply [RUN_ID|RUN_DIR|latest] [--repo DIR] [--catalog DIR] [--json]
       amx work recover [RUN_ID|RUN_DIR|latest] [--repo DIR] [--catalog DIR] [--json]

Preview is the default: it starts no models and writes no run files.
Add --allow-models to execute implementation, fresh review, any review fixes,
and declared acceptance checks. Trusted-local defaults use installed native CLIs:
prefer Codex for coding and a different available reviewer, including Claude.
A single installed CLI can serve both roles with separate fresh review context.
PATH discovery checks availability, not login. Sign in through each selected CLI.

Options:
  --repo DIR       Project directory (default: current directory)
  --output DIR     New result directory outside the project (default: private catalog)
  --catalog DIR    Private run catalog (default: XDG state directory)
  --timeout 10m    Overall deadline, from 1s to 24h in whole seconds
  --check-timeout 1m  Deadline per acceptance check, from 1s to 10m
  --attempts 2     Bounded implementation correction attempts
  --containment process-group  Use process-group or Linux pid-namespace containment
  --storage private  Use private snapshots, or explicitly select legacy shared storage
  --isolation bubblewrap  Require filesystem and network isolation on Linux
  --network none    Isolated network: none or allowlist
  --allow-origin ORIGIN  Exact allowed inference origin (repeatable)
  --read-only-path DIR  Selected runtime dependency (repeatable)
  --auth ID=FILE    Explicit isolated credential file for coder/reviewer (repeatable)
  --auth-provider ID=NAME  Provider entry in an isolated Pi credential file
  --coder NAME     Coding profile (default: installed Codex, Claude or Antigravity)
  --reviewer NAME  Review profile (default: a different installed CLI when available)
  --dry-run        Preview even when --allow-models is supplied
  --allow-models   Permit provider turns and their charges
  --json           Print a machine-readable result instead of prose

After --, the check is literal executable argv, never shell text.
Without an explicit check, supported unambiguous Go/npm project checks are detected.
Commands and native tools use trusted local authority. Worktrees separate files;
they are not a sandbox. Applying a result is always a separate explicit action.
Interrupted work is retained and never automatically replayed.
Profiles: codex, claude, antigravity or antigravity-bridge.
Explicit profiles are never replaced. Isolated defaults remain Codex coding
and the Antigravity bridge reviewer; select other isolated profiles explicitly.
Isolated previews and runs inspect selected auth before output. Their CLI output
is deferred until completion and checked in bounded memory; unsafe output is refused.
See docs/working.md.`

// The executable fixes its provider choices. Tests may replace them with
// transparent command fixtures without exposing a provider-bypass CLI flag.
var configureGuidedWork = func(options workflow.GuidedOptions) workflow.GuidedOptions { return options }

type guidedValues []string

func (v *guidedValues) String() string         { return strings.Join(*v, ",") }
func (v *guidedValues) Set(value string) error { *v = append(*v, value); return nil }
func (v *guidedValues) AllowRepeated() bool    { return true }

func runGuidedWork(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runGuidedWorkIO(ctx, args, os.Stdout, os.Stderr)
}

func runGuidedWorkIO(ctx context.Context, args []string, out, diagnostics io.Writer) int {
	return runCLIPublicationSession(ctx, out, diagnostics, false, func(ctx context.Context, session *cliPublicationSession) (context.Context, error) {
		return prepareGuidedCLIPublication(ctx, session, args)
	}, func(ctx context.Context, out, diagnostics io.Writer) int {
		return runGuidedWorkUnguardedIO(ctx, args, out, diagnostics)
	})
}

func runGuidedWorkUnguardedIO(ctx context.Context, args []string, out, diagnostics io.Writer) int {
	diagnostics = workflowDiagnosticWriter{diagnostics}
	if len(args) == 0 || len(args) == 1 && helpArgument(args[0]) || len(args) == 2 && helpArgument(args[1]) {
		fmt.Fprintln(out, guidedWorkUsage)
		return exitSuccess
	}
	switch args[0] {
	case "list", "status", "show", "apply", "recover":
		return runGuidedHistory(ctx, args[0], args[1:], out, diagnostics)
	}
	flags := flag.NewFlagSet("work", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	repository := flags.String("repo", ".", "project directory")
	output := flags.String("output", "", "new result directory")
	catalog := flags.String("catalog", "", "private run catalog")
	timeout := flags.Duration("timeout", 10*time.Minute, "overall deadline")
	checkTimeout := flags.Duration("check-timeout", time.Minute, "deadline per acceptance check")
	attempts := flags.Int("attempts", 2, "bounded implementation correction attempts")
	containment := flags.String("containment", "process-group", "process-group or Linux pid-namespace containment")
	storage := flags.String("storage", "private", "private or legacy shared snapshot storage")
	isolation := flags.String("isolation", "", "required bubblewrap filesystem and network isolation")
	network := flags.String("network", "", "none or allowlist for isolated execution")
	coder := flags.String("coder", "", "coding provider profile")
	reviewer := flags.String("reviewer", "", "review provider profile")
	var origins, readOnlyPaths, authFiles, authProviders guidedValues
	flags.Var(&origins, "allow-origin", "exact allowed inference origin")
	flags.Var(&readOnlyPaths, "read-only-path", "selected read-only runtime dependency")
	flags.Var(&authFiles, "auth", "candidate ID=absolute credential file")
	flags.Var(&authProviders, "auth-provider", "candidate ID=Pi provider name")
	dryRun := flags.Bool("dry-run", false, "preview without starting models")
	allowModels := flags.Bool("allow-models", false, "allow provider turns and charges")
	asJSON := flags.Bool("json", false, "machine-readable output")
	before, check := splitGuidedCheck(args)
	*asJSON = guidedJSONRequested(flags, before)
	positionals, err := parseGuidedFlags(flags, before)
	encodedResult := false
	fail := func(code int, err error) int {
		if *asJSON && !encodedResult {
			encodedResult = true
			if encodeErr := encodeWorkflow(out, "work.error", nil, err); encodeErr != nil {
				fmt.Fprintf(diagnostics, "work: cannot write JSON error: %q\n", encodeErr.Error())
			}
		}
		fmt.Fprintf(diagnostics, "work: %q\n", err.Error())
		return code
	}
	if err != nil {
		return fail(exitValidation, err)
	}
	if len(positionals) != 1 || strings.TrimSpace(positionals[0]) == "" {
		return fail(exitValidation, errors.New("provide one quoted description of the change; see amx work --help"))
	}
	if check != nil && len(check) == 0 {
		return fail(exitValidation, errors.New("provide the check executable and its arguments after --"))
	}
	if *timeout < time.Second || *timeout > 24*time.Hour || *timeout%time.Second != 0 || *attempts < 1 || *attempts > 5 {
		return fail(exitValidation, errors.New("timeout requires whole seconds within 1s–24h; attempts must be 1–5"))
	}
	if *checkTimeout < time.Second || *checkTimeout > 10*time.Minute || *checkTimeout%time.Second != 0 {
		return fail(exitValidation, errors.New("check-timeout requires whole seconds within 1s–10m"))
	}
	isolationOptions, err := guidedIsolationOptions(*isolation, *network, origins, readOnlyPaths, authFiles, authProviders)
	if err != nil {
		return fail(exitValidation, err)
	}
	explicitOutput := *output != ""
	if !explicitOutput {
		*output, err = workflow.SuggestedOutput(ctx, *repository, *catalog)
		if err != nil {
			return fail(exitValidation, err)
		}
	}
	options := configureGuidedWork(workflow.GuidedOptions{Repository: *repository, Storage: *storage, Isolation: isolationOptions, CoderHarness: *coder, ReviewerHarness: *reviewer, ProcessContainment: *containment, Goal: positionals[0], Output: *output, CheckCommand: check, TimeoutSeconds: int(*timeout / time.Second), MaxAttempts: *attempts, CheckTimeoutSeconds: int(*checkTimeout / time.Second)})
	options, err = selectGuidedProviders(options, nil)
	if err != nil {
		return fail(exitValidation, err)
	}
	ctx, err = selectGuidedCLIOptions(ctx, options)
	if err != nil {
		return exitRuntime
	}
	preview, err := workflow.PrepareGuided(ctx, options)
	if err != nil {
		return fail(exitValidation, err)
	}
	if explicitOutput {
		parent, statErr := os.Stat(filepath.Dir(preview.Output))
		if statErr != nil || !parent.IsDir() {
			return fail(exitValidation, errors.New("the selected output parent must already exist; create it first or omit --output to use private work history"))
		}
	}
	if *dryRun || !*allowModels {
		if *asJSON {
			encodedResult = true
			if err := encodeWorkflow(out, "work.preview", preview, nil); err != nil {
				return fail(exitRuntime, err)
			}
		} else {
			printGuidedPreview(out, preview, *catalog, explicitOutput, *allowModels && *dryRun)
		}
		return exitSuccess
	}
	if err := workflow.PrepareCatalog(ctx, preview.Repository, *catalog); err != nil {
		return fail(exitRuntime, err)
	}
	// Explicit output paths keep their ordinary existing-parent requirement;
	// the catalog owns only its own new directory hierarchy.
	if !*asJSON {
		fmt.Fprintf(out, "Working on: %s\nProject: %s\n", guidedLine(options.Goal), guidedLine(preview.Repository))
		printGuidedProfiles(out, preview.Plan.Candidates)
		fmt.Fprintf(out, "Plan: normally four native turns, up to %d within %s. Check timeout: %s.\n", preview.MaxNativeTurns, *timeout, *checkTimeout)
		fmt.Fprintln(out, "Agentmux integrates no changes into the source until you explicitly apply a checked result.")
	}
	fmt.Fprintf(diagnostics, "Results: %q\n", preview.Output)
	stopProgress := startGuidedProgress(ctx, preview.Output, diagnostics, func(registerCtx context.Context) error {
		return workflow.RegisterRun(registerCtx, preview.Repository, *catalog, preview.Output)
	})
	report, runErr := workflow.RunGuided(ctx, options, true)
	stopProgress()
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer finishCancel()
	if report.ID != "" {
		if registerErr := workflow.RegisterRun(finishCtx, preview.Repository, *catalog, report.Output); registerErr != nil {
			fmt.Fprintf(diagnostics, "Work evidence is retained at %q, but catalog registration failed: %q\n", report.Output, registerErr.Error())
			runErr = errors.Join(runErr, fmt.Errorf("register retained result: %w", registerErr))
		}
	}
	if *asJSON {
		encodedResult = true
		if encodeErr := encodeWorkflow(out, "work.run", report, runErr); encodeErr != nil {
			return fail(exitRuntime, encodeErr)
		}
	} else if report.ID != "" {
		summary, summaryErr := workflow.Summarize(finishCtx, report.Output)
		if summaryErr != nil {
			fmt.Fprintf(out, "Recorded result: %s\nEvidence: %s\n", guidedLine(report.State), guidedLine(report.Output))
			fmt.Fprintf(diagnostics, "Summary unavailable: %q\n", summaryErr.Error())
		} else {
			printGuidedSummary(out, summary)
		}
	}
	if runErr != nil {
		return fail(exitRuntime, runErr)
	}
	return exitSuccess
}

func splitGuidedCheck(args []string) ([]string, []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], append([]string{}, args[i+1:]...)
		}
	}
	return args, nil
}

func guidedIsolationOptions(mode, network string, origins, dependencies, files, providers []string) (*workflow.IsolationOptions, error) {
	if mode == "" {
		if network != "" || len(origins)+len(dependencies)+len(files)+len(providers) > 0 {
			return nil, errors.New("network, credentials and dependency capabilities require --isolation bubblewrap")
		}
		return nil, nil
	}
	if mode != "bubblewrap" {
		return nil, errors.New("isolation must be bubblewrap")
	}
	if network == "" {
		network = "none"
		if len(origins) > 0 {
			network = "allowlist"
		}
	}
	o := &workflow.IsolationOptions{Mode: mode, Network: network, Auth: map[string]workflow.IsolationAuth{}}
	for _, origin := range origins {
		if !utf8.ValidString(origin) {
			return nil, errors.New("allowed origins must be valid UTF-8")
		}
		o.Allow = append(o.Allow, egress.Endpoint{Origin: origin})
	}
	for _, path := range dependencies {
		if !utf8.ValidString(path) {
			return nil, errors.New("dependency paths must be valid UTF-8")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		o.ReadOnlyPaths = append(o.ReadOnlyPaths, absolute)
	}
	for _, value := range files {
		id, file, ok := strings.Cut(value, "=")
		if !ok || id == "" || file == "" || !utf8.ValidString(value) {
			return nil, errors.New("auth requires an exact candidate ID=credential file")
		}
		if _, exists := o.Auth[id]; exists {
			return nil, errors.New("choose one isolated credential file per candidate")
		}
		absolute, err := filepath.Abs(file)
		if err != nil {
			return nil, err
		}
		o.Auth[id] = workflow.IsolationAuth{File: absolute}
	}
	seen := map[string]bool{}
	for _, value := range providers {
		id, provider, ok := strings.Cut(value, "=")
		auth, exists := o.Auth[id]
		if !ok || !exists || provider == "" || seen[id] || !utf8.ValidString(value) {
			return nil, errors.New("auth-provider requires one ID=provider for an existing auth selection")
		}
		seen[id] = true
		auth.Provider = provider
		o.Auth[id] = auth
	}
	return workflow.NormalizedIsolation(o), nil
}

// Permit flags around the one quoted goal/selector while retaining every check
// argument after -- exactly as supplied. Unknown flags fail before any work.
func parseGuidedFlags(flags *flag.FlagSet, args []string) ([]string, error) {
	var options, positionals []string
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		name, _, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		f := flags.Lookup(name)
		if f == nil {
			return nil, fmt.Errorf("unknown option %q", arg)
		}
		repeated, mayRepeat := f.Value.(interface{ AllowRepeated() bool })
		if seen[name] && !(mayRepeat && repeated.AllowRepeated()) {
			return nil, fmt.Errorf("option --%s was supplied more than once; choose one value", name)
		}
		seen[name] = true
		options = append(options, arg)
		boolean, isBoolean := f.Value.(interface{ IsBoolFlag() bool })
		if !inline && !(isBoolean && boolean.IsBoolFlag()) {
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			options = append(options, args[i])
		}
	}
	if err := flags.Parse(options); err != nil {
		return nil, err
	}
	return positionals, nil
}

// Preserve a valid output-mode request even when another option fails before
// FlagSet.Parse reaches it. Values of known nonboolean options and literal
// check argv must never be reinterpreted as the JSON flag.
func guidedJSONRequested(flags *flag.FlagSet, args []string) bool {
	requested := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		name, value, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		option := flags.Lookup(name)
		if option == nil {
			continue
		}
		if name == "json" {
			if !inline {
				requested = true
			} else if mode, err := strconv.ParseBool(value); err == nil {
				requested = mode
			}
		}
		boolean, isBoolean := option.Value.(interface{ IsBoolFlag() bool })
		if !inline && !(isBoolean && boolean.IsBoolFlag()) {
			i++
		}
	}
	return requested
}

func runGuidedHistory(ctx context.Context, operation string, args []string, out, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("work "+operation, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	repository := flags.String("repo", ".", "project directory")
	catalog := flags.String("catalog", "", "private run catalog")
	asJSON := flags.Bool("json", false, "machine-readable output")
	limit := 0
	openReport := false
	if operation == "list" {
		flags.IntVar(&limit, "limit", 0, "show the newest N runs; omitted lists complete history")
	}
	if operation == "show" {
		flags.BoolVar(&openReport, "open", false, "open a fresh private report for an explicit run selector; cannot combine with --json")
	}
	*asJSON = guidedJSONRequested(flags, args)
	positionals, err := parseGuidedFlags(flags, args)
	encodedResult := false
	fail := func(code int, err error) int {
		if *asJSON && !encodedResult {
			encodedResult = true
			if encodeErr := encodeWorkflow(out, "work.error", nil, err); encodeErr != nil {
				fmt.Fprintf(diagnostics, "work: cannot write JSON error: %q\n", encodeErr.Error())
			}
		}
		fmt.Fprintf(diagnostics, "work: %q\n", err.Error())
		return code
	}
	if err != nil {
		return fail(exitValidation, err)
	}
	if operation == "list" {
		if len(positionals) > 0 {
			return fail(exitValidation, errors.New("list takes no run selector"))
		}
		limited := false
		flags.Visit(func(option *flag.Flag) { limited = limited || option.Name == "limit" })
		if limited && (limit < 1 || limit > 1024) {
			return fail(exitValidation, errors.New("limit must be between 1 and 1024; omit --limit for complete history"))
		}
		runs, err := workflow.ListRuns(ctx, *repository, *catalog)
		if err != nil {
			return fail(exitRuntime, err)
		}
		total := len(runs)
		if limited && len(runs) > limit {
			runs = runs[:limit]
		}
		if *asJSON {
			encodedResult = true
			if err := encodeWorkflow(out, "work.list", runs, nil); err != nil {
				return fail(exitRuntime, err)
			}
		} else if len(runs) == 0 {
			fmt.Fprintln(out, "No recorded work for this project. Preview your first task with amx work \"Describe the change\".")
		} else {
			if len(runs) < total {
				fmt.Fprintf(out, "Recorded work for this project (showing %d of %d):\n", len(runs), total)
			} else {
				fmt.Fprintln(out, "Recorded work for this project:")
			}
			for _, run := range runs {
				fmt.Fprintf(out, "  %s · %s · %s\n    %s\n    %s\n", guidedLine(run.ID), guidedLine(run.State), guidedLine(run.Goal), guidedLine(run.StartedAt), guidedLine(run.Directory))
			}
			fmt.Fprintln(out, "Use the run ID with amx work status, show, apply or recover in this project.")
		}
		return exitSuccess
	}
	if len(positionals) > 1 {
		return fail(exitValidation, errors.New("provide at most one run ID, directory or latest"))
	}
	if openReport && (*asJSON || len(positionals) != 1 || strings.TrimSpace(positionals[0]) == "") {
		return fail(exitValidation, errors.New("--open requires one explicit run ID, directory or latest and cannot be combined with --json"))
	}
	selector := "latest"
	if len(positionals) == 1 {
		selector = positionals[0]
	}
	directory, err := workflow.ResolveRun(ctx, *repository, *catalog, selector)
	if err != nil {
		return fail(exitRuntime, err)
	}
	if operation == "apply" {
		result, applyErr := workflow.Apply(ctx, directory, "")
		if *asJSON {
			encodedResult = true
			if err := encodeWorkflow(out, "work.apply", result, applyErr); err != nil {
				return fail(exitRuntime, err)
			}
		} else {
			fmt.Fprintln(out, guidedLine(result.Note))
			if result.Applied {
				fmt.Fprintf(out, "Project: %s\nInspect the source diff before committing.\n", guidedLine(result.Repository))
			}
			if result.Uncertain {
				fmt.Fprintln(out, "Application needs inspection. Changes may already be present; do not automatically retry.")
			}
		}
		if applyErr != nil {
			return fail(exitRuntime, applyErr)
		}
		return exitSuccess
	}
	if operation == "recover" {
		result, recoveryErr := workflow.RecoverApply(ctx, directory, "")
		if *asJSON {
			encodedResult = true
			if err := encodeWorkflow(out, "work.recover", result, recoveryErr); err != nil {
				return fail(exitRuntime, err)
			}
		} else {
			fmt.Fprintf(out, "Application recovery: %s\nSource observation: %s\n%s\n", guidedLine(result.State), guidedLine(result.SourceObservation), guidedLine(result.Note))
			fmt.Fprintln(out, "Recovery inspects source and updates saved application records. It never reapplies a patch or changes source files.")
		}
		if recoveryErr != nil {
			return fail(exitRuntime, recoveryErr)
		}
		return exitSuccess
	}
	summary, err := workflow.Summarize(ctx, directory)
	if err != nil {
		return fail(exitRuntime, err)
	}
	if *asJSON {
		encodedResult = true
		if err := encodeWorkflow(out, "work."+operation, summary, nil); err != nil {
			return fail(exitRuntime, err)
		}
	} else {
		printGuidedSummary(out, summary)
		if operation == "show" {
			printGuidedPatch(out, summary)
			if openReport {
				if err := openGuidedHistoryReport(ctx, directory, summary.ID, out); err != nil {
					return fail(exitRuntime, err)
				}
			} else {
				if command := guidedHistoryShowCommand(summary); command != "" {
					fmt.Fprintf(out, "Open a fresh report in your browser:\n%s\n", command)
				} else {
					fmt.Fprintln(out, "Browser command omitted because a path contains display controls; inspect --json for exact paths.")
				}
			}
		}
	}
	return exitSuccess
}

func printGuidedPreview(out io.Writer, preview workflow.GuidedPreview, catalog string, explicitOutput, forced bool) {
	minimum, maximum := 0, 0
	for _, step := range preview.Plan.Steps {
		if step.Candidate == "" {
			continue
		}
		minimum++
		if step.Verify {
			maximum += preview.Plan.MaxAttempts
		} else {
			maximum++
		}
	}
	if preview.Plan.ReviewPolicy == workflow.ReviewPolicyEarlyApproval {
		minimum = 2
	}
	fmt.Fprintf(out, "Preview: %s\nProject: %s\n\n", guidedLine(preview.Plan.Goal), guidedLine(preview.Repository))
	printGuidedProfiles(out, preview.Plan.Candidates)
	for i, phase := range preview.Phases {
		fmt.Fprintf(out, "  %d. %s\n", i+1, guidedLine(phase))
	}
	fmt.Fprintf(out, "\nCheck (%s):", guidedLine(preview.CheckSource))
	for _, arg := range preview.Plan.Checks[0].Command {
		fmt.Fprintf(out, " %s", guidedLine(guidedShellWord(arg)))
	}
	fmt.Fprintf(out, "\nDeadline: %s · native turns: %d–%d\nResults: %s\n", time.Duration(preview.Plan.TimeoutSeconds)*time.Second, minimum, maximum, guidedLine(preview.Output))
	fmt.Fprintf(out, "Check timeout: %s\n", time.Duration(preview.Plan.Checks[0].TimeoutSeconds)*time.Second)
	fmt.Fprintf(out, "Snapshot: %d selected files · %d bytes (limits: %d files, %d bytes)\n", preview.Selection.Files, preview.Selection.Bytes, workflow.MaxSnapshotFiles, workflow.MaxSnapshotBytes)
	fmt.Fprintf(out, "Process containment: %s\nExecution: %s\n", guidedLine(preview.Plan.ProcessContainment), guidedLine(preview.Authority))
	fmt.Fprintf(out, "Snapshot storage: %s · reviews: fresh context\n", guidedLine(preview.Plan.Storage))
	if preview.Plan.ReviewPolicy == workflow.ReviewPolicyEarlyApproval {
		fmt.Fprintln(out, "A clean first review skips the response and second review only for the same checked revision; final checks always run.")
	}
	fmt.Fprintln(out, "Preview only. No models started and no run files were written.")
	if forced {
		fmt.Fprintln(out, "Remove --dry-run to execute the approved request.")
		return
	}
	executionFlags, representable := guidedExecutionFlags(preview.Plan)
	if !representable {
		fmt.Fprintln(out, "To execute, add --allow-models before -- in your original command; retain all configured capabilities.")
		return
	}
	values := append([]string{preview.Plan.Goal, preview.Repository, preview.Output, catalog}, preview.Plan.Checks[0].Command...)
	values = append(values, executionFlags...)
	for _, value := range values {
		if value != guidedLine(value) {
			fmt.Fprintln(out, "To execute, add --allow-models before -- in your original command. Control characters above are escaped for display.")
			return
		}
	}
	// Reconstruct from already parsed intent rather than reinterpreting shell
	// strings. This places permission before -- and preserves literal check argv.
	fmt.Fprint(out, "Run: amx work "+guidedShellWord(preview.Plan.Goal)+" --repo "+guidedShellWord(preview.Repository)+" --allow-models")
	if explicitOutput {
		fmt.Fprint(out, " --output "+guidedShellWord(preview.Output))
	}
	if catalog != "" {
		fmt.Fprint(out, " --catalog "+guidedShellWord(catalog))
	}
	if preview.Plan.ProcessContainment != "" && preview.Plan.ProcessContainment != "process-group" {
		fmt.Fprint(out, " --containment "+guidedShellWord(preview.Plan.ProcessContainment))
	}
	for _, arg := range executionFlags {
		fmt.Fprint(out, " "+guidedShellWord(arg))
	}
	fmt.Fprintf(out, " --timeout %s --check-timeout %s --attempts %d --", time.Duration(preview.Plan.TimeoutSeconds)*time.Second, time.Duration(preview.Plan.Checks[0].TimeoutSeconds)*time.Second, preview.Plan.MaxAttempts)
	for _, arg := range preview.Plan.Checks[0].Command {
		fmt.Fprint(out, " "+guidedShellWord(arg))
	}
	fmt.Fprintln(out)
}

// A suggested command must retain every capability. Advanced plan-only options
// have no CLI spelling, so those previews refer back to the original request.
func guidedExecutionFlags(plan workflow.Plan) ([]string, bool) {
	args := []string{"--storage", plan.Storage}
	for i, candidate := range plan.Candidates {
		if i > 1 {
			return nil, false
		}
		if candidate.Harness == "command" { // explicit embedded fixture/profile
			continue
		}
		role := "--coder"
		if i == 1 {
			role = "--reviewer"
		}
		args = append(args, role, candidate.Harness)
	}
	if plan.Isolation == nil {
		return args, true
	}
	o := plan.Isolation
	defaults := workflow.NormalizedIsolation(&workflow.IsolationOptions{})
	if o.HomeBytes != defaults.HomeBytes || o.TempBytes != defaults.TempBytes || o.BridgeMaxRequests != defaults.BridgeMaxRequests || len(o.BridgeCommand) > 0 || o.RelayExecutable != "" {
		return nil, false
	}
	args = append(args, "--isolation", o.Mode, "--network", o.Network)
	for _, endpoint := range o.Allow {
		if len(endpoint.IPs) > 0 {
			return nil, false
		}
		args = append(args, "--allow-origin", endpoint.Origin)
	}
	for _, path := range o.ReadOnlyPaths {
		args = append(args, "--read-only-path", path)
	}
	ids := make([]string, 0, len(o.Auth))
	for id := range o.Auth {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		auth := o.Auth[id]
		args = append(args, "--auth", id+"="+auth.File)
		if auth.Provider != "" {
			args = append(args, "--auth-provider", id+"="+auth.Provider)
		}
	}
	return args, true
}

func printGuidedSummary(out io.Writer, summary workflow.WorkSummary) {
	fmt.Fprintf(out, "\n%s\nRun: %s\nTask: %s\nProject: %s\n", guidedLine(summary.Outcome), guidedLine(summary.ID), guidedLine(summary.Goal), guidedLine(summary.Repository))
	fmt.Fprintf(out, "Process containment: %s\n", guidedLine(summary.ProcessContainment))
	fmt.Fprintf(out, "Snapshot storage: %s\n", guidedLine(summary.Storage))
	if summary.Isolation != "" {
		fmt.Fprintf(out, "Filesystem isolation: %s\n", guidedLine(summary.Isolation))
	}
	for _, phase := range summary.Phases {
		fmt.Fprintf(out, "  %s: %s\n", guidedPhaseLabel(phase.ID), guidedStateLabel(phase.State))
	}
	fmt.Fprintf(out, "Checks: %d/%d passed\n", summary.ChecksPassed, summary.ChecksTotal)
	if summary.Review != "" {
		fmt.Fprintf(out, "\nReview (%s):\n%s\n", guidedLine(summary.ReviewState), guidedLine(summary.Review))
	}
	for _, finding := range summary.Findings {
		fmt.Fprintf(out, "  %s:%d: %s\n", guidedLine(finding.File), finding.Line, guidedLine(finding.Message))
	}
	if summary.ReviewTruncated {
		fmt.Fprintln(out, "Review excerpt only; inspect complete findings in the retained turn evidence.")
	}
	if len(summary.Files) > 0 {
		fmt.Fprintln(out, "\nChanged files:")
		for _, file := range summary.Files {
			fmt.Fprintf(out, "  %s %s\n", guidedLine(file.Status), guidedLine(file.Path))
		}
	}
	if summary.FilesTruncated {
		fmt.Fprintln(out, "Additional files are listed in the retained patch.")
	}
	if summary.Error != "" {
		fmt.Fprintf(out, "\nRecorded problem: %s\n", guidedLine(summary.Error))
	}
	fmt.Fprintf(out, "\nNext: %s\n", guidedLine(summary.NextAction))
	if summary.NextCommand != "" {
		fmt.Fprintln(out, guidedLine(summary.NextCommand))
	}
	fmt.Fprintf(out, "Evidence: %s\n", guidedLine(summary.Directory))
	if summary.ReportPath != "" {
		fmt.Fprintf(out, "Report: %s\n", guidedLine(summary.ReportPath))
	}
}

func guidedHistoryShowCommand(summary workflow.WorkSummary) string {
	command := "amx work show " + guidedShellWord(summary.Directory) + " --repo " + guidedShellWord(summary.Repository) + " --open"
	if guidedLine(command) != command {
		return ""
	}
	return command
}

func startGuidedProgress(ctx context.Context, directory string, out io.Writer, register func(context.Context) error) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		previous := map[string]string{}
		registered := false
		poll := func() {
			report, err := workflow.LoadReport(directory)
			if err != nil {
				return
			}
			if !registered && register != nil {
				registerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				registerErr := register(registerCtx)
				cancel()
				registered = registerErr == nil
			}
			for _, step := range report.Steps {
				signature := step.State + step.Error
				if previous[step.ID] == signature {
					continue
				}
				previous[step.ID] = signature
				fmt.Fprintf(out, "  %s: %s\n", guidedPhaseLabel(step.ID), guidedStateLabel(step.State))
			}
		}
		fmt.Fprintln(out, "Preparing a separate worktree…")
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				poll()
			case <-ctx.Done():
				return
			case <-stop:
				poll()
				return
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }); <-done }
}

func guidedLine(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func guidedShellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func printGuidedPatch(out io.Writer, summary workflow.WorkSummary) {
	if summary.PatchPreviewError != "" {
		fmt.Fprintf(out, "\nPatch preview unavailable: %s\n", guidedLine(summary.PatchPreviewError))
		return
	}
	if summary.PatchPath == "" {
		fmt.Fprintln(out, "\nA final patch is not available for this recorded run.")
		return
	}
	if summary.PatchPreview == "" {
		fmt.Fprintln(out, "\nNo changes are recorded in the retained patch.")
		return
	}
	preview, truncated := guidedPatchBlock(summary.PatchPreview)
	fmt.Fprintln(out, "\nPatch preview (control characters are escaped for display):")
	fmt.Fprint(out, preview)
	if !strings.HasSuffix(preview, "\n") {
		fmt.Fprintln(out)
	}
	if summary.PatchPreviewTruncated || truncated {
		fmt.Fprintln(out, "Preview truncated at 64 KiB; inspect the complete retained patch before applying.")
	}
	fmt.Fprintf(out, "Full patch: %s\n", guidedLine(summary.PatchPath))
}

// Preserve code layout while refusing terminal actions and directional controls.
// The backend also bounds and sanitizes the preview; this presentation boundary
// independently caps its displayed representation, including expanded escapes.
func guidedPatchBlock(value string) (string, bool) {
	const maximum = 64 << 10
	var b strings.Builder
	for _, r := range value {
		piece := string(r)
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069) {
			piece = fmt.Sprintf("\\u%04x", r)
		}
		if b.Len()+len(piece) > maximum {
			return b.String(), true
		}
		b.WriteString(piece)
	}
	return b.String(), false
}

func guidedPhaseLabel(id string) string {
	switch id {
	case "implement":
		return "Implement and check"
	case "review":
		return "Review implementation"
	case "address-review":
		return "Address review findings"
	case "final-review":
		return "Review final revision"
	case "final-check":
		return "Final acceptance checks"
	default:
		return guidedLine(strings.ReplaceAll(strings.ReplaceAll(id, "-", " "), "_", " "))
	}
}

func guidedStateLabel(state string) string {
	switch state {
	case "skipped":
		return "skipped; initial fresh review approved this revision"
	case "verified":
		return "checks passed"
	case "completed":
		return "finished"
	case "running":
		return "in progress (recorded)"
	case "uncertain":
		return "needs inspection; outcome uncertain"
	case "checks_failed":
		return "checks failed"
	case "empty_result":
		return "no required review result"
	case "not_started", "pending":
		return "not started"
	default:
		return guidedLine(strings.ReplaceAll(strings.ReplaceAll(state, "_", " "), "-", " "))
	}
}
