package managedlaunch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentmux/internal/managed"
)

type paneInfo struct {
	ID, Attempt, Role, Agent string
	Dead                     bool
}
type registryAgent struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	State    string `json:"state"`
	External bool   `json:"external"`
}

// Supervise runs inside the dedicated supervisor pane. Broker and workers have
// their own tracked panes so ordered cleanup remains possible if this process
// dies. An exited worker is observed, never automatically respawned.
func (l *Launcher) Supervise(ctx context.Context, attempt string) error {
	unlock, err := lock(l.options.StateDir + "/supervisor.lock")
	if err != nil {
		return errors.New("managed supervisor is already running")
	}
	defer unlock()
	runtime, err := Snapshot(l.options.StateDir)
	if err != nil {
		return err
	}
	if runtime.AttemptID != attempt || !l.ownsSession(ctx, runtime) {
		return errors.New("supervisor attempt does not own this tmux session")
	}
	l.options.BrokerSandbox = runtime.BrokerSandbox
	background := context.Background()
	finish := func(cause error) error {
		shutdownCtx, cancel := context.WithTimeout(background, stopTimeout+5*time.Second)
		defer cancel()
		runtime.State = "stopping"
		runtime.BrokerRecovering = false
		_ = writeRuntime(l.options.StateDir, runtime)
		if err := l.stopOwned(shutdownCtx, runtime); err != nil {
			runtime.State, runtime.Problem = "attention", "worker shutdown is unconfirmed; broker remains available"
			_ = writeRuntime(l.options.StateDir, runtime)
			return err
		}
		runtime.State, runtime.Problem = "stopped", ""
		if cause != nil {
			runtime.State, runtime.Problem = "failed", "supervisor stopped after a broker or registry failure"
		}
		_ = writeRuntime(l.options.StateDir, runtime)
		return cause
	}
	// This definition is immutable for the attempt. Only an explicitly native
	// workspace may preserve human terminals while coordination reconnects.
	store, err := managed.Open(l.options.StateDir)
	if err != nil {
		return finish(err)
	}
	nativeWorkspace := store.Config().InteractiveWorkspace
	deadline, deadlineErr := store.RuntimeDeadline()
	_ = store.Close()
	if deadlineErr != nil {
		return finish(deadlineErr)
	}
	brokerArgv := []string{l.options.Executable, "managed", "serve", "--state", l.options.StateDir}
	if runtime.BrokerSandbox == "bubblewrap" {
		brokerArgv = append(brokerArgv, "--sandbox", "bubblewrap")
	}
	runtime.BrokerPane, err = l.createPane(ctx, runtime, "broker", "broker", "", brokerArgv)
	if err != nil {
		return finish(err)
	}
	if err = writeRuntime(l.options.StateDir, runtime); err != nil {
		return finish(err)
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(l.options.StateDir, ""))
	if err != nil {
		return finish(errors.New("supervisor could not load its owner credential"))
	}
	client := managed.NewClient(credential)
	readyCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	err = l.waitBroker(readyCtx, client, runtime)
	cancel()
	if err != nil {
		return finish(err)
	}
	// The owner console can inspect content and edit policy. Its credential is
	// loaded from private host state and is never mounted into an agent pane.
	runtime.DashboardPane, err = l.createPane(ctx, runtime, "dashboard", "dashboard", "", []string{l.options.Executable, "managed", "dashboard", "--state", l.options.StateDir})
	if err != nil {
		return finish(err)
	}
	if _, err := l.tmux(ctx, "select-window", "-t", runtime.DashboardPane); err != nil {
		return finish(err)
	}
	if err := l.configureWorkbench(ctx, runtime); err != nil {
		return finish(err)
	}
	windowTitles := map[string]string{}
	recovery := brokerRecovery{}
	for {
		if ctx.Err() != nil || shouldStop(l.options.StateDir, attempt) || !deadline.IsZero() && !time.Now().Before(deadline) {
			return finish(nil)
		}
		if !l.ownsSession(ctx, runtime) {
			return finish(errors.New("supervisor session ownership changed"))
		}
		panes, err := l.panes(ctx, runtime)
		if err != nil {
			return finish(err)
		}
		observeAgentPaneStates(&runtime, panes)
		brokerAlive := false
		for _, pane := range panes {
			if pane.ID == runtime.BrokerPane && pane.Attempt == attempt && pane.Role == "broker" && !pane.Dead {
				brokerAlive = true
			}
		}
		if !brokerAlive {
			if !nativeWorkspace {
				return finish(errors.New("managed broker exited; automatic restart is disabled"))
			}
			runtime.State, runtime.Problem = "attention", "Broker reconnecting; native terminals remain interactive. In-flight deliveries require explicit resolution."
			runtime.BrokerRecovering = true
			if !time.Now().Before(recovery.next) {
				if err := l.restartBrokerPane(ctx, &runtime, panes, brokerArgv); err != nil {
					runtime.Problem = "Broker restart deferred; native terminals remain interactive. " + err.Error()
				}
				recovery.deferAttempt(time.Now())
			}
			if err := writeRuntime(l.options.StateDir, runtime); err != nil {
				return finish(err)
			}
			if err := pause(ctx); err != nil {
				return finish(nil)
			}
			continue
		}
		if nativeWorkspace && time.Now().Before(recovery.next) {
			if err := pause(ctx); err != nil {
				return finish(nil)
			}
			continue
		}
		agents, err := registry(ctx, client)
		if err != nil {
			if !nativeWorkspace {
				return finish(err)
			}
			runtime.State, runtime.Problem = "attention", "Broker registry reconnecting; native terminals remain interactive. Coordination writes are deferred."
			runtime.BrokerRecovering = true
			recovery.deferAttempt(time.Now())
			if err := writeRuntime(l.options.StateDir, runtime); err != nil {
				return finish(err)
			}
			if err := pause(ctx); err != nil {
				return finish(nil)
			}
			continue
		}
		if err := l.processRelaunches(ctx, &runtime, client); err != nil {
			return finish(err)
		}
		addedWindows := false
		for _, agent := range agents {
			if agent.External {
				continue
			}
			pane, exists := runtime.Agents[agent.ID]
			if previous, labelled := windowTitles[pane.PaneID]; exists && (!labelled || previous != agent.Title) {
				// Presentation is best effort. A removed or retagged window
				// must not stop unrelated workers; retry only unsaved labels.
				if err := l.syncWindowTitle(ctx, runtime, pane.PaneID, agent.ID, agent.Title); err == nil {
					windowTitles[pane.PaneID] = agent.Title
				}
			}
			if agent.State != "active" {
				if exists && pane.State == "running" {
					if err := l.stopPaneSet(ctx, runtime, []string{pane.PaneID}); err != nil {
						return finish(err)
					}
					pane.State = "stopped"
					runtime.Agents[agent.ID] = pane
				}
				continue
			}
			if exists {
				continue
			}
			id, err := l.createPane(ctx, runtime, "agent-"+agent.ID, "worker", agent.ID, []string{l.options.Executable, "managed", "worker", "--state", l.options.StateDir, "--agent", agent.ID})
			if err != nil {
				return finish(err)
			}
			runtime.Agents[agent.ID] = AgentPane{PaneID: id, State: "running"}
			addedWindows = true
			if err := l.syncWindowTitle(ctx, runtime, id, agent.ID, agent.Title); err == nil {
				windowTitles[id] = agent.Title
			}
			if err := writeRuntime(l.options.StateDir, runtime); err != nil {
				return finish(err)
			}
		}
		if addedWindows {
			_ = l.syncClientViews(ctx, runtime)
		}
		runtime.State = "running"
		runtime.BrokerRecovering = false
		runtime.Problem = ""
		recovery = brokerRecovery{}
		if err := writeRuntime(l.options.StateDir, runtime); err != nil {
			return finish(err)
		}
		if err := pause(ctx); err != nil {
			return finish(nil)
		}
	}
}

type brokerRecovery struct {
	next  time.Time
	delay time.Duration
}

func (r *brokerRecovery) deferAttempt(now time.Time) {
	if r.delay == 0 {
		r.delay = time.Second
	} else {
		r.delay = min(10*time.Second, r.delay*2)
	}
	r.next = now.Add(r.delay)
}

// Recovery can restart only the exact dead broker owned by this attempt. It
// never kills a live process, respawns a worker, or reaches another tmux server.
func (l *Launcher) restartBrokerPane(ctx context.Context, runtime *Runtime, panes []paneInfo, argv []string) error {
	if ctx.Err() != nil || shouldStop(l.options.StateDir, runtime.AttemptID) {
		return errors.New("workspace shutdown requested")
	}
	if !l.ownsSession(ctx, *runtime) {
		return errors.New("runtime session ownership changed")
	}
	for _, pane := range panes {
		if pane.ID != runtime.BrokerPane {
			continue
		}
		if pane.Attempt != runtime.AttemptID || pane.Role != "broker" || pane.Agent != "" {
			return errors.New("broker pane ownership changed; refusing automatic replacement")
		}
		if !pane.Dead {
			return errors.New("broker process is still alive; refusing to replace it")
		}
		// No -k: a process that became live after the inventory must survive.
		_, err := l.tmux(ctx, "respawn-pane", "-t", pane.ID, "exec "+ShellCommand(argv))
		return err
	}
	pane, err := l.createPane(ctx, *runtime, "broker", "broker", "", argv)
	if err == nil {
		runtime.BrokerPane = pane
	}
	return err
}

// Only call after a successful complete pane inventory. Missing or retagged
// terminals are unavailable, never an invitation to respawn or stop peers.
func observeAgentPaneStates(runtime *Runtime, panes []paneInfo) {
	for id, tracked := range runtime.Agents {
		var observed *paneInfo
		for i := range panes {
			p := &panes[i]
			if p.ID == tracked.PaneID && p.Agent == id && p.Attempt == runtime.AttemptID && p.Role == "worker" {
				observed = p
				break
			}
		}
		switch {
		case observed == nil && tracked.State == "running":
			tracked.State = "unavailable"
		case observed != nil && observed.Dead && tracked.State != "stopped":
			tracked.State = "exited"
		case observed != nil && !observed.Dead && tracked.State != "stopped":
			tracked.State = "running"
		}
		runtime.Agents[id] = tracked
	}
}

func (l *Launcher) createPane(ctx context.Context, runtime Runtime, name, role, agent string, argv []string) (string, error) {
	args := []string{"new-window", "-d", "-P", "-F", "#{pane_id}", "-t", runtime.Session, "-n", name}
	// tmux inspects the worker wrapper's cwd, rather than its nested native
	// child. Start the wrapper in the project too, so ordinary splits inherit it.
	if role == "worker" {
		if store, err := managed.Open(l.options.StateDir); err == nil {
			definition, problem := store.Agent(agent)
			_ = store.Close()
			if problem == nil && filepath.IsAbs(definition.Workdir) {
				if info, err := os.Stat(definition.Workdir); err == nil && info.IsDir() {
					args = append(args, "-c", definition.Workdir)
				}
			}
		}
	}
	args = append(args, "exec sleep 300")
	out, err := l.tmux(ctx, args...)
	if err != nil {
		return "", err
	}
	pane := strings.TrimSpace(out)
	if err := l.tagPane(ctx, pane, runtime.AttemptID, role, agent); err != nil {
		return pane, err
	}
	if _, err := l.tmux(ctx, "respawn-pane", "-k", "-t", pane, "exec "+ShellCommand(argv)); err != nil {
		return pane, err
	}
	return pane, nil
}

func (l *Launcher) panes(ctx context.Context, runtime Runtime) ([]paneInfo, error) {
	out, err := l.tmux(ctx, "list-panes", "-s", "-t", runtime.Session, "-F", "#{pane_id}\t#{pane_dead}\t#{@agentmux_runtime}\t#{@agentmux_role}\t#{@agentmux_agent}")
	if err != nil {
		return nil, err
	}
	var result []paneInfo
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || !validPane(fields[0]) {
			return nil, errors.New("invalid tracked tmux pane metadata")
		}
		result = append(result, paneInfo{ID: fields[0], Dead: fields[1] == "1", Attempt: fields[2], Role: fields[3], Agent: fields[4]})
	}
	return result, nil
}

func (l *Launcher) waitBroker(ctx context.Context, client *managed.Client, runtime Runtime) error {
	for {
		queryCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		_, problem := client.Call(queryCtx, managed.Request{Operation: "identity", Args: json.RawMessage(`{}`)})
		cancel()
		if problem == nil {
			return nil
		}
		if problem.Code != "transport" {
			return errors.New("managed broker rejected supervisor authentication")
		}
		panes, err := l.panes(ctx, runtime)
		if err != nil {
			return err
		}
		for _, pane := range panes {
			if pane.ID == runtime.BrokerPane && pane.Dead {
				return errors.New("managed broker exited before becoming ready")
			}
		}
		if err := pause(ctx); err != nil {
			return errors.New("managed broker did not become ready")
		}
	}
}

func registry(ctx context.Context, client *managed.Client) ([]registryAgent, error) {
	cursor := ""
	result := []registryAgent{}
	for page := 0; page < 1000; page++ {
		args, _ := json.Marshal(map[string]any{"limit": 200, "after": cursor})
		queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		raw, problem := client.Call(queryCtx, managed.Request{Operation: "agent.list", Args: args})
		cancel()
		if problem != nil {
			return nil, errors.New("managed registry is unavailable")
		}
		var response struct {
			Agents []registryAgent `json:"agents"`
			Cursor string          `json:"cursor"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, errors.New("invalid agent registry response")
		}
		for _, agent := range response.Agents {
			if !validAgentID(agent.ID) || (agent.State != "active" && agent.State != "revoked") {
				return nil, errors.New("invalid registered agent identity")
			}
			result = append(result, agent)
		}
		if len(response.Agents) == 0 || response.Cursor == "" || response.Cursor == cursor {
			return result, nil
		}
		cursor = response.Cursor
	}
	return nil, errors.New("agent registry pagination exceeded its bound")
}

func validAgentID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i, c := range id {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && ((c >= '0' && c <= '9') || c == '_' || c == '-')) {
			continue
		}
		return false
	}
	return true
}

func (l *Launcher) stopOwned(ctx context.Context, runtime Runtime) error {
	owned, absent, err := l.inspectOwnership(ctx, runtime)
	if err != nil {
		return err
	}
	if !owned && absent {
		return nil
	}
	panes, err := l.panes(ctx, runtime)
	if err != nil {
		return err
	}
	var workers, brokers []string
	for _, pane := range panes {
		if pane.Attempt != runtime.AttemptID || pane.Dead {
			continue
		}
		switch pane.Role {
		case "worker":
			workers = append(workers, pane.ID)
		case "broker":
			brokers = append(brokers, pane.ID)
		}
	}
	if err := l.stopPaneSet(ctx, runtime, workers); err != nil {
		return err
	}
	return l.stopPaneSet(ctx, runtime, brokers)
}

func (l *Launcher) stopPaneSet(ctx context.Context, runtime Runtime, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if !l.ownsSession(ctx, runtime) {
		return errors.New("runtime session ownership changed during shutdown")
	}
	targets := map[string]bool{}
	for _, id := range ids {
		if validPane(id) {
			targets[id] = true
		}
	}
	panes, err := l.panes(ctx, runtime)
	if err != nil {
		return err
	}
	for _, pane := range panes {
		if targets[pane.ID] && pane.Attempt == runtime.AttemptID && !pane.Dead {
			if _, err := l.tmux(ctx, "send-keys", "-t", pane.ID, "C-c"); err != nil {
				return err
			}
		}
	}
	stopCtx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	for {
		panes, err := l.panes(stopCtx, runtime)
		if err != nil {
			return err
		}
		pending := false
		for _, pane := range panes {
			if targets[pane.ID] && pane.Attempt == runtime.AttemptID && !pane.Dead {
				pending = true
			}
		}
		if !pending {
			return nil
		}
		if err := pause(stopCtx); err != nil {
			return errors.New("tracked processes did not confirm termination; broker was not stopped")
		}
	}
}

// Describe exposes launch metadata even when the broker is unavailable.
func (l *Launcher) Describe(ctx context.Context) (Runtime, error) {
	runtime, err := Snapshot(l.options.StateDir)
	if err != nil {
		return runtime, err
	}
	owned, absent, ownershipErr := l.inspectOwnership(ctx, runtime)
	if ownershipErr != nil {
		runtime.State = "unknown"
		return runtime, ownershipErr
	}
	if owned {
		panes, err := l.panes(ctx, runtime)
		if err != nil {
			return runtime, err
		}
		for _, pane := range panes {
			if pane.Attempt == runtime.AttemptID && pane.Role == "worker" {
				if _, tracked := runtime.Agents[pane.Agent]; !tracked {
					runtime.Agents[pane.Agent] = AgentPane{PaneID: pane.ID, State: "running"}
				}
			}
		}
		observeAgentPaneStates(&runtime, panes)
	} else if absent && (runtime.State == "running" || runtime.State == "starting") {
		runtime.State = "unavailable"
	}
	return runtime, nil
}

func FormatStatus(runtime Runtime, counts map[string]any) string {
	var output strings.Builder
	fmt.Fprintf(&output, "Agentmux %s  [%s]\nAttempt %s\nBroker sandbox: %s\n", runtime.Session, runtime.State, runtime.AttemptID, runtime.BrokerSandbox)
	if runtime.Problem != "" {
		fmt.Fprintf(&output, "Attention: %s\n", runtime.Problem)
	}
	if counts != nil {
		fmt.Fprintf(&output, "Messages: %v  Tasks: %v\n", counts["messages"], counts["tasks"])
		if value, ok := counts["task_states"]; ok {
			data, _ := json.Marshal(value)
			fmt.Fprintf(&output, "Task states: %s\n", data)
		}
	}
	ids := make([]string, 0, len(runtime.Agents))
	for id := range runtime.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	output.WriteString("\nAGENT\tPANE\tWORKER\n")
	for _, id := range ids {
		pane := runtime.Agents[id]
		fmt.Fprintf(&output, "%s\t%s\t%s\n", id, pane.PaneID, pane.State)
	}
	output.WriteString("\nExited workers are not automatically restarted.\n")
	if runtime.AttachCommand != "" {
		fmt.Fprintf(&output, "Attach: %s\n", runtime.AttachCommand)
	}
	return output.String()
}
