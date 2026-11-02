package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"agentmux/internal/managed"
	"agentmux/internal/workflow"
)

const workflowUsage = `Usage: amx flow run PLAN.json --output DIR [--repo DIR] [--allow-models]
       amx flow mission PLAN.json --output DIR [--repo DIR] [--allow-models]
       amx flow watch PLAN.json --output DIR [--repo DIR] [--interval 10s] [--max-runs 10] [--duration 1h] [--allow-models]
       amx flow lab PLAN.json --output DIR [--repo DIR] [--repetitions 3] [--allow-models]
       amx flow check --repo DIR --checks CHECKS.json --output FILE [--containment pid-namespace]
       amx flow status RUN_DIR
       amx flow explain RUN_DIR
       amx flow report RUN_DIR --output FILE.html
       amx flow apply RUN_DIR [--candidate ID]
       amx flow recover RUN_DIR [--candidate ID]
       amx flow handoff RUN_DIR [--candidate ID] --output FILE
           [--state DIR --to ID --request-id ID]
       amx flow handoff RUN_DIR --brief SAVED_FILE --state DIR --to ID --request-id ID

Plans explicitly choose execution: "local". Worktrees separate changes, but
commands and provider tools retain trusted host authority. --allow-models permits
provider-backed turns and their charges. Checks run owner-configured argv.
Outputs are private and never replace existing files. Runs retain patches;
only an explicit apply command integrates a selected result.
Handoff creates a reviewed brief; --to queues an instruction through the existing
broker without changing the receiver's permissions or delivery mode.
See docs/workflows.md and examples/workflows/.`

func runWorkflow(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWorkflowIO(ctx, args, os.Stdout, os.Stderr)
}

func runWorkflowIO(ctx context.Context, args []string, out, diagnostics io.Writer) int {
	diagnostics = workflowDiagnosticWriter{diagnostics}
	fail := func(code int, err error) int {
		fmt.Fprintf(diagnostics, "flow: %q\n", err.Error())
		return code
	}
	if len(args) == 0 {
		fmt.Fprintln(diagnostics, workflowUsage)
		return exitValidation
	}
	if len(args) == 1 && helpArgument(args[0]) || len(args) == 2 && helpArgument(args[1]) {
		fmt.Fprintln(out, workflowUsage)
		return exitSuccess
	}
	switch args[0] {
	case "run", "mission", "watch", "lab":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return fail(exitValidation, errors.New("provide one JSON plan path before flags; see amx flow --help"))
		}
		operation := args[0]
		flags := flag.NewFlagSet("flow "+operation, flag.ContinueOnError)
		flags.SetOutput(diagnostics)
		output := flags.String("output", "", "new private result directory")
		repository := flags.String("repo", "", "override the plan's Git repository")
		allowModels := flags.Bool("allow-models", false, "allow native provider calls and their charges")
		var interval, duration time.Duration
		var maxRuns, repetitions int
		if operation == "watch" {
			flags.DurationVar(&interval, "interval", 10*time.Second, "poll interval (whole seconds)")
			flags.DurationVar(&duration, "duration", time.Hour, "maximum watch duration (whole seconds)")
			flags.IntVar(&maxRuns, "max-runs", 10, "maximum changed-snapshot reviews")
		}
		if operation == "lab" {
			flags.IntVar(&repetitions, "repetitions", 3, "bounded number of repeated comparisons")
		}
		if err := flags.Parse(args[2:]); err != nil {
			return fail(exitValidation, err)
		}
		if flags.NArg() != 0 || *output == "" {
			return fail(exitValidation, errors.New("provide --output DIR and no extra positional arguments"))
		}
		plan, err := workflow.LoadPlan(args[1])
		if err != nil {
			return fail(exitConfigParse, err)
		}
		if *repository != "" {
			plan.Repository = *repository
		}
		if operation == "mission" && plan.Mode != "mission" {
			return fail(exitValidation, errors.New("mission requires a plan with mode: mission"))
		}
		if operation != "mission" && plan.Mode == "mission" {
			return fail(exitValidation, errors.New("use amx flow mission for a mission plan"))
		}
		if operation == "watch" && (interval < time.Second || interval%time.Second != 0 || interval > time.Hour || duration < time.Second || duration%time.Second != 0 || duration > 24*time.Hour || maxRuns < 1 || maxRuns > 100) {
			return fail(exitValidation, errors.New("watch interval requires whole seconds within 1s–1h, duration within 1s–24h, and max-runs 1–100"))
		}
		if operation == "lab" && (repetitions < 1 || repetitions > 20) {
			return fail(exitValidation, errors.New("repetitions must be 1–20"))
		}
		fmt.Fprintln(diagnostics, "Execution: trusted local host. Source changes stay separate; results are retained for inspection.")
		var result any
		switch operation {
		case "run":
			result, err = workflow.Run(ctx, plan, *output, *allowModels)
		case "mission":
			result, err = workflow.RunMission(ctx, plan, *output, *allowModels)
		case "watch":
			result, err = workflow.Watch(ctx, plan, *output, *allowModels, workflow.WatchOptions{IntervalSeconds: int(interval / time.Second), MaxRuns: maxRuns, TimeoutSeconds: int(duration / time.Second)})
		case "lab":
			result, err = workflow.Lab(ctx, plan, *output, *allowModels, repetitions)
		}
		if report, ok := result.(workflow.Report); ok && report.ID != "" {
			if _, renderErr := os.Stat(filepath.Join(report.Output, "report.html")); renderErr != nil {
				fmt.Fprintf(diagnostics, "HTML report unavailable: %q; JSON evidence remains at %q\n", renderErr.Error(), report.Output)
			} else {
				fmt.Fprintf(diagnostics, "Review report: %q\n", filepath.Join(report.Output, "report.html"))
			}
		}
		if encodeErr := encodeWorkflow(out, operation, result, err); encodeErr != nil {
			return fail(exitRuntime, encodeErr)
		}
		if err != nil {
			return fail(exitRuntime, err)
		}
		return exitSuccess
	case "check":
		flags := flag.NewFlagSet("flow check", flag.ContinueOnError)
		flags.SetOutput(diagnostics)
		repository := flags.String("repo", "", "Git repository or candidate worktree")
		checksFile := flags.String("checks", "", "JSON array of named checks with command argv")
		output := flags.String("output", "", "new private verification JSON file")
		containment := flags.String("containment", "process-group", "process-group or Linux pid-namespace containment")
		if err := flags.Parse(args[1:]); err != nil {
			return fail(exitValidation, err)
		}
		if flags.NArg() != 0 || *repository == "" || *checksFile == "" || *output == "" {
			return fail(exitValidation, errors.New("check requires --repo DIR --checks FILE --output FILE"))
		}
		if err := workflow.ValidateProcessContainment(*containment); err != nil {
			return fail(exitValidation, err)
		}
		var checks []workflow.Check
		if err := readWorkflowJSON(*checksFile, &checks); err != nil {
			return fail(exitConfigParse, err)
		}
		if err := validateWorkflowCheckOutput(ctx, *repository, *output); err != nil {
			return fail(exitValidation, err)
		}
		// Reserve output before running checks: refusing a pre-existing destination
		// must never execute owner commands as a side effect.
		file, err := newWorkflowOutput(*output)
		if err != nil {
			return fail(exitValidation, err)
		}
		defer file.Close()
		verification, err := workflow.VerifyWithContainment(ctx, *repository, checks, *containment)
		if writeErr := json.NewEncoder(file).Encode(verification); writeErr != nil {
			return fail(exitRuntime, writeErr)
		}
		if err == nil && !verification.Passed {
			err = errors.New("declared checks failed or changed the verified content; inspect the retained verification")
		}
		if encodeErr := encodeWorkflow(out, "check", verification, err); encodeErr != nil {
			return fail(exitRuntime, encodeErr)
		}
		if err != nil {
			return fail(exitRuntime, err)
		}
		return exitSuccess
	case "apply", "recover", "report":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return fail(exitValidation, errors.New("provide one run directory before flags"))
		}
		flags := flag.NewFlagSet("flow "+args[0], flag.ContinueOnError)
		flags.SetOutput(diagnostics)
		var candidate, output string
		if args[0] == "apply" || args[0] == "recover" {
			flags.StringVar(&candidate, "candidate", "", "exact candidate result")
		} else {
			flags.StringVar(&output, "output", "", "new private HTML report")
		}
		if err := flags.Parse(args[2:]); err != nil {
			return fail(exitValidation, err)
		}
		if flags.NArg() != 0 || args[0] == "report" && output == "" {
			return fail(exitValidation, errors.New("unexpected arguments or missing --output FILE"))
		}
		if args[0] == "report" {
			if err := writeWorkflowReport(args[1], output); err != nil {
				return fail(exitRuntime, err)
			}
			if err := encodeWorkflow(out, "report", map[string]string{"report": output}, nil); err != nil {
				return fail(exitRuntime, err)
			}
		} else if args[0] == "recover" {
			result, err := workflow.RecoverApply(ctx, args[1], candidate)
			if encodeErr := encodeWorkflow(out, "recover", result, err); encodeErr != nil {
				return fail(exitRuntime, encodeErr)
			}
			if err != nil {
				return fail(exitRuntime, err)
			}
		} else {
			result, err := workflow.Apply(ctx, args[1], candidate)
			if encodeErr := encodeWorkflow(out, "apply", result, err); encodeErr != nil {
				return fail(exitRuntime, encodeErr)
			}
			if err != nil {
				return fail(exitRuntime, err)
			}
		}
		return exitSuccess
	case "status", "explain":
		if len(args) != 2 || strings.HasPrefix(args[1], "-") {
			return fail(exitValidation, errors.New("status/explain requires exactly one run directory"))
		}
		var result any
		var err error
		if args[0] == "status" {
			result, err = loadWorkflowStatus(args[1])
		} else {
			result, err = workflow.Explain(args[1])
		}
		if err != nil {
			return fail(exitRuntime, err)
		}
		if err := encodeWorkflow(out, args[0], result, nil); err != nil {
			return fail(exitRuntime, err)
		}
		return exitSuccess
	case "handoff":
		return runWorkflowHandoff(ctx, args[1:], out, diagnostics)
	default:
		return fail(exitValidation, fmt.Errorf("unknown operation %q; see amx flow --help", args[0]))
	}
}

func encodeWorkflow(out io.Writer, operation string, result any, err error) error {
	envelope := map[string]any{"schema_version": 1, "kind": "workflow", "operation": operation, "ok": err == nil, "result": result}
	if err != nil {
		envelope["error"] = err.Error()
	}
	data, encodeErr := json.Marshal(envelope)
	if encodeErr != nil {
		return encodeErr
	}
	var safe strings.Builder
	for _, r := range string(data) {
		if r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			fmt.Fprintf(&safe, "\\u%04x", r)
		} else {
			safe.WriteRune(r)
		}
	}
	safe.WriteByte('\n')
	n, writeErr := io.WriteString(out, safe.String())
	if writeErr != nil {
		return writeErr
	}
	if n != safe.Len() {
		return io.ErrShortWrite
	}
	return nil
}

func loadWorkflowStatus(directory string) (any, error) {
	if _, err := os.Lstat(filepath.Join(directory, "report.json")); err == nil || !os.IsNotExist(err) {
		return workflow.LoadReport(directory)
	}
	if _, err := os.Lstat(filepath.Join(directory, "watch.json")); err == nil {
		var report workflow.WatchReport
		if err := workflow.DecodeFile(filepath.Join(directory, "watch.json"), &report); err != nil {
			return nil, err
		}
		if err := validateWorkflowAggregate(report.Version, report.State, report.Repository, report.Output, report.StartedAt, report.FinishedAt, "watching"); err != nil {
			return nil, errors.New("invalid watch report")
		}
		if report.Options.IntervalSeconds < 1 || report.Options.IntervalSeconds > 3600 || report.Options.MaxRuns < 1 || report.Options.MaxRuns > 100 || report.Options.TimeoutSeconds < 1 || report.Options.TimeoutSeconds > 86400 || len(report.Runs) > report.Options.MaxRuns {
			return nil, errors.New("invalid watch report bounds")
		}
		return report, nil
	}
	var report workflow.LabReport
	if err := workflow.DecodeFile(filepath.Join(directory, "lab.json"), &report); err != nil {
		return nil, err
	}
	if err := validateWorkflowAggregate(report.Version, report.State, report.Repository, report.Output, report.StartedAt, report.FinishedAt, "running"); err != nil {
		return nil, errors.New("invalid lab report")
	}
	if report.Repetitions < 1 || report.Repetitions > 20 || len(report.Runs) > report.Repetitions || report.State == "completed" && len(report.Runs) != report.Repetitions {
		return nil, errors.New("invalid lab report repetitions")
	}
	return report, nil
}

func validateWorkflowAggregate(version int, state, repository, output, started, finished, activeState string) error {
	if version != 1 || !filepath.IsAbs(repository) || !filepath.IsAbs(output) {
		return errors.New("invalid aggregate report identity")
	}
	switch state {
	case activeState, "completed", "failed", "interrupted":
	default:
		return errors.New("unknown aggregate report state")
	}
	start, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return err
	}
	if state == activeState {
		if finished != "" {
			return errors.New("active report has a finish timestamp")
		}
		return nil
	}
	end, err := time.Parse(time.RFC3339Nano, finished)
	if err != nil {
		return err
	}
	if end.Before(start) {
		return errors.New("report ends before it starts")
	}
	return nil
}

func readWorkflowJSON(path string, target any) error {
	data, err := workflow.ReadRegularFile(path, 1<<20)
	if err != nil {
		return err
	}
	return workflow.DecodeJSON(data, target)
}

func newWorkflowOutput(path string) (*os.File, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("output path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, fmt.Errorf("output parent must exist: %w", err)
	}
	return os.OpenFile(filepath.Join(parent, filepath.Base(abs)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

func validateWorkflowCheckOutput(ctx context.Context, repository, output string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repository, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "rev-parse", "--show-toplevel")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("locate checked Git repository: %w", err)
	}
	root, err := filepath.EvalSymlinks(strings.TrimSuffix(string(data), "\n"))
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, filepath.Join(parent, filepath.Base(abs)))
	if err != nil {
		return err
	}
	if relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("verification output must be outside the checked Git repository so saving it cannot invalidate its own evidence")
	}
	return nil
}

func writeWorkflowReport(directory, output string) error {
	_, err := workflow.RenderReportTo(directory, output)
	return err
}

func runWorkflowHandoff(ctx context.Context, args []string, out, diagnostics io.Writer) int {
	diagnostics = workflowDiagnosticWriter{diagnostics}
	fail := func(code int, err error) int {
		fmt.Fprintf(diagnostics, "flow handoff: %q\n", err.Error())
		return code
	}
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return fail(exitValidation, errors.New("provide a run directory followed by --output FILE"))
	}
	flags := flag.NewFlagSet("flow handoff", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	candidate := flags.String("candidate", "", "exact candidate ID (required when ambiguous)")
	briefFile := flags.String("brief", "", "send the exact bytes of a previously saved brief")
	output := flags.String("output", "", "new private handoff brief")
	state := flags.String("state", "", "existing owner-controlled broker state")
	to := flags.String("to", "", "recipient's stable agent ID")
	requestID := flags.String("request-id", "", "stable identity used to safely retry a broker instruction")
	if err := flags.Parse(args[1:]); err != nil {
		return fail(exitValidation, err)
	}
	if flags.NArg() != 0 || *output == "" && *briefFile == "" || *briefFile != "" && (*output != "" || *candidate != "") {
		return fail(exitValidation, errors.New("choose --output FILE or --brief SAVED_FILE, with no extra positional arguments"))
	}
	send := *state != "" || *to != "" || *requestID != ""
	if send && (*state == "" || !utf8.ValidString(*to) || strings.TrimSpace(*to) == "" || !utf8.ValidString(*requestID) || len(*requestID) == 0 || len(*requestID) > 128 || strings.IndexFunc(*requestID, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) >= 0) {
		return fail(exitValidation, errors.New("delivery requires --state DIR --to ID --request-id ID (1–128 non-whitespace bytes)"))
	}
	if *briefFile != "" && !send {
		return fail(exitValidation, errors.New("--brief requires --state DIR --to ID --request-id ID"))
	}
	var brief string
	var err error
	if *briefFile != "" {
		brief, err = readWorkflowBrief(*briefFile)
	} else {
		brief, err = workflow.Handoff(args[0], *candidate)
	}
	if err != nil {
		return fail(exitRuntime, err)
	}
	if *briefFile == "" {
		if err := workflow.ValidateReportOutput(args[0], *output); err != nil {
			return fail(exitValidation, err)
		}
		file, err := newWorkflowOutput(*output)
		if err != nil {
			return fail(exitValidation, err)
		}
		if _, err = io.Copy(file, bytes.NewBufferString(brief)); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return fail(exitRuntime, err)
		}
	} else {
		*output = *briefFile
	}
	result := map[string]any{"brief": *output, "delivered": false}
	if send {
		result["request_id"] = *requestID
		credential, credentialErr := managed.LoadCredential(managed.CredentialPath(*state, ""))
		if credentialErr != nil {
			err = credentialErr
		} else if credential.AgentID != "$owner" {
			err = errors.New("handoff delivery requires the workspace owner's credential")
		} else {
			payload, _ := json.Marshal(map[string]any{"to": []string{*to}, "text": brief})
			response, problem := managed.NewClient(credential).Call(ctx, managed.Request{Operation: "instruct", RequestID: *requestID, Args: payload})
			if problem != nil {
				err = fmt.Errorf("%w; brief retained at %s; if acceptance is unknown, retry with --brief %s and request-id %s", problem, *output, *output, *requestID)
			} else {
				result["delivered"] = true
				result["broker_result"] = response
			}
		}
	}
	if encodeErr := encodeWorkflow(out, "handoff", result, err); encodeErr != nil {
		return fail(exitRuntime, encodeErr)
	}
	if err != nil {
		return fail(exitRuntime, err)
	}
	return exitSuccess
}

func readWorkflowBrief(path string) (string, error) {
	data, err := workflow.ReadRegularFile(path, 512<<10)
	if err != nil {
		return "", err
	}
	if len(data) == 0 || strings.TrimSpace(string(data)) == "" || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", errors.New("saved brief must contain nonempty UTF-8 text without NUL")
	}
	return string(data), nil
}

// flag.FlagSet writes invalid user arguments before returning an error. Escape
// terminal control sequences there as well as in the later quoted diagnostic.
type workflowDiagnosticWriter struct{ io.Writer }

func (w workflowDiagnosticWriter) Write(p []byte) (int, error) {
	var safe strings.Builder
	for _, r := range string(p) {
		if r == '\n' || r == '\t' || unicode.IsPrint(r) {
			safe.WriteRune(r)
		} else {
			quoted := strconv.QuoteRuneToASCII(r)
			safe.WriteString(quoted[1 : len(quoted)-1])
		}
	}
	n, err := io.WriteString(w.Writer, safe.String())
	if err != nil {
		return 0, err
	}
	if n != safe.Len() {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}
