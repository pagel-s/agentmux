package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentmux/internal/config"
	tpl "agentmux/internal/template"
)

type Config struct {
	Version     int                 `json:"version"`
	Name        string              `json:"name,omitempty"`
	Session     string              `json:"session"`
	Description string              `json:"description,omitempty"`
	Requires    config.Requirements `json:"requires,omitempty"`
	Params      map[string]string   `json:"params,omitempty"`
	Env         map[string]string   `json:"env,omitempty"`
	Agents      map[string]Agent    `json:"agents,omitempty"`
	Windows     []Window            `json:"windows"`
	Hooks       Hooks               `json:"hooks"`
}

type Agent struct {
	Name     string            `json:"name"`
	Provider string            `json:"provider"`
	Command  string            `json:"command"`
	Cwd      string            `json:"cwd,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Title    string            `json:"title,omitempty"`
	Shell    string            `json:"shell,omitempty"`
	OnExit   string            `json:"onExit"`
}

type Window struct {
	Name        string            `json:"name"`
	Root        string            `json:"root,omitempty"`
	Layout      string            `json:"layout,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	GridRows    int               `json:"gridRows,omitempty"`
	GridColumns int               `json:"gridColumns,omitempty"`
	Panes       []Pane            `json:"panes"`
}

type Pane struct {
	Name       string            `json:"name,omitempty"`
	SourceType string            `json:"sourceType"`
	AgentName  string            `json:"agentName,omitempty"`
	Command    string            `json:"command"`
	Cwd        string            `json:"cwd,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Title      string            `json:"title,omitempty"`
	Focus      bool              `json:"focus,omitempty"`
	Shell      string            `json:"shell,omitempty"`
	OnExit     string            `json:"onExit"`
}

type Hooks struct {
	PreSession  string `json:"preSession,omitempty"`
	PostSession string `json:"postSession,omitempty"`
}

func Project(doc *config.Document, overrides map[string]string) (*Config, error) {
	raw := &doc.Config
	if overrides == nil {
		overrides = make(map[string]string)
	}

	// Inject config_dir if not already present
	if _, ok := overrides["config_dir"]; !ok {
		overrides["config_dir"] = filepath.Dir(doc.Path)
	}

	params, err := resolveParams(raw.Params, overrides)
	if err != nil {
		return nil, err
	}

	name, err := expand(raw.Name, params)
	if err != nil {
		return nil, err
	}
	session, err := expand(raw.Session, params)
	if err != nil {
		return nil, err
	}
	description, err := expand(raw.Description, params)
	if err != nil {
		return nil, err
	}
	env, err := expandMap(raw.Env, params)
	if err != nil {
		return nil, err
	}

	agents := make(map[string]Agent, len(raw.Agents))
	for name, agent := range raw.Agents {
		resolvedAgent, err := resolveAgent(name, agent, params)
		if err != nil {
			return nil, err
		}
		agents[name] = resolvedAgent
	}

	windows := make([]Window, 0, len(raw.Layout))
	for _, window := range raw.Layout {
		resolvedWindow, err := resolveWindow(window, agents, env, params)
		if err != nil {
			return nil, err
		}
		windows = append(windows, resolvedWindow)
	}

	preSession, err := expand(raw.Hooks.PreSession, params)
	if err != nil {
		return nil, err
	}
	postSession, err := expand(raw.Hooks.PostSession, params)
	if err != nil {
		return nil, err
	}

	return &Config{
		Version:     raw.Version,
		Name:        name,
		Session:     session,
		Description: description,
		Requires:    raw.Requires,
		Params:      params,
		Env:         env,
		Agents:      agents,
		Windows:     windows,
		Hooks: Hooks{
			PreSession:  preSession,
			PostSession: postSession,
		},
	}, nil
}

func resolveAgent(name string, raw config.Agent, params map[string]string) (Agent, error) {
	provider, err := expand(raw.Provider, params)
	if err != nil {
		return Agent{}, err
	}
	command, err := expand(raw.Command, params)
	if err != nil {
		return Agent{}, err
	}
	cwd, err := expand(raw.Cwd, params)
	if err != nil {
		return Agent{}, err
	}
	env, err := expandMap(raw.Env, params)
	if err != nil {
		return Agent{}, err
	}
	title, err := expand(raw.Title, params)
	if err != nil {
		return Agent{}, err
	}
	shell, err := expand(raw.Shell, params)
	if err != nil {
		return Agent{}, err
	}
	onExit, err := expand(raw.OnExit, params)
	if err != nil {
		return Agent{}, err
	}

	return Agent{
		Name:     name,
		Provider: provider,
		Command:  command,
		Cwd:      cleanPath(cwd),
		Env:      env,
		Title:    title,
		Shell:    shell,
		OnExit:   effectiveOnExit(onExit, config.OnExitShell),
	}, nil
}

func resolveWindow(raw config.Window, agents map[string]Agent, sessionEnv map[string]string, params map[string]string) (Window, error) {
	name, err := expand(raw.Window, params)
	if err != nil {
		return Window{}, err
	}
	root, err := expand(raw.Root, params)
	if err != nil {
		return Window{}, err
	}
	layout, err := expand(raw.Layout, params)
	if err != nil {
		return Window{}, err
	}
	windowEnv, err := expandMap(raw.Env, params)
	if err != nil {
		return Window{}, err
	}

	resolved := Window{
		Name:        name,
		Root:        cleanPath(root),
		Layout:      layout,
		Env:         windowEnv,
		GridRows:    raw.GridRows,
		GridColumns: raw.GridColumns,
		Panes:       make([]Pane, 0, len(raw.Panes)),
	}
	for _, pane := range raw.Panes {
		out, err := resolvePane(pane, agents, sessionEnv, windowEnv, resolved.Root, params)
		if err != nil {
			return Window{}, err
		}
		resolved.Panes = append(resolved.Panes, out)
	}

	return resolved, nil
}

func resolvePane(raw config.Pane, agents map[string]Agent, sessionEnv map[string]string, windowEnv map[string]string, windowRoot string, params map[string]string) (Pane, error) {
	name, err := expand(raw.Name, params)
	if err != nil {
		return Pane{}, err
	}
	agentName, err := expand(raw.Agent, params)
	if err != nil {
		return Pane{}, err
	}
	command, err := expand(raw.Command, params)
	if err != nil {
		return Pane{}, err
	}
	title, err := expand(raw.Title, params)
	if err != nil {
		return Pane{}, err
	}
	cwd, err := expand(raw.Cwd, params)
	if err != nil {
		return Pane{}, err
	}
	paneEnv, err := expandMap(raw.Env, params)
	if err != nil {
		return Pane{}, err
	}
	onExit, err := expand(raw.OnExit, params)
	if err != nil {
		return Pane{}, err
	}

	resolved := Pane{
		Focus:  raw.Focus,
		Env:    cloneMap(sessionEnv),
		Name:   name,
		OnExit: config.OnExitShell,
	}

	if agentName != "" {
		agent := agents[agentName]
		resolved.SourceType = "agent"
		resolved.AgentName = agentName
		resolved.Command = agent.Command
		resolved.Shell = agent.Shell
		resolved.OnExit = effectiveOnExit(onExit, agent.OnExit, config.OnExitShell)
		mergeInto(resolved.Env, agent.Env)
		mergeInto(resolved.Env, windowEnv)
		resolved.Cwd = firstNonEmpty(cleanPath(cwd), agent.Cwd, windowRoot, mustGetwd())
		resolved.Title = firstNonEmpty(title, agent.Title, name, agentName, commandFallback(agent.Command))
	} else {
		resolved.SourceType = "command"
		resolved.Command = command
		resolved.OnExit = effectiveOnExit(onExit, config.OnExitShell)
		mergeInto(resolved.Env, windowEnv)
		resolved.Cwd = firstNonEmpty(cleanPath(cwd), windowRoot, mustGetwd())
		resolved.Title = firstNonEmpty(title, name, commandFallback(command))
	}

	mergeInto(resolved.Env, paneEnv)
	if resolved.Command == "" {
		resolved.Command = command
	}
	if resolved.Cwd == "" {
		resolved.Cwd = mustGetwd()
	}
	if resolved.Title == "" {
		resolved.Title = commandFallback(resolved.Command)
	}

	return resolved, nil
}

func resolveParams(base map[string]string, overrides map[string]string) (map[string]string, error) {
	params := make(map[string]string, len(base)+len(overrides))
	for key, value := range base {
		params[key] = value
	}
	for key, value := range overrides {
		params[key] = value
	}

	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value, err := expand(params[key], params)
		if err != nil {
			return nil, fmt.Errorf("resolve param %q: %w", key, err)
		}
		params[key] = value
	}

	return params, nil
}

func expandMap(input map[string]string, params map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(input))
	for _, key := range keys {
		value, err := expand(input[key], params)
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}

func expand(input string, params map[string]string) (string, error) {
	if input == "" {
		return "", nil
	}
	return tpl.ExpandString(input, params)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func effectiveOnExit(values ...string) string {
	for _, value := range values {
		if normalized := config.NormalizeOnExit(value); normalized != "" {
			return normalized
		}
	}
	return config.OnExitShell
}

func cleanPath(value string) string {
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if value == "~" {
				value = home
			} else if strings.HasPrefix(value, "~/") {
				value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
			}
		}
	}
	value = filepath.Clean(value)
	if filepath.IsAbs(value) {
		return value
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return value
	}
	return absolute
}

func mergeInto(dst map[string]string, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func cloneMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func commandFallback(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return "Pane"
	}
	if idx := strings.IndexAny(command, " \t"); idx > 0 {
		return command[:idx]
	}
	return command
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
