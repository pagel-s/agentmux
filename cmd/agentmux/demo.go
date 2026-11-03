package main

import (
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
	"strings"
	"syscall"
	"time"

	"agentmux/internal/workflow"
)

const demoUsage = `Usage: amx demo [--output NEW_DIR]
Try a real checked workflow without models, accounts, tmux or Python.

Creates a private throwaway Git repository, runs a deterministic command,
checks the retained change, and applies it only to that fixture repository.
Your project and Agentmux workspaces are not used. Requires Git and POSIX sh.
NEW_DIR must not exist; the default is a new directory in the system temp area.
Reports and the fixture remain available after success, failure or interruption.
This fixture demonstrates workflow mechanics, not AI quality or security isolation.`

const demoOriginal = "Agentmux demo: starting file.\n"
const demoChanged = "Agentmux demo: checked change applied.\n"

type demoRecord struct {
	State      string `json:"state"`
	Phase      string `json:"phase"`
	Repository string `json:"repository"`
	Run        string `json:"run"`
	Patch      string `json:"patch,omitempty"`
	Applied    bool   `json:"applied"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
}

func runDemo(args []string, out, diagnostics io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runDemoIO(ctx, args, out, diagnostics)
}

func runDemoIO(ctx context.Context, args []string, out, diagnostics io.Writer) int {
	out = workflowDiagnosticWriter{out}
	diagnostics = workflowDiagnosticWriter{diagnostics}
	if len(args) == 1 && helpArgument(args[0]) {
		fmt.Fprintln(out, demoUsage)
		return exitSuccess
	}
	flags := flag.NewFlagSet("demo", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	flags.Usage = func() { fmt.Fprintln(out, demoUsage) }
	output := flags.String("output", "", "new private directory for fixture and evidence")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitSuccess
		}
		return exitValidation
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(diagnostics, "demo: use amx demo [--output NEW_DIR]")
		return exitValidation
	}
	git, err := exec.LookPath("git")
	if err != nil {
		fmt.Fprintln(diagnostics, "demo: Git is required; install Git and retry")
		return exitDependency
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		fmt.Fprintln(diagnostics, "demo: a POSIX shell (sh) is required")
		return exitDependency
	}
	root, err := newDemoDirectory(*output)
	if err != nil {
		fmt.Fprintf(diagnostics, "demo: create new output directory: %q\n", err)
		return exitValidation
	}
	fmt.Fprintf(out, "Deterministic workflow fixture; no model calls.\nArtifacts: %s\n", root)
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := executeDemo(ctx, root, git, shell, out); err != nil {
		fmt.Fprintf(diagnostics, "demo: %q\nRetained diagnostics: %s\n", err, filepath.Join(root, "demo.json"))
		return exitRuntime
	}
	return exitSuccess
}

func newDemoDirectory(output string) (string, error) {
	var root string
	var err error
	if output == "" {
		root, err = os.MkdirTemp("", "agentmux-demo-")
	} else {
		root, err = filepath.Abs(output)
		if err == nil {
			err = os.Mkdir(root, 0700)
		}
	}
	if err != nil {
		return "", err
	}
	// macOS system temp paths can have a symlinked ancestor. Workflow evidence
	// records canonical paths, and no cleanup ever removes a caller-selected path.
	return filepath.EvalSymlinks(root)
}

func executeDemo(ctx context.Context, root, git, shell string, out io.Writer) (runErr error) {
	record := demoRecord{State: "running", Phase: "prepare", Repository: filepath.Join(root, "repository"), Run: filepath.Join(root, "run"), StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := writeDemoRecord(root, record); err != nil {
		return err
	}
	defer func() {
		if runErr == nil && record.State == "completed" {
			return
		}
		record.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		record.State = "completed"
		if runErr != nil {
			record.State, record.Error = "failed", runErr.Error()
			if ctx.Err() != nil {
				record.State = "interrupted"
			}
		}
		runErr = errors.Join(runErr, writeDemoRecord(root, record))
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	home := filepath.Join(root, "home")
	for _, dir := range []string{record.Repository, home} {
		if err := os.Mkdir(dir, 0700); err != nil {
			return err
		}
	}
	emptyConfig := filepath.Join(home, "empty")
	if err := os.WriteFile(emptyConfig, nil, 0600); err != nil {
		return err
	}
	gitEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_AUTHOR_NAME=Agentmux Demo", "GIT_AUTHOR_EMAIL=demo@localhost", "GIT_COMMITTER_NAME=Agentmux Demo", "GIT_COMMITTER_EMAIL=demo@localhost"}
	gitRun := func(args ...string) (string, error) {
		command := exec.CommandContext(ctx, git, append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-C", record.Repository}, args...)...)
		command.Env = gitEnv
		command.WaitDelay = time.Second
		data, err := command.CombinedOutput()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			return "", fmt.Errorf("fixture git %s: %w: %s", args[0], err, strings.TrimSpace(string(data)))
		}
		return strings.TrimSpace(string(data)), nil
	}
	if _, err := gitRun("init", "--quiet", "--template="); err != nil {
		return err
	}
	// Local settings keep fixture selection independent of owner ignore rules,
	// line-ending preferences and hooks without changing any owner configuration.
	for key, value := range map[string]string{"core.excludesFile": emptyConfig, "core.attributesFile": emptyConfig, "core.autocrlf": "false", "core.hooksPath": os.DevNull, "commit.gpgsign": "false", "maintenance.auto": "false", "gc.auto": "0"} {
		if _, err := gitRun("config", "--local", key, value); err != nil {
			return err
		}
	}
	file := filepath.Join(record.Repository, "hello.txt")
	if err := os.WriteFile(file, []byte(demoOriginal), 0600); err != nil {
		return err
	}
	if _, err := gitRun("add", "--", "hello.txt"); err != nil {
		return err
	}
	if _, err := gitRun("commit", "--quiet", "-m", "Prepare deterministic Agentmux demo"); err != nil {
		return err
	}
	head, err := gitRun("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	record.Phase = "run-and-check"
	if err := writeDemoRecord(root, record); err != nil {
		return err
	}
	plan := workflow.Plan{Version: 1, Storage: workflow.StoragePrivate, Execution: "local", Mode: "compare", Repository: record.Repository, Goal: "Deterministically change hello.txt and verify it; no AI model is used.", TimeoutSeconds: 30, MaxAttempts: 1, MaxParallel: 1,
		Candidates: []workflow.Candidate{{ID: "fixture", Harness: "command", Command: []string{shell, "-c", "printf '%s\\n' 'Agentmux demo: checked change applied.' > hello.txt"}, Env: map[string]string{"HOME": home, "XDG_CONFIG_HOME": home, "XDG_STATE_HOME": home}, TimeoutSeconds: 10}},
		Checks:     []workflow.Check{{Name: "expected fixture text", Command: []string{shell, "-c", "test -f hello.txt && test ! -L hello.txt && { IFS= read -r line && test \"$line\" = 'Agentmux demo: checked change applied.' && ! IFS= read -r extra && test -z \"$extra\"; } < hello.txt"}, TimeoutSeconds: 10}},
	}
	report, err := workflow.Run(ctx, plan, record.Run, false)
	if err != nil {
		return fmt.Errorf("run deterministic workflow: %w", err)
	}
	if report.State != "completed" || len(report.Candidates) != 1 || report.Candidates[0].State != "verified" || report.Candidates[0].Patch == "" {
		return errors.New("workflow did not retain one verified fixture change")
	}
	record.Patch = report.Candidates[0].Patch
	if err := verifyDemoFile(file, demoOriginal); err != nil {
		return fmt.Errorf("source preservation before apply: %w", err)
	}
	if status, err := gitRun("status", "--porcelain"); err != nil || status != "" {
		return errors.Join(errors.New("fixture source changed before explicit application"), err)
	}
	record.Phase = "apply-to-fixture"
	if err := writeDemoRecord(root, record); err != nil {
		return err
	}
	fmt.Fprintln(out, "Verified retained change. Applying it only to the throwaway fixture repository.")
	applied, err := workflow.Apply(ctx, record.Run, "fixture")
	if err != nil {
		return fmt.Errorf("apply to fixture: %w", err)
	}
	if !applied.Applied || applied.Uncertain {
		return errors.New("fixture application did not confirm completion")
	}
	record.Applied, record.Phase = true, "verify-applied-file"
	if err := verifyDemoFile(file, demoChanged); err != nil {
		return err
	}
	if after, err := gitRun("rev-parse", "HEAD"); err != nil || after != head {
		return errors.Join(errors.New("fixture HEAD changed unexpectedly"), err)
	}
	if _, err := gitRun("diff", "--cached", "--quiet"); err != nil {
		return fmt.Errorf("fixture index changed unexpectedly: %w", err)
	}
	record.Phase = "finished"
	record.State, record.FinishedAt = "completed", time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeDemoRecord(root, record); err != nil {
		return err
	}
	fmt.Fprintf(out, "Success: checked patch applied; fixture file independently verified.\nFixture: %s\nReport: %s\nHTML: %s\nPatch: %s\n\nInspect the saved result:\n  amx flow status %s\n  amx flow explain %s\n\nFor a real coding task: amx work --help\nThis deterministic fixture is not evidence of AI quality or security isolation.\n", record.Repository, filepath.Join(record.Run, "report.json"), filepath.Join(record.Run, "report.html"), record.Patch, shellQuote(record.Run), shellQuote(record.Run))
	return nil
}

func verifyDemoFile(path, expected string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != int64(len(expected)) {
		return errors.New("fixture file type or size differs from the expected result")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(data) != expected {
		return errors.New("fixture file bytes differ from the expected result")
	}
	return nil
}

func writeDemoRecord(root string, record demoRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "demo.json"), append(data, '\n'), 0600)
}
