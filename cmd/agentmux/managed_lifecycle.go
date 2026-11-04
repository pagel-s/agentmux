package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedcheck"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
)

func loadManagedConfig(path string) (managedconfig.Config, error) {
	path, err := filepath.Abs(expandUserPath(path))
	if err != nil {
		return managedconfig.Config{}, err
	}
	cfg, err := managedconfig.Load(path)
	if err != nil {
		return cfg, err
	}
	for id, agent := range cfg.Agents {
		resolve := func(value string) string {
			value = expandUserPath(value)
			if value != "" && !filepath.IsAbs(value) {
				value = filepath.Join(filepath.Dir(path), value)
			}
			return value
		}
		agent.Workdir = resolve(agent.Workdir)
		agent.Auth.File = resolve(agent.Auth.File)
		for i, value := range agent.ReadOnlyPaths {
			agent.ReadOnlyPaths[i] = resolve(value)
		}
		cfg.Agents[id] = agent
	}
	for id, agent := range cfg.Spawn.Templates {
		if agent.Auth.File != "" {
			agent.Auth.File = expandUserPath(agent.Auth.File)
			if !filepath.IsAbs(agent.Auth.File) {
				agent.Auth.File = filepath.Join(filepath.Dir(path), agent.Auth.File)
			}
		}
		for i, value := range agent.ReadOnlyPaths {
			value = expandUserPath(value)
			if !filepath.IsAbs(value) {
				value = filepath.Join(filepath.Dir(path), value)
			}
			agent.ReadOnlyPaths[i] = value
		}
		cfg.Spawn.Templates[id] = agent
	}
	return cfg, nil
}

func runManagedLifecycle(operation string, options managedCLIOptions, state string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	executable, err := os.Executable()
	if err != nil {
		return managedCLIError(stdout, operation, "", exitRuntime, err)
	}
	var cfg managedconfig.Config
	mode := options.values["broker-sandbox"]
	if operation == "up" || operation == "doctor" {
		cfg, err = loadManagedConfig(options.positionals[0])
		if err != nil {
			return managedCLIError(stdout, operation, "", exitConfigParse, err)
		}
		if state == "" {
			state, err = filepath.Abs(filepath.Join(".agentmux-state", cfg.Session))
			if err != nil {
				return managedCLIError(stdout, operation, "", exitValidation, err)
			}
		}
	} else if operation == "restart" {
		store, err := managed.Open(state)
		if err != nil {
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
		cfg = store.Config()
		_ = store.Close()
		if mode == "" {
			if runtime, err := managedlaunch.Snapshot(state); err == nil {
				mode = runtime.BrokerSandbox
			}
		}
	}
	if mode == "" {
		mode = "local"
	}
	if operation == "up" || operation == "restart" || operation == "doctor" {
		report := managedcheck.Check(ctx, cfg, managedcheck.Options{Executable: executable, StateDir: state, BrokerSandbox: mode, CheckAuth: true})
		check := managedcheck.CheckResult{Name: "tmux.binary", Status: managedcheck.StatusOK, Message: "tmux is available for the dedicated managed session"}
		if _, err := exec.LookPath("tmux"); err != nil {
			check.Status, check.Message = managedcheck.StatusError, "tmux is required for managed startup and is unavailable on PATH"
			report.OK = false
		}
		report.Checks = append(report.Checks, check)
		sort.SliceStable(report.Checks, func(i, j int) bool {
			if report.Checks[i].Agent != report.Checks[j].Agent {
				return report.Checks[i].Agent < report.Checks[j].Agent
			}
			return report.Checks[i].Name < report.Checks[j].Name
		})
		if operation == "doctor" || !report.OK {
			envelope := managedEnvelope{Operation: operation, OK: report.OK, Result: report}
			code := exitSuccess
			if !report.OK {
				code = exitValidation
				envelope.Error = &managed.Fault{Code: "preflight", Message: "managed preflight found blocking issues"}
			}
			if writeManagedEnvelope(stdout, envelope) != exitSuccess {
				return exitRuntime
			}
			return code
		}
	}
	launcher, err := managedlaunch.New(managedlaunch.Options{StateDir: state, Executable: executable, BrokerSandbox: mode})
	if err != nil {
		return managedCLIError(stdout, operation, "", exitValidation, err)
	}
	if operation == "supervise" {
		if err := launcher.Supervise(ctx, options.values["attempt"]); err != nil {
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
		return writeManagedEnvelope(stdout, managedEnvelope{Operation: operation, OK: true, Result: map[string]bool{"stopped": true}})
	}
	var runtime managedlaunch.Runtime
	if operation == "down" || operation == "restart" {
		shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		runtime, err = launcher.Down(shutdownCtx)
		cancel()
		if err != nil {
			printManagedShutdownRecovery(stderr, executable, state, operation)
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
	}
	if operation == "up" || operation == "restart" {
		runtime, err = launcher.Up(ctx, cfg)
		if err != nil {
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
	}
	if operation == "up" || operation == "restart" {
		if err := rememberWorkState(state); err != nil {
			fmt.Fprintln(stderr, "Workspace started, but could not update workspace inventory:", err)
		}
	}
	if code := writeManagedEnvelope(stdout, managedEnvelope{Operation: operation, OK: true, Result: runtime}); code != exitSuccess {
		return code
	}
	if options.attach {
		if err := launcher.Attach(ctx, runtime, stdin, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "managed run started, but tmux attachment failed; use the returned attach command")
			return exitRuntime
		}
	}
	return exitSuccess
}

func printManagedShutdownRecovery(out io.Writer, executable, state, operation string) {
	command := func(args ...string) string {
		return managedlaunch.ShellCommand(append([]string{executable}, args...))
	}
	fmt.Fprintln(out, "Shutdown could not be confirmed. Broker and native terminal availability are unconfirmed.")
	fmt.Fprintln(out, "Inspect this exact workspace:", command("managed", "status", "--state", state))
	fmt.Fprintln(out, "If owned native terminals are still running, open them with:", command("attach", "--state", state))
	fmt.Fprintln(out, "Choose each affected owned terminal and finish or exit its native CLI normally. Closing the workbench or detaching does not exit a native CLI.")
	fmt.Fprintln(out, "Then retry the ordered shutdown:", command("managed", "down", "--state", state))
	if operation == "restart" {
		fmt.Fprintln(out, "Only after shutdown succeeds, retry:", command("managed", "restart", "--state", state))
	}
}

func watchManagedStatus(state string, out io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	executable, err := os.Executable()
	if err != nil {
		return managedCLIError(out, "status", "", exitRuntime, err)
	}
	launcher, err := managedlaunch.New(managedlaunch.Options{StateDir: state, Executable: executable})
	if err != nil {
		return managedCLIError(out, "status", "", exitValidation, err)
	}
	for {
		runtime, runtimeErr := launcher.Describe(ctx)
		if runtimeErr != nil {
			runtime = managedlaunch.Runtime{State: "unavailable", Agents: map[string]managedlaunch.AgentPane{}}
		}
		var counts map[string]any
		if credential, err := managed.LoadCredential(managed.CredentialPath(state, "")); err == nil {
			queryCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
			raw, problem := managed.NewClient(credential).Call(queryCtx, managed.Request{Operation: "status", Args: json.RawMessage(`{}`)})
			cancel()
			if problem == nil {
				_ = json.Unmarshal(raw, &counts)
				if runtime.Session == "" {
					runtime.Session, _ = counts["session"].(string)
					runtime.State = "running"
				}
			}
		}
		if _, err := fmt.Fprint(out, "\x1b[H\x1b[2J", managedlaunch.FormatStatus(runtime, counts)); err != nil {
			return exitRuntime
		}
		select {
		case <-ctx.Done():
			return exitSuccess
		case <-time.After(time.Second):
		}
	}
}
