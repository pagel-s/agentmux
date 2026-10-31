// Package managedlaunch owns a dedicated tmux presentation and supervisor for
// managed runs. It never uses the user's default tmux server or sends prompts
// to agent terminals. Workers consume the authenticated broker task queue.
package managedlaunch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

const (
	startupTimeout = 20 * time.Second
	stopTimeout    = 12 * time.Second
	pollInterval   = 200 * time.Millisecond
)

var errSessionAbsent = errors.New("dedicated tmux session is absent")

type Options struct {
	StateDir      string
	Executable    string
	BrokerSandbox string
}

type AgentPane struct {
	PaneID string `json:"pane_id"`
	State  string `json:"state"`
}

// Runtime is presentation/lifecycle metadata only. No configuration, task text,
// environment values, tokens, or model output enters the launch manifest.
type Runtime struct {
	Version        int                  `json:"version"`
	AttemptID      string               `json:"attempt_id"`
	Session        string               `json:"session"`
	State          string               `json:"state"`
	BrokerSandbox  string               `json:"broker_sandbox"`
	SupervisorPane string               `json:"supervisor_pane,omitempty"`
	BrokerPane     string               `json:"broker_pane,omitempty"`
	DashboardPane  string               `json:"dashboard_pane,omitempty"`
	Agents         map[string]AgentPane `json:"agents"`
	UpdatedAt      string               `json:"updated_at"`
	Problem        string               `json:"problem,omitempty"`
	AttachCommand  string               `json:"attach_command,omitempty"`
}

type Launcher struct {
	options Options
	run     func(context.Context, ...string) (string, error)
}

func New(options Options) (*Launcher, error) {
	if options.StateDir == "" || options.Executable == "" {
		return nil, errors.New("managed launcher requires state and executable paths")
	}
	var err error
	options.StateDir, err = filepath.Abs(options.StateDir)
	if err != nil {
		return nil, err
	}
	options.Executable, err = filepath.Abs(options.Executable)
	if err != nil {
		return nil, err
	}
	if strings.ContainsRune(options.StateDir+options.Executable, 0) {
		return nil, errors.New("invalid launcher path")
	}
	if len(SocketPath(options.StateDir)) > 100 {
		return nil, errors.New("state path is too long for its dedicated tmux socket")
	}
	if options.BrokerSandbox == "" {
		options.BrokerSandbox = "local"
	}
	if options.BrokerSandbox != "local" && options.BrokerSandbox != "bubblewrap" {
		return nil, errors.New("broker sandbox must be local or bubblewrap")
	}
	return &Launcher{options: options, run: runTMux}, nil
}

func SocketPath(state string) string   { return filepath.Join(state, "tmux.sock") }
func manifestPath(state string) string { return filepath.Join(state, "runtime.json") }
func stopPath(state string) string     { return filepath.Join(state, "runtime-stop.json") }

func Snapshot(state string) (Runtime, error) {
	var runtime Runtime
	data, err := readPrivateFile(manifestPath(state))
	if err != nil {
		return runtime, err
	}
	if err := json.Unmarshal(data, &runtime); err != nil {
		return runtime, errors.New("invalid managed runtime manifest")
	}
	if runtime.Version != 1 || runtime.AttemptID == "" || runtime.Session == "" {
		return runtime, errors.New("unsupported managed runtime manifest")
	}
	return runtime, nil
}

// Up assumes the caller completed its no-mutation preflight. It reuses only an
// identical initialized configuration and never replaces a running attempt.
func (l *Launcher) Up(ctx context.Context, cfg managedconfig.Config) (Runtime, error) {
	if err := cfg.Validate(); err != nil {
		return Runtime{}, err
	}
	if err := os.MkdirAll(filepath.Dir(l.options.StateDir), 0700); err != nil {
		return Runtime{}, err
	}
	unlock, err := lock(l.options.StateDir + ".launch.lock")
	if err != nil {
		return Runtime{}, errors.New("another managed lifecycle operation is in progress")
	}
	defer unlock()
	if err := l.ensureState(cfg); err != nil {
		return Runtime{}, err
	}
	if previous, err := Snapshot(l.options.StateDir); err == nil {
		if l.ownsSession(ctx, previous) {
			return previous, errors.New("managed run already has a tmux session; use status, attach, or restart")
		}
		if previous.State != "stopped" && previous.State != "failed" {
			return previous, errors.New("previous runtime did not stop cleanly; run managed down before starting a new attempt")
		}
	} else if !os.IsNotExist(err) {
		return Runtime{}, err
	}
	if _, err := l.tmux(ctx, "has-session", "-t", cfg.Session); err == nil {
		return Runtime{}, errors.New("refusing to replace an untracked tmux session")
	}
	attempt, err := newID()
	if err != nil {
		return Runtime{}, err
	}
	runtime := Runtime{Version: 1, AttemptID: attempt, Session: cfg.Session, State: "starting", BrokerSandbox: l.options.BrokerSandbox, Agents: map[string]AgentPane{}}
	runtime.AttachCommand = ShellCommand([]string{"tmux", "-S", SocketPath(l.options.StateDir), "attach-session", "-t", runtime.Session})
	if err := writeRuntime(l.options.StateDir, runtime); err != nil {
		return runtime, err
	}
	pane, err := l.tmux(ctx, "new-session", "-d", "-s", runtime.Session, "-n", "supervisor", "-P", "-F", "#{pane_id}", "exec sleep 300")
	if err != nil {
		runtime.State, runtime.Problem = "failed", "tmux session creation failed"
		_ = writeRuntime(l.options.StateDir, runtime)
		return runtime, err
	}
	runtime.SupervisorPane = strings.TrimSpace(pane)
	if !validPane(runtime.SupervisorPane) {
		return runtime, errors.New("tmux did not return a pane identity")
	}
	if _, err := l.tmux(ctx, "set-option", "-t", runtime.Session, "@agentmux_runtime", attempt); err != nil {
		return l.rollback(ctx, runtime, err)
	}
	if err := l.tagPane(ctx, runtime.SupervisorPane, attempt, "supervisor", ""); err != nil {
		return l.rollback(ctx, runtime, err)
	}
	if err := os.Chmod(SocketPath(l.options.StateDir), 0600); err != nil {
		return l.rollback(ctx, runtime, err)
	}
	if err := writeRuntime(l.options.StateDir, runtime); err != nil {
		return l.rollback(ctx, runtime, err)
	}
	command := ShellCommand([]string{l.options.Executable, "managed", "supervise", "--state", l.options.StateDir, "--attempt", attempt})
	if _, err := l.tmux(ctx, "respawn-pane", "-k", "-t", runtime.SupervisorPane, "exec "+command); err != nil {
		return l.rollback(ctx, runtime, err)
	}
	readyCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	for {
		current, err := Snapshot(l.options.StateDir)
		if err == nil && current.AttemptID == attempt {
			if current.State == "running" {
				return current, nil
			}
			if current.State == "failed" || current.State == "attention" || current.State == "stopped" {
				return current, errors.New("managed startup did not complete; inspect status and the supervisor pane")
			}
		}
		if err := pause(readyCtx); err != nil {
			_ = writeStop(l.options.StateDir, attempt)
			return runtime, errors.New("managed startup did not become ready; shutdown requested for this attempt")
		}
	}
}

func (l *Launcher) ensureState(cfg managedconfig.Config) error {
	if _, err := os.Stat(filepath.Join(l.options.StateDir, "state.sqlite")); os.IsNotExist(err) {
		store, err := managed.Init(l.options.StateDir, cfg)
		if err != nil {
			return err
		}
		return store.Close()
	} else if err != nil {
		return err
	}
	store, err := managed.Open(l.options.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	want := cfg.Normalized()
	for id, agent := range want.Agents {
		if agent.Workdir == "" {
			agent.Workdir = filepath.Join(l.options.StateDir, "workspaces", id)
			want.Agents[id] = agent
		}
	}
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(store.Config())
	if string(a) != string(b) {
		return errors.New("state configuration differs; use its saved configuration via restart or choose a new state directory")
	}
	return nil
}

func (l *Launcher) rollback(ctx context.Context, runtime Runtime, cause error) (Runtime, error) {
	if l.ownsSession(ctx, runtime) {
		_, _ = l.tmux(ctx, "kill-session", "-t", runtime.Session)
	}
	runtime.State, runtime.Problem = "failed", "launch preparation failed before the supervisor started"
	_ = writeRuntime(l.options.StateDir, runtime)
	return runtime, cause
}

// Down is idempotent and targets only the exact session/panes owned by this
// recorded attempt. Workers must stop before the broker is interrupted.
func (l *Launcher) Down(ctx context.Context) (Runtime, error) {
	if _, err := os.Stat(l.options.StateDir); os.IsNotExist(err) {
		return Runtime{Version: 1, State: "stopped", Agents: map[string]AgentPane{}}, nil
	}
	unlock, err := lock(l.options.StateDir + ".launch.lock")
	if err != nil {
		return Runtime{}, errors.New("another managed lifecycle operation is in progress")
	}
	defer unlock()
	runtime, err := Snapshot(l.options.StateDir)
	if os.IsNotExist(err) {
		return Runtime{Version: 1, State: "stopped", Agents: map[string]AgentPane{}}, nil
	}
	if err != nil {
		return runtime, err
	}
	owned, absent, ownershipErr := l.inspectOwnership(ctx, runtime)
	if ownershipErr != nil {
		return runtime, ownershipErr
	}
	if !owned && absent {
		runtime.State, runtime.Problem = "stopped", ""
		return runtime, writeRuntime(l.options.StateDir, runtime)
	}
	if err := writeStop(l.options.StateDir, runtime.AttemptID); err != nil {
		return runtime, err
	}
	// The controller also performs ordered shutdown so cleanup works after a
	// supervisor crash. A live supervisor performs the same idempotent steps.
	if err := l.stopOwned(ctx, runtime); err != nil {
		runtime.State, runtime.Problem = "attention", "worker shutdown is unconfirmed; broker remains available"
		_ = writeRuntime(l.options.StateDir, runtime)
		return runtime, err
	}
	// Wait for the supervisor to release its lock and finish metadata writes
	// before allowing another attempt to reuse this state directory.
	if err := l.stopPaneSet(ctx, runtime, []string{runtime.SupervisorPane}); err != nil {
		return runtime, err
	}
	if l.ownsSession(ctx, runtime) {
		if _, err := l.tmux(ctx, "kill-session", "-t", runtime.Session); err != nil {
			return runtime, err
		}
	}
	runtime.State, runtime.Problem = "stopped", ""
	for id, pane := range runtime.Agents {
		pane.State = "stopped"
		runtime.Agents[id] = pane
	}
	return runtime, writeRuntime(l.options.StateDir, runtime)
}

func (l *Launcher) Attach(ctx context.Context, runtime Runtime, in io.Reader, out, diagnostics io.Writer) error {
	if !l.ownsSession(ctx, runtime) {
		return errors.New("managed tmux session is not running with this attempt identity")
	}
	command := exec.CommandContext(ctx, "tmux", "-S", SocketPath(l.options.StateDir), "attach-session", "-t", runtime.Session)
	command.Env = withoutTMux(os.Environ())
	command.Stdin, command.Stdout, command.Stderr = in, out, diagnostics
	return command.Run()
}

func (l *Launcher) tmux(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	argv := append([]string{"-S", SocketPath(l.options.StateDir), "-f", "/dev/null"}, args...)
	return l.run(ctx, argv...)
}

func runTMux(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "tmux", args...)
	command.Env = withoutTMux(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.ToLower(string(output))
		if strings.Contains(message, "no server running") || strings.Contains(message, "can't find session") || strings.Contains(message, "no sessions") || (strings.Contains(message, "error connecting") && strings.Contains(message, "no such file")) {
			return "", errSessionAbsent
		}
		return "", errors.New("dedicated tmux operation failed; inspect managed status")
	}
	return string(output), nil
}

func withoutTMux(env []string) []string {
	result := make([]string, 0, len(env))
	for _, value := range env {
		if !strings.HasPrefix(value, "TMUX=") {
			result = append(result, value)
		}
	}
	return result
}

func (l *Launcher) ownsSession(ctx context.Context, runtime Runtime) bool {
	value, err := l.tmux(ctx, "show-options", "-v", "-t", runtime.Session, "@agentmux_runtime")
	return err == nil && strings.TrimSpace(value) == runtime.AttemptID
}

func (l *Launcher) inspectOwnership(ctx context.Context, runtime Runtime) (bool, bool, error) {
	if l.ownsSession(ctx, runtime) {
		return true, false, nil
	}
	_, err := l.tmux(ctx, "has-session", "-t", runtime.Session)
	if errors.Is(err, errSessionAbsent) {
		return false, true, nil
	}
	if err != nil {
		return false, false, errors.New("cannot confirm managed tmux session state; refusing cleanup")
	}
	return false, false, errors.New("tmux session ownership changed; refusing cleanup")
}

func (l *Launcher) tagPane(ctx context.Context, pane, attempt, role, agent string) error {
	if !validPane(pane) {
		return errors.New("invalid pane identity")
	}
	for _, pair := range [][2]string{{"@agentmux_runtime", attempt}, {"@agentmux_role", role}, {"@agentmux_agent", agent}} {
		if _, err := l.tmux(ctx, "set-option", "-p", "-t", pane, pair[0], pair[1]); err != nil {
			return err
		}
	}
	_, err := l.tmux(ctx, "set-option", "-w", "-t", pane, "remain-on-exit", "on")
	return err
}

func validPane(value string) bool {
	if !strings.HasPrefix(value, "%") || len(value) < 2 {
		return false
	}
	for _, c := range value[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func ShellCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
	}
	return strings.Join(quoted, " ")
}

func writeRuntime(state string, runtime Runtime) error {
	runtime.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return writePrivateJSON(manifestPath(state), runtime)
}
func writeStop(state, attempt string) error {
	return writePrivateJSON(stopPath(state), map[string]string{"attempt_id": attempt})
}
func shouldStop(state, attempt string) bool {
	data, err := readPrivateFile(stopPath(state))
	if err != nil {
		return false
	}
	var value struct {
		AttemptID string `json:"attempt_id"`
	}
	return json.Unmarshal(data, &value) == nil && value.AttemptID == attempt
}

func readPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024*1024 {
		return nil, errors.New("runtime metadata must be a private bounded regular file")
	}
	return os.ReadFile(path)
}
func writePrivateJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".runtime-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "run_" + hex.EncodeToString(b[:]), nil
}
func pause(ctx context.Context) error {
	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func sortedAgentIDs(agents map[string]managedconfig.Agent) []string {
	ids := make([]string, 0, len(agents))
	for id := range agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
