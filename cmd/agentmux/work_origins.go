package main

import (
	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
	"agentmux/internal/sessioninventory"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Attribution uses authenticated known registries and exact process ancestry.
// Matching project names, titles, or recycled pane numbers is not ownership.
func annotateSessionOrigins(parent context.Context, current string, observed []sessioninventory.Session) []sessioninventory.Session {
	if len(observed) == 0 {
		return observed
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	index, err := workspaceIndexPath()
	if err != nil {
		return observed
	}
	paths, _ := loadWorkspaceIndex(index)
	entries, _ := os.ReadDir(filepath.Dir(index))
	for _, e := range entries {
		if e.IsDir() {
			paths = append(paths, filepath.Join(filepath.Dir(index), e.Name()))
		}
	}
	seen := map[string]bool{}
	for _, state := range paths {
		if ctx.Err() != nil {
			break
		}
		state = filepath.Clean(state)
		if state == filepath.Clean(current) || seen[state] {
			continue
		}
		seen[state] = true
		credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
		if err != nil || credential.AgentID != "$owner" {
			continue
		}
		raw, problem := managed.NewClient(credential).Call(ctx, managed.Request{Operation: "agent.list", Args: json.RawMessage(`{"limit":200}`)})
		if problem != nil {
			continue
		}
		var registry struct {
			Agents []managed.AgentInfo `json:"agents"`
		}
		if json.Unmarshal(raw, &registry) != nil {
			continue
		}
		runtime, err := managedlaunch.Snapshot(state)
		if err != nil {
			continue
		}
		roots := map[int]bool{}
		if tag, e := contextTMux(ctx, managedlaunch.SocketPath(state), "show-options", "-v", "-t", runtime.Session, "@agentmux_runtime"); e == nil && tag == runtime.AttemptID {
			if pids, e := contextTMux(ctx, managedlaunch.SocketPath(state), "list-panes", "-s", "-t", runtime.Session, "-F", "#{pane_pid}"); e == nil {
				for _, value := range strings.Fields(pids) {
					pid, _ := strconv.Atoi(value)
					if pid > 0 {
						roots[pid] = true
					}
				}
			}
		}
		for i := range observed {
			known := processDescendsFrom(observed[i].PID, roots)
			for _, agent := range registry.Agents {
				known = known || agent.External && agent.State == "active" && agent.ExternalRef == observed[i].ID
			}
			if known {
				observed[i].WorkspaceOrigins = append(observed[i].WorkspaceOrigins, runtime.Session)
			}
		}
	}
	for i := range observed {
		sort.Strings(observed[i].WorkspaceOrigins)
	}
	return observed
}

func processDescendsFrom(pid int, roots map[int]bool) bool {
	for depth := 0; pid > 1 && depth < 64; depth++ {
		if roots[pid] {
			return true
		}
		raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if err != nil {
			return false
		}
		end := strings.LastIndexByte(string(raw), ')')
		if end < 0 {
			return false
		}
		fields := strings.Fields(string(raw[end+1:]))
		if len(fields) < 2 {
			return false
		}
		next, err := strconv.Atoi(fields[1])
		if err != nil || next == pid {
			return false
		}
		pid = next
	}
	return false
}
