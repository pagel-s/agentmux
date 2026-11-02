package managedlaunch

import (
	"context"
	"os"
	"strconv"
	"strings"
)

// LastAgent restores a native terminal, never a provider history or a new turn.
// An attachment's own selection wins over the workspace fallback.
func (l *Launcher) LastAgent(ctx context.Context, runtime Runtime) (string, error) {
	if !l.ownsSession(ctx, runtime) {
		return "", errSessionAbsent
	}
	targets := []string{}
	if session := tmuxContextSession(SocketPath(l.options.StateDir), os.Getenv("TMUX")); session != "" {
		name, err := l.tmux(ctx, "display-message", "-p", "-t", session, "#{session_name}")
		if err == nil {
			name = strings.TrimSpace(name)
			tag, _ := l.tmux(ctx, "show-options", "-v", "-t", name, "@agentmux_view")
			if strings.TrimSpace(tag) == runtime.AttemptID {
				targets = append(targets, name)
			}
		}
	}
	targets = append(targets, runtime.Session)
	for _, target := range targets {
		id, _ := l.tmux(ctx, "show-options", "-v", "-t", target, "@agentmux_last_agent")
		id = strings.TrimSpace(id)
		if _, ok := runtime.Agents[id]; !ok || id == "" {
			continue
		}
		if _, err := l.liveAgentPane(ctx, runtime, id); err == nil {
			return id, nil
		}
	}
	return "", nil
}

func tmuxContextSession(socket, value string) string {
	last := strings.LastIndexByte(value, ',')
	previous := -1
	if last >= 0 {
		previous = strings.LastIndexByte(value[:last], ',')
	}
	if previous >= 0 && value[:previous] == socket {
		id := value[last+1:]
		if _, err := strconv.ParseUint(id, 10, 64); err == nil {
			return "$" + id
		}
	}
	return ""
}

func (l *Launcher) rememberAgent(ctx context.Context, runtime Runtime, view, agent string) error {
	for _, target := range []string{view, runtime.Session} {
		if _, err := l.tmux(ctx, "set-option", "-t", target, "@agentmux_last_agent", agent); err != nil {
			return err
		}
	}
	return nil
}
