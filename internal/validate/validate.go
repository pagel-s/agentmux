package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"agentmux/internal/config"
	tpl "agentmux/internal/template"
)

type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

var allowedLayouts = map[string]struct{}{
	"":                {},
	"even-horizontal": {},
	"even-vertical":   {},
	"main-horizontal": {},
	"main-vertical":   {},
	"tiled":           {},
	"grid":            {},
}

var paneNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Issue struct {
	Severity Severity
	Message  string
}

func Document(doc *config.Document, cliParams map[string]string) []Issue {
	var issues []Issue
	params := mergedParams(doc.Config.Params, cliParams)
	params["config_dir"] = "/injected" // Built-in param during resolution

	if doc.Config.Version != 1 {
		issues = append(issues, errorf("version must exist and equal 1"))
	}
	if strings.TrimSpace(doc.Config.Session) == "" {
		issues = append(issues, errorf("session must exist"))
	}
	if len(doc.Config.Layout) == 0 {
		issues = append(issues, errorf("layout must contain at least one window"))
	}

	for _, field := range doc.UnknownTopLevel {
		issues = append(issues, warningf("unknown top-level field %q", field))
	}
	for _, key := range doc.DuplicateAgentIDs {
		issues = append(issues, errorf("duplicate agent name %q", key))
	}

	windowNames := make(map[string]int)
	duplicateTitles := make(map[string]struct{})
	agents := doc.Config.Agents
	if agents == nil {
		agents = map[string]config.Agent{}
	}

	for name, agent := range agents {
		provider := strings.ToLower(strings.TrimSpace(agent.Provider))
		if provider == "" {
			issues = append(issues, errorf("agent %q is missing provider", name))
		}
		switch provider {
		case "command", "shell", "ssh", "claude", "claude-cli", "gemini", "gemini-cli", "openai", "aichat", "gptme":
		case "":
		default:
			issues = append(issues, errorf("agent %q has unsupported provider %q", name, agent.Provider))
		}
		if provider == "command" || provider == "shell" || provider == "" {
			if strings.TrimSpace(agent.Command) == "" {
				issues = append(issues, errorf("agent %q has an empty command", name))
			}
		}
		if onExit := resolvedOnExit(agent.OnExit, params); onExit != "" && !config.IsValidOnExit(onExit) {
			issues = append(issues, errorf("agent %q has unsupported on_exit %q; use shell, remain, restart, or kill", name, onExit))
		}
		if agent.Persona != "" {
			personaPath, _ := tpl.ExpandString(agent.Persona, params)
			if personaPath != "" && !filepath.IsAbs(personaPath) && !strings.HasPrefix(personaPath, "~") && personaPath != "/injected" {
				personaPath = filepath.Join(filepath.Dir(doc.Path), personaPath)
			}
			if personaPath != "" && personaPath != "/injected" {
				if _, err := os.Stat(personaPath); err != nil {
					issues = append(issues, errorf("agent %q persona file not found: %s", name, personaPath))
				}
			}
		}
	}

	focusCount := 0
	paneNames := make(map[string]string)
	for windowIndex, window := range doc.Config.Layout {
		if strings.TrimSpace(window.Window) == "" {
			issues = append(issues, errorf("window %d is missing window name", windowIndex+1))
		}
		if len(window.Panes) == 0 {
			issues = append(issues, errorf("window %q must contain at least one pane", window.Window))
		}
		if _, ok := allowedLayouts[strings.TrimSpace(window.Layout)]; !ok {
			issues = append(issues, errorf("window %q has unsupported layout %q", window.Window, window.Layout))
		}
		if window.Layout == "grid" {
			if window.GridRows <= 0 || window.GridColumns <= 0 {
				issues = append(issues, errorf("window %q uses layout %q and requires positive grid_rows and grid_columns", window.Window, window.Layout))
			}
			if window.GridRows > 0 && window.GridColumns > 0 && len(window.Panes) != window.GridRows*window.GridColumns {
				issues = append(issues, errorf("window %q grid %dx%d requires exactly %d panes, got %d", window.Window, window.GridRows, window.GridColumns, window.GridRows*window.GridColumns, len(window.Panes)))
			}
		} else if window.GridRows != 0 || window.GridColumns != 0 {
			issues = append(issues, errorf("window %q sets grid_rows/grid_columns but layout is %q; use layout %q for exact grids", window.Window, window.Layout, "grid"))
		}
		windowNames[window.Window]++

		localTitles := make(map[string]int)
		for paneIndex, pane := range window.Panes {
			paneName := resolvedPaneName(pane.Name, params)
			if paneName != "" {
				if !paneNamePattern.MatchString(paneName) {
					issues = append(issues, errorf("pane %d in window %q has invalid name %q; use letters, numbers, dot, dash, or underscore", paneIndex+1, window.Window, paneName))
				}
				if existingWindow, ok := paneNames[paneName]; ok {
					issues = append(issues, errorf("pane name %q is duplicated across windows %q and %q", paneName, existingWindow, window.Window))
				} else {
					paneNames[paneName] = window.Window
				}
			}
			if pane.Agent == "" && pane.Command == "" && pane.Provider == "" {
				issues = append(issues, errorf("pane %d in window %q must specify exactly one of agent, command, or provider", paneIndex+1, window.Window))
				continue
			}
			if pane.Agent != "" && (pane.Command != "" || pane.Provider != "") {
				issues = append(issues, errorf("pane %d in window %q specifies multiple sources; use exactly one of agent, command, or provider", paneIndex+1, window.Window))
			}
			if pane.Agent != "" {
				if _, ok := agents[pane.Agent]; !ok {
					issues = append(issues, errorf("pane %d in window %q references unknown agent %q", paneIndex+1, window.Window, pane.Agent))
				}
			}
			if pane.Provider != "" {
				provider := strings.ToLower(strings.TrimSpace(pane.Provider))
				switch provider {
				case "command", "shell", "ssh", "claude", "claude-cli", "gemini", "gemini-cli", "openai", "aichat", "gptme":
				default:
					issues = append(issues, errorf("pane %d in window %q has unsupported provider %q", paneIndex+1, window.Window, pane.Provider))
				}
			}
			if pane.Command != "" && strings.TrimSpace(pane.Command) == "" {
				issues = append(issues, errorf("pane %d in window %q has an empty command", paneIndex+1, window.Window))
			}
			if onExit := resolvedOnExit(pane.OnExit, params); onExit != "" && !config.IsValidOnExit(onExit) {
				issues = append(issues, errorf("pane %d in window %q has unsupported on_exit %q; use shell, remain, restart, or kill", paneIndex+1, window.Window, onExit))
			}
			if pane.Persona != "" {
				personaPath, _ := tpl.ExpandString(pane.Persona, params)
				if personaPath != "" && !filepath.IsAbs(personaPath) && !strings.HasPrefix(personaPath, "~") && personaPath != "/injected" {
					personaPath = filepath.Join(filepath.Dir(doc.Path), personaPath)
				}
				if personaPath != "" && personaPath != "/injected" {
					if _, err := os.Stat(personaPath); err != nil {
						issues = append(issues, errorf("pane %d in window %q persona file not found: %s", paneIndex+1, window.Window, personaPath))
					}
				}
			}
			if pane.Focus {
				focusCount++
			}
			if pane.Title != "" {
				localTitles[pane.Title]++
			}
		}
		for title, count := range localTitles {
			if count > 1 {
				duplicateTitles[title] = struct{}{}
			}
		}
	}

	for name, count := range windowNames {
		if count > 1 {
			issues = append(issues, errorf("window name %q is duplicated", name))
		}
	}
	for title := range duplicateTitles {
		issues = append(issues, warningf("duplicate pane title %q", title))
	}
	if focusCount > 1 {
		issues = append(issues, warningf("multiple panes request focus; the last created pane will win"))
	}

	for _, req := range doc.Config.Requires.Params {
		if _, ok := params[req]; !ok {
			issues = append(issues, errorf("missing required param %q", req))
		}
	}

	for _, ref := range collectConfigRefs(&doc.Config) {
		if _, ok := params[ref]; !ok {
			issues = append(issues, errorf("missing required param %q", ref))
		}
	}

	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Severity == issues[j].Severity {
			return issues[i].Message < issues[j].Message
		}
		return issues[i].Severity < issues[j].Severity
	})

	return issues
}

func HasErrors(issues []Issue) bool {
	for _, issue := range issues {
		if issue.Severity == SeverityError {
			return true
		}
	}
	return false
}

func HasWarnings(issues []Issue) bool {
	for _, issue := range issues {
		if issue.Severity == SeverityWarning {
			return true
		}
	}
	return false
}

func collectConfigRefs(cfg *config.Config) []string {
	seen := make(map[string]struct{})
	add := func(value string) {
		for _, ref := range tpl.CollectRefs(value) {
			seen[ref] = struct{}{}
		}
	}

	add(cfg.Name)
	add(cfg.Session)
	add(cfg.Description)
	for _, value := range cfg.Params {
		add(value)
	}
	for _, value := range cfg.Env {
		add(value)
	}
	for _, agent := range cfg.Agents {
		add(agent.Provider)
		add(agent.Command)
		add(agent.Persona)
		add(agent.Model)
		add(agent.Cwd)
		add(agent.Title)
		add(agent.Shell)
		add(agent.OnExit)
		for _, value := range agent.Env {
			add(value)
		}
	}
	for _, window := range cfg.Layout {
		add(window.Window)
		add(window.Root)
		add(window.Layout)
		for _, value := range window.Env {
			add(value)
		}
		for _, pane := range window.Panes {
			add(pane.Name)
			add(pane.Agent)
			add(pane.Provider)
			add(pane.Command)
			add(pane.Persona)
			add(pane.Model)
			add(pane.Title)
			add(pane.Cwd)
			add(pane.OnExit)
			for _, value := range pane.Env {
				add(value)
			}
		}
	}
	add(cfg.Hooks.PreSession)
	add(cfg.Hooks.PostSession)

	refs := make([]string, 0, len(seen))
	for ref := range seen {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

func mergedParams(base map[string]string, overrides map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overrides))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range overrides {
		out[key] = value
	}
	return out
}

func resolvedPaneName(raw string, params map[string]string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}

	expanded, err := tpl.ExpandString(value, params)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(expanded)
}

func resolvedOnExit(raw string, params map[string]string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}

	expanded, err := tpl.ExpandString(value, params)
	if err != nil {
		return ""
	}
	return config.NormalizeOnExit(expanded)
}

func errorf(format string, args ...any) Issue {
	return Issue{
		Severity: SeverityError,
		Message:  fmt.Sprintf(format, args...),
	}
}

func warningf(format string, args ...any) Issue {
	return Issue{
		Severity: SeverityWarning,
		Message:  fmt.Sprintf(format, args...),
	}
}
