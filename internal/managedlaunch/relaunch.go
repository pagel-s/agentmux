package managedlaunch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentmux/internal/harness"
	"agentmux/internal/managed"
)

type relaunchRequest struct {
	ID             string `json:"id"`
	Agent          string `json:"agent"`
	Attempt        string `json:"attempt"`
	Pane           string `json:"pane"`
	Resume         string `json:"resume,omitempty"`
	NativeLauncher string `json:"native_launcher,omitempty"`
}
type relaunchResult struct {
	ID    string `json:"id"`
	Pane  string `json:"pane,omitempty"`
	Error string `json:"error,omitempty"`
}

func relaunchPath(state, id string) string { return filepath.Join(state, "relaunch-"+id+".json") }

// Relaunch requests one deliberate restart through the existing supervisor.
// It never kills a live pane, renews deadlines, changes identity or restarts peers.
func (l *Launcher) Relaunch(ctx context.Context, id, resume string) (Runtime, error) {
	return l.RelaunchWithLauncher(ctx, id, resume, "")
}

// RelaunchWithLauncher optionally selects a public native launcher for this
// invocation alone. The running controller must explicitly support recovery;
// saved agent definitions, command prefixes and owner mounts stay unchanged.
func (l *Launcher) RelaunchWithLauncher(ctx context.Context, id, resume, nativeLauncher string) (Runtime, error) {
	if !validAgentID(id) {
		return Runtime{}, errors.New("invalid agent ID")
	}
	if !validResumeSelection(resume) {
		return Runtime{}, errors.New("invalid native history selection")
	}
	if err := validateNativeLauncher(nativeLauncher); err != nil {
		return Runtime{}, err
	}
	unlock, err := lock(l.options.StateDir + ".launch.lock")
	if err != nil {
		return Runtime{}, err
	}
	defer unlock()
	runtime, err := l.Describe(ctx)
	if err != nil {
		return runtime, err
	}
	if runtime.RelaunchVersion != 1 {
		return runtime, errors.New("running controller predates individual relaunch; restart this workspace once with the updated CLI")
	}
	if nativeLauncher != "" && runtime.NativeLauncherVersion != 1 {
		return runtime, errors.New("running controller does not support native launcher recovery; update the workspace controller before using --native-launcher")
	}
	if runtime.State != "running" || !l.ownsSession(ctx, runtime) {
		return runtime, errors.New("workspace is not running; restart the workspace first")
	}
	pane, ok := runtime.Agents[id]
	if !ok {
		return runtime, errors.New("session is not a workspace-owned terminal")
	}
	if pane.State == "running" {
		return runtime, errors.New("session is still running; exit its native CLI before relaunching")
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(l.options.StateDir, ""))
	if err != nil {
		return runtime, err
	}
	client := managed.NewClient(credential)
	if err := validateRelaunchWithLauncher(ctx, client, id, resume, nativeLauncher); err != nil {
		return runtime, err
	}
	nonce, err := newID()
	if err != nil {
		return runtime, err
	}
	request := relaunchRequest{ID: nonce, Agent: id, Attempt: runtime.AttemptID, Pane: pane.PaneID, Resume: resume, NativeLauncher: nativeLauncher}
	path := relaunchPath(l.options.StateDir, id)
	if err := writePrivateJSON(path, request); err != nil {
		return runtime, err
	}
	wait, stop := context.WithTimeout(ctx, startupTimeout)
	defer stop()
	for {
		data, err := readPrivateFile(path + ".result")
		if err == nil {
			var result relaunchResult
			if json.Unmarshal(data, &result) == nil && result.ID == nonce {
				current, e := Snapshot(l.options.StateDir)
				if e != nil {
					return runtime, e
				}
				if result.Error != "" {
					return current, errors.New(result.Error)
				}
				return current, nil
			}
		}
		if err := pause(wait); err != nil {
			return runtime, errors.New("relaunch response unavailable; inspect sessions before retrying; the request may have completed")
		}
	}
}

func validateRelaunch(ctx context.Context, client *managed.Client, id, resume string) error {
	return validateRelaunchWithLauncher(ctx, client, id, resume, "")
}

func validateNativeLauncher(path string) error {
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) {
		return errors.New("native launcher recovery requires an absolute executable path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return errors.New("native launcher recovery requires an available executable regular file")
	}
	return nil
}

func validateRelaunchWithLauncher(ctx context.Context, client *managed.Client, id, resume, nativeLauncher string) error {
	if err := validateNativeLauncher(nativeLauncher); err != nil {
		return err
	}
	raw, problem := client.Call(ctx, managed.Request{Operation: "agent.list", Args: json.RawMessage(`{"limit":200}`)})
	if problem != nil {
		return problem
	}
	var page struct {
		Agents []managed.AgentInfo `json:"agents"`
		Cursor string              `json:"cursor"`
	}
	for {
		page.Cursor = ""
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, a := range page.Agents {
			if a.ID == id {
				if a.State != "active" || a.External || a.Config.Interaction != "interactive" {
					return errors.New("only active workspace-owned native sessions can be relaunched")
				}
				command := a.Config.Command
				if nativeLauncher != "" {
					if a.Config.Sandbox != "local" {
						return errors.New("native launcher recovery is only available for trusted-local interactive sessions")
					}
					command = []string{nativeLauncher}
					if len(a.Config.Command) > 0 {
						command = append(command, a.Config.Command[1:]...)
					}
				}
				_, err := harness.BuildInteractive(harness.Request{Harness: a.Config.Harness, Command: command, NativeResume: resume, Prompt: "", Model: a.Config.Model})
				return err
			}
		}
		if page.Cursor == "" {
			return errors.New("session not found")
		}
		args, _ := json.Marshal(map[string]any{"limit": 200, "after": page.Cursor})
		raw, problem = client.Call(ctx, managed.Request{Operation: "agent.list", Args: args})
		if problem != nil {
			return problem
		}
	}
}

func (l *Launcher) processRelaunches(ctx context.Context, runtime *Runtime, client *managed.Client) error {
	files, err := filepath.Glob(filepath.Join(l.options.StateDir, "relaunch-*.json"))
	if err != nil {
		return err
	}
	for _, path := range files {
		data, err := readPrivateFile(path)
		if err != nil {
			continue
		}
		var r relaunchRequest
		if json.Unmarshal(data, &r) != nil || !validAgentID(r.Agent) || path != relaunchPath(l.options.StateDir, r.Agent) {
			continue
		}
		if previous, e := readPrivateFile(path + ".result"); e == nil {
			var result relaunchResult
			if json.Unmarshal(previous, &result) == nil && result.ID == r.ID {
				continue
			}
		}
		result := relaunchResult{ID: r.ID}
		reject := func(err error) error { result.Error = err.Error(); return writePrivateJSON(path+".result", result) }
		if runtime.AttemptID != r.Attempt {
			if err := reject(errors.New("relaunch belongs to a previous workspace attempt")); err != nil {
				return err
			}
			continue
		}
		if r.NativeLauncher != "" && runtime.NativeLauncherVersion != 1 {
			if err := reject(errors.New("running controller does not support native launcher recovery")); err != nil {
				return err
			}
			continue
		}
		tracked, ok := runtime.Agents[r.Agent]
		if !ok || tracked.PaneID != r.Pane {
			if err := reject(errors.New("session changed since relaunch was requested")); err != nil {
				return err
			}
			continue
		}
		if err := validateRelaunchWithLauncher(ctx, client, r.Agent, r.Resume, r.NativeLauncher); err != nil {
			if err := reject(err); err != nil {
				return err
			}
			continue
		}
		panes, err := l.panes(ctx, *runtime)
		if err != nil {
			return err
		}
		found := false
		var problem error
		for _, p := range panes {
			if p.ID == tracked.PaneID {
				found = true
				if p.Attempt != runtime.AttemptID || p.Agent != r.Agent || p.Role != "worker" {
					problem = errors.New("pane ownership changed; relaunch refused")
				} else if !p.Dead {
					problem = errors.New("session is still running; relaunch refused")
				}
				break
			}
		}
		if problem != nil {
			if err := reject(problem); err != nil {
				return err
			}
			continue
		}
		argv := []string{l.options.Executable, "managed", "worker", "--state", l.options.StateDir, "--agent", r.Agent}
		if r.Resume != "" {
			argv = append(argv, "--native-resume", r.Resume)
		}
		if r.NativeLauncher != "" {
			argv = append(argv, "--native-launcher", r.NativeLauncher)
		}
		id := tracked.PaneID
		if found {
			_, err = l.tmux(ctx, "respawn-pane", "-t", id, "exec "+ShellCommand(argv))
		} else {
			id, err = l.createPane(ctx, *runtime, "agent-"+r.Agent, "worker", r.Agent, argv)
		}
		if err != nil {
			if err := reject(fmt.Errorf("session relaunch failed: %w", err)); err != nil {
				return err
			}
			continue
		}
		tracked.PaneID, tracked.State = id, "running"
		tracked.Launches++
		runtime.Agents[r.Agent] = tracked
		if err := writeRuntime(l.options.StateDir, *runtime); err != nil {
			return err
		}
		if !found {
			_ = l.syncClientViews(ctx, *runtime)
		}
		result.Pane = id
		if err := writePrivateJSON(path+".result", result); err != nil {
			return err
		}
	}
	return nil
}

// Keep exported input validation independent of any shell interpretation.
func validResumeSelection(value string) bool {
	return len(value) <= 512 && !strings.HasPrefix(value, "-") && !strings.ContainsAny(value, "\x00\r\n")
}
