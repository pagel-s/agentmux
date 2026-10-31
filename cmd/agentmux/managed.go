package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/sandbox"
)

const managedCLIInputLimit = 4 * 1024 * 1024

type managedEnvelope struct {
	SchemaVersion int            `json:"schema_version"`
	Kind          string         `json:"kind"`
	Operation     string         `json:"operation"`
	OK            bool           `json:"ok"`
	RequestID     string         `json:"request_id,omitempty"`
	Result        any            `json:"result,omitempty"`
	Error         *managed.Fault `json:"error,omitempty"`
}

type managedCLIOptions struct {
	values      map[string]string
	positionals []string
	stdin       bool
	once        bool
	attach      bool
	watch       bool
}

func runManaged(args []string) int {
	return runManagedIO(args, os.Stdin, os.Stdout, os.Stderr)
}

func runManagedIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "trace" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runManagedTrace(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && (args[0] == "remote" || args[0] == "capsule") {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if args[0] == "remote" {
			return runManagedRemote(ctx, args[1:], stdout, stderr)
		}
		return runManagedCapsule(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "memory" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runManagedMemory(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "setup" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runManagedSetup(ctx, args[1:], stdout, stderr)
	}
	if len(args) == 0 {
		printManagedUsage(stderr)
		return managedCLIError(stdout, "help", "", exitValidation, errors.New("managed subcommand is required"))
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printManagedUsage(stdout)
		return exitSuccess
	}
	operation := args[0]
	for _, arg := range args[1:] {
		if arg == "--help" || arg == "-h" {
			printManagedUsage(stdout)
			return exitSuccess
		}
	}
	options, err := parseManagedCLI(operation, args[1:])
	if err != nil {
		return managedCLIError(stdout, operation, "", exitValidation, err)
	}
	state := options.values["state"]
	if state != "" {
		state, err = filepath.Abs(expandUserPath(state))
		if err != nil {
			return managedCLIError(stdout, operation, "", exitValidation, err)
		}
	}
	switch operation {
	case "up", "down", "restart", "supervise", "doctor":
		return runManagedLifecycle(operation, options, state, stdin, stdout, stderr)
	case "init":
		cfg, err := loadManagedConfig(options.positionals[0])
		if err != nil {
			return managedCLIError(stdout, operation, "", exitConfigParse, err)
		}
		store, err := managed.Init(state, cfg)
		if err != nil {
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
		if err := store.Close(); err != nil {
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
		return writeManagedEnvelope(stdout, managedEnvelope{Operation: operation, OK: true, Result: map[string]any{
			"state_dir": state, "session": cfg.Session, "agents": len(cfg.Agents),
			"owner_credential_file": managed.CredentialPath(state, ""), "socket": managed.SocketPath(state),
		}})
	case "serve":
		if options.values["sandbox"] == "bubblewrap" {
			return runManagedIsolatedBroker(stdout, stderr, state)
		}
		store, err := managed.Open(state)
		if err != nil {
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
		defer store.Close()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer stop()
		fmt.Fprintf(stderr, "Starting managed service at %s\n", managed.SocketPath(state))
		if err := managed.Serve(ctx, store); err != nil && !errors.Is(err, context.Canceled) {
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
		return writeManagedEnvelope(stdout, managedEnvelope{Operation: operation, OK: true, Result: map[string]any{"stopped": true}})
	case "worker":
		executable, err := os.Executable()
		if err != nil {
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer stop()
		fmt.Fprintf(stderr, "Managed worker %s: waiting for authorized tasks; use status or the broker API to inspect work.\n", options.values["agent"])
		if err := managed.RunWorker(ctx, managed.WorkerOptions{StateDir: state, AgentID: options.values["agent"], Once: options.once, Executable: executable, DefinitionFile: options.values["definition"]}); err != nil && !errors.Is(err, context.Canceled) {
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
		return writeManagedEnvelope(stdout, managedEnvelope{Operation: operation, OK: true, Result: map[string]any{"agent_id": options.values["agent"], "stopped": true}})
	case "rotate":
		store, err := managed.Open(state)
		if err != nil {
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
		defer store.Close()
		id := options.values["agent"]
		credential, err := store.Rotate(id)
		if err != nil {
			return managedCLIError(stdout, operation, "", exitRuntime, err)
		}
		return writeManagedEnvelope(stdout, managedEnvelope{Operation: operation, OK: true, Result: map[string]any{
			"agent_id": credential.AgentID, "generation": credential.Generation,
			"credential_file": managed.CredentialPath(state, credential.AgentID),
		}})
	case "status":
		if options.watch {
			return watchManagedStatus(state, stdout)
		}
		return callManagedCLI(stdout, managed.CredentialPath(state, ""), "", "status", "", json.RawMessage(`{}`))
	case "activity":
		if options.watch {
			return watchManagedActivity(state, stdout)
		}
		return callManagedCLI(stdout, managed.CredentialPath(state, ""), "", "activity", "", json.RawMessage(`{"limit":20}`))
	case "call":
		credentialFile := options.values["credential-file"]
		if credentialFile == "" {
			credentialFile = os.Getenv("AGENTMUX_CREDENTIAL_FILE")
		}
		if credentialFile == "" {
			return managedCLIError(stdout, operation, "", exitValidation, errors.New("--credential-file or AGENTMUX_CREDENTIAL_FILE is required"))
		}
		socket := options.values["socket"]
		if socket == "" {
			socket = os.Getenv("AGENTMUX_SOCKET")
		}
		var data []byte
		if options.stdin {
			data, err = io.ReadAll(io.LimitReader(stdin, managedCLIInputLimit+1))
		} else if value, ok := options.values["json"]; ok {
			data = []byte(value)
		} else {
			data = []byte(`{}`)
		}
		if err != nil {
			return managedCLIError(stdout, operation, "", exitValidation, errors.New("cannot read JSON arguments from stdin"))
		}
		if len(data) > managedCLIInputLimit {
			return managedCLIError(stdout, operation, "", exitValidation, errors.New("JSON arguments exceed the CLI input limit"))
		}
		data = []byte(strings.TrimSpace(string(data)))
		if len(data) == 0 || data[0] != '{' || !json.Valid(data) {
			return managedCLIError(stdout, operation, "", exitValidation, errors.New("arguments must be one JSON object"))
		}
		return callManagedCLI(stdout, credentialFile, socket, options.positionals[0], options.values["request-id"], data)
	}
	return managedCLIError(stdout, operation, "", exitValidation, errors.New("unsupported managed subcommand"))
}

func runManagedIsolatedBroker(stdout, stderr io.Writer, state string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := sandbox.Probe(ctx); err != nil {
		return managedCLIError(stdout, "serve", "", exitRuntime, err)
	}
	executable, err := os.Executable()
	if err != nil {
		return managedCLIError(stdout, "serve", "", exitRuntime, err)
	}
	spec, err := sandbox.WrapBroker(executable, state)
	if err != nil {
		return managedCLIError(stdout, "serve", "", exitRuntime, err)
	}
	cmd := exec.CommandContext(ctx, spec.Binary, spec.Args...)
	cmd.Env, cmd.Dir, cmd.Stderr = spec.Env, spec.Workdir, stderr
	cmd.WaitDelay = 2 * time.Second
	configureManagedBrokerProcess(cmd)
	// The child emits a single CLI result when it stops. Buffer that envelope
	// so a startup failure cannot produce two concatenated JSON responses.
	var output bytes.Buffer
	cmd.Stdout = &output
	err = cmd.Run()
	if ctx.Err() != nil {
		return writeManagedEnvelope(stdout, managedEnvelope{Operation: "serve", OK: true, Result: map[string]any{"stopped": true, "sandbox": "bubblewrap"}})
	}
	if json.Valid(output.Bytes()) {
		if _, copyErr := io.Copy(stdout, &output); copyErr != nil {
			return exitRuntime
		}
		if err == nil {
			return exitSuccess
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode()
		}
		return exitRuntime
	}
	if err != nil {
		return managedCLIError(stdout, "serve", "", exitRuntime, errors.New("isolated broker exited without a valid result; inspect stderr"))
	}
	return managedCLIError(stdout, "serve", "", exitRuntime, errors.New("isolated broker exited without a result"))
}

func callManagedCLI(stdout io.Writer, credentialFile, socket, operation, requestID string, args json.RawMessage) int {
	credential, err := managed.LoadCredential(expandUserPath(credentialFile))
	if err != nil {
		return managedCLIError(stdout, operation, requestID, exitRuntime, err)
	}
	if socket != "" {
		credential.Socket = expandUserPath(socket)
	}
	if requestID == "" {
		var random [24]byte
		if _, err := rand.Read(random[:]); err != nil {
			return managedCLIError(stdout, operation, "", exitRuntime, errors.New("cannot generate request identity"))
		}
		requestID = "cli_" + hex.EncodeToString(random[:])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, failure := managed.NewClient(credential).Call(ctx, managed.Request{Operation: operation, RequestID: requestID, Args: args})
	if failure != nil {
		code := exitRuntime
		if failure.Code == "invalid" || failure.Code == "unsupported" || failure.Code == "forbidden" || failure.Code == "unauthorized" {
			code = exitValidation
		}
		_ = writeManagedEnvelope(stdout, managedEnvelope{Operation: operation, RequestID: requestID, Error: failure})
		return code
	}
	return writeManagedEnvelope(stdout, managedEnvelope{Operation: operation, OK: true, RequestID: requestID, Result: result})
}

func managedCLIError(stdout io.Writer, operation, requestID string, code int, err error) int {
	kind := "runtime"
	if code == exitValidation || code == exitConfigParse {
		kind = "invalid"
	}
	_ = writeManagedEnvelope(stdout, managedEnvelope{Operation: operation, RequestID: requestID, Error: &managed.Fault{Code: kind, Message: err.Error()}})
	return code
}

func writeManagedEnvelope(stdout io.Writer, envelope managedEnvelope) int {
	envelope.SchemaVersion = 1
	envelope.Kind = "managed"
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(envelope); err != nil {
		return exitRuntime
	}
	return exitSuccess
}

// Parse flags before or after positionals; Go's flag package stops at the first
// positional, while the documented CLI accepts `init config --state dir`.
func parseManagedCLI(operation string, args []string) (managedCLIOptions, error) {
	allowed := map[string]bool{}
	positionals := 0
	switch operation {
	case "up", "doctor":
		allowed["state"], allowed["broker-sandbox"] = true, true
		allowed["attach"] = operation == "up"
		positionals = 1
	case "down":
		allowed["state"] = true
	case "restart":
		allowed["state"], allowed["broker-sandbox"], allowed["attach"] = true, true, true
	case "supervise":
		allowed["state"], allowed["attempt"] = true, true
	case "init":
		allowed["state"] = true
		positionals = 1
	case "serve":
		allowed["state"], allowed["sandbox"] = true, true
	case "status", "activity":
		allowed["state"], allowed["watch"] = true, true
	case "worker":
		allowed["state"], allowed["agent"], allowed["once"] = true, true, true
		allowed["definition"] = true
	case "rotate":
		allowed["state"], allowed["agent"] = true, true
	case "call":
		for _, name := range []string{"credential-file", "socket", "json", "stdin", "request-id"} {
			allowed[name] = true
		}
		positionals = 1
	default:
		return managedCLIOptions{}, errors.New("unsupported managed subcommand")
	}
	options := managedCLIOptions{values: map[string]string{}}
	seen := map[string]bool{}
	positionalOnly := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" && !positionalOnly {
			positionalOnly = true
			continue
		}
		if positionalOnly || !strings.HasPrefix(arg, "-") {
			options.positionals = append(options.positionals, arg)
			continue
		}
		name, value, inline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if !strings.HasPrefix(arg, "--") || !allowed[name] {
			return options, fmt.Errorf("unknown flag %s", strings.SplitN(arg, "=", 2)[0])
		}
		if seen[name] {
			return options, fmt.Errorf("flag --%s was provided more than once", name)
		}
		seen[name] = true
		if name == "stdin" || name == "once" || name == "attach" || name == "watch" {
			if inline {
				return options, fmt.Errorf("flag --%s does not take a value", name)
			}
			options.stdin = options.stdin || name == "stdin"
			options.once = options.once || name == "once"
			options.attach = options.attach || name == "attach"
			options.watch = options.watch || name == "watch"
			continue
		}
		if !inline {
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "--") {
				return options, fmt.Errorf("flag --%s requires a value", name)
			}
			value = args[i]
		}
		if value == "" {
			return options, fmt.Errorf("flag --%s requires a nonempty value", name)
		}
		options.values[name] = value
	}
	if len(options.positionals) != positionals {
		return options, fmt.Errorf("managed %s requires %d positional argument(s)", operation, positionals)
	}
	if operation != "call" && operation != "up" && operation != "doctor" && options.values["state"] == "" {
		return options, errors.New("--state is required")
	}
	if (operation == "worker" || operation == "rotate") && options.values["agent"] == "" {
		return options, errors.New("--agent is required (use '$owner' to rotate the owner credential)")
	}
	if mode := options.values["sandbox"]; mode != "" && mode != "local" && mode != "bubblewrap" {
		return options, errors.New("--sandbox must be local or bubblewrap")
	}
	if mode := options.values["broker-sandbox"]; mode != "" && mode != "local" && mode != "bubblewrap" {
		return options, errors.New("--broker-sandbox must be local or bubblewrap")
	}
	if operation == "supervise" && options.values["attempt"] == "" {
		return options, errors.New("supervisor requires an attempt identity")
	}
	if options.stdin && seen["json"] {
		return options, errors.New("use exactly one of --json or --stdin")
	}
	return options, nil
}

func printManagedUsage(out io.Writer) {
	fmt.Fprintln(out, `Managed runtime (opt-in v2 configuration):
  agentmux managed setup [--spec FILE] [--output FILE] [--init-git]
  agentmux managed up <config> [--state <private-dir>]
      [--broker-sandbox local|bubblewrap] [--attach]
  agentmux managed down --state <private-dir>
  agentmux managed restart --state <private-dir> [--attach]
  agentmux managed doctor <config> [--state <private-dir>]
  agentmux managed init <config> --state <private-dir>
  agentmux managed serve --state <private-dir> [--sandbox local|bubblewrap]
  agentmux managed call <operation> [--credential-file <path>] [--socket <path>]
      [--json <object> | --stdin] [--request-id <id>]
  agentmux managed worker --state <private-dir> --agent <id> [--once] [--definition FILE]
  agentmux managed remote --state DIR --agent ID --host SSH_ALIAS --remote-dir DIR
      [--layout FILE] [--binary FILE] [--once] [--resume]
  agentmux managed memory export|import|search|index|prune [--help]
  agentmux managed trace list|verify|show --state DIR --agent ID [--id ID] [--mac MAC]
  agentmux managed capsule inspect|recover --state DIR --agent ID [--help]
  agentmux managed status --state <private-dir> [--watch]
  agentmux managed activity --state <private-dir> [--watch]
  agentmux managed rotate --state <private-dir> --agent <id|'$owner'>

Call credentials may come from AGENTMUX_CREDENTIAL_FILE; AGENTMUX_SOCKET
overrides the credential's socket when --socket is omitted. Credentials are
never printed. Local workers are trusted processes, not sandbox isolation.
See docs/managed-runtime.md for setup, permissions, and examples.`)
}
