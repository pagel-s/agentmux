package managedcheck

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentmux/internal/harness"
	"agentmux/internal/managedconfig"
)

type metadataResult struct {
	helpOK  bool
	version string
	missing []string
}

var versionPattern = regexp.MustCompile(`\b[vV]?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9_.-]+)?\b`)

func inspectMetadata(ctx context.Context, agent managedconfig.Agent, binary string) metadataResult {
	spec := harness.Spec{Binary: binary, Env: metadataEnv(agent.Env)}
	spec.Args = metadataArgs(agent, "help")
	help, err := boundedMetadata(ctx, spec, true, true)
	result := metadataResult{helpOK: err == nil}
	if result.helpOK {
		for _, flag := range requiredFlags(agent.Harness) {
			if !strings.Contains(help, flag) {
				result.missing = append(result.missing, flag)
			}
		}
	}
	spec.Args = metadataArgs(agent, "version")
	version, err := boundedMetadata(ctx, spec, true, true)
	if err == nil {
		result.version = versionPattern.FindString(version)
	}
	if agent.Harness == "pi" && result.version != "" {
		major, _ := strconv.Atoi(strings.Split(strings.TrimLeft(result.version, "vV"), ".")[0])
		if major < 1 {
			result.missing = append(result.missing, "Pi 1.x RPC agent_settled completion")
		}
	}
	return result
}

func specPrefix(agent managedconfig.Agent) []string {
	if len(agent.Command) > 1 {
		return append([]string(nil), agent.Command[1:]...)
	}
	return nil
}

func metadataArgs(agent managedconfig.Agent, kind string) []string {
	args := specPrefix(agent)
	if kind == "version" {
		return append(args, "--version")
	}
	switch agent.Harness {
	case "claude":
		return append(args, "--safe-mode", "--help")
	case "codex":
		return append(args, "exec", "--help")
	case "pi":
		return append(args, "--no-session", "--offline", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-context-files", "--no-approve", "--help")
	default:
		return append(args, "--help")
	}
}

func requiredFlags(name string) []string {
	switch name {
	case "claude":
		return []string{"--safe-mode", "--print", "--input-format", "--output-format", "--verbose", "--no-session-persistence", "--permission-mode", "--model"}
	case "codex":
		return []string{"--json", "--color", "--sandbox", "--model"}
	case "antigravity":
		return []string{"--input-format", "--output-format", "--disable-slash-commands", "--model"}
	case "pi":
		return []string{"--mode", "rpc", "--no-session", "--provider", "--model"}
	}
	return nil
}

// Only runtime paths and locale reach help/version processes. Provider keys,
// broker credentials, and arbitrary agent environment variables do not.
func metadataEnv(agentEnv map[string]string) []string {
	values := map[string]string{"LANG": "C.UTF-8", "TERM": "dumb", "PI_OFFLINE": "1", "PI_TELEMETRY": "0"}
	for _, key := range []string{"PATH", "NODE_PATH", "PI_PACKAGE_DIR", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		value, ok := agentEnv[key]
		if !ok {
			value, ok = os.LookupEnv(key)
		}
		if ok {
			values[key] = value
		}
	}
	return envSlice(values)
}

func boundedCommand(ctx context.Context, spec harness.Spec, private bool) (string, error) {
	return boundedMetadata(ctx, spec, private, false)
}

func boundedMetadata(ctx context.Context, spec harness.Spec, private, helpDiagnostics bool) (string, error) {
	if private {
		dir, err := os.MkdirTemp("", "agentmux-metadata-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(dir)
		spec.Env = append(spec.Env,
			"HOME="+dir, "XDG_CONFIG_HOME="+filepath.Join(dir, ".config"),
			"XDG_CACHE_HOME="+filepath.Join(dir, ".cache"), "XDG_DATA_HOME="+filepath.Join(dir, ".local", "share"),
			"XDG_STATE_HOME="+filepath.Join(dir, ".local", "state"), "TMPDIR="+dir,
			"PI_CODING_AGENT_DIR="+filepath.Join(dir, "pi"), "CODEX_HOME="+filepath.Join(dir, "codex"),
		)
		spec.Workdir = dir
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, spec.Binary, spec.Args...)
	cmd.Env, cmd.Dir = spec.Env, spec.Workdir
	cmd.WaitDelay = 250 * time.Millisecond
	configureMetadataProcess(cmd)
	output := &metadataBuffer{}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	if helpDiagnostics {
		// Some CLIs (including agy) deliberately print --help to stderr.
		// Only isolated help/version calls use combined output. Auth stderr
		// is always discarded, and no raw help text enters the Report.
		cmd.Stderr = output
	}
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if output.overflow {
		return "", errors.New("metadata output exceeded its limit")
	}
	return string(output.data), err
}

type metadataBuffer struct {
	mu       sync.Mutex
	data     []byte
	overflow bool
}

func (b *metadataBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := 128*1024 - len(b.data)
	if len(p) > remaining {
		b.overflow = true
		b.data = append(b.data, p[:remaining]...)
	} else {
		b.data = append(b.data, p...)
	}
	return len(p), nil
}

func inspectAuth(ctx context.Context, agent managedconfig.Agent, binary string) (ready, verified bool) {
	args, ok := authArgs(agent)
	if !ok {
		return false, false
	}
	build, err := harness.Build(harness.Request{Harness: "command", Command: append([]string{binary}, args...), Env: agent.Env})
	if err != nil {
		return false, false
	}
	build.Env = append(build.Env, "PI_OFFLINE=1", "PI_TELEMETRY=0")
	// Work outside a project so auth metadata does not discover project hooks.
	dir, err := os.MkdirTemp("", "agentmux-auth-metadata-")
	if err != nil {
		return false, false
	}
	defer os.RemoveAll(dir)
	build.Workdir = dir
	text, err := boundedCommand(ctx, build, false)
	return authReadiness(agent.Harness, text, err)
}

func authArgs(agent managedconfig.Agent) ([]string, bool) {
	args := specPrefix(agent)
	switch agent.Harness {
	case "claude":
		args = append(args, "--safe-mode", "auth", "status", "--json")
	case "codex":
		args = append(args, "login", "status")
	case "pi":
		if agent.Model == "" {
			return nil, false
		}
		args = append(args, "auth", "check", "--json", "--no-refresh", "--model", agent.Model)
	default:
		// Antigravity currently has no verified local auth-status command.
		return nil, false
	}
	return args, true
}

func authReadiness(name, text string, err error) (ready, verified bool) {
	if name == "codex" {
		if err == nil {
			return true, true
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, true
		}
		return false, false
	}
	var state struct {
		LoggedIn      *bool  `json:"loggedIn"`
		Authenticated *bool  `json:"authenticated"`
		AuthMethod    string `json:"authMethod"`
		Status        string `json:"status"`
	}
	if json.Unmarshal([]byte(text), &state) != nil {
		return false, false
	}
	if state.LoggedIn != nil {
		return *state.LoggedIn, true
	}
	if state.Authenticated != nil {
		return *state.Authenticated, true
	}
	if state.AuthMethod != "" {
		switch state.AuthMethod {
		case "none":
			return false, true
		case "claude.ai", "oauth_token", "api_key", "api_key_helper", "third_party":
			return true, true
		}
		return false, false
	}
	if state.Status == "ready" {
		return true, true
	}
	if state.Status == "not_ready" {
		return false, true
	}
	return false, false
}
