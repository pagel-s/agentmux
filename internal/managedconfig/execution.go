package managedconfig

import (
	"fmt"
	"strings"
)

func validateExecution(id string, a Agent, connection Connection) error {
	switch a.CodexSandbox {
	case "", "native":
	case "external":
		if a.Harness != "codex" || a.Sandbox != "bubblewrap" {
			return fmt.Errorf("agent %q: codex_sandbox external requires the Codex harness inside bubblewrap", id)
		}
	default:
		return fmt.Errorf("agent %q: codex_sandbox must be native or external", id)
	}
	if a.CodexSandbox != "" && a.Harness != "codex" {
		return fmt.Errorf("agent %q: codex_sandbox applies only to Codex", id)
	}
	if a.Auth.File != "" || a.Auth.Provider != "" {
		if a.Sandbox != "bubblewrap" {
			return fmt.Errorf("agent %q: auth files require bubblewrap isolation", id)
		}
		if a.Auth.File == "" || strings.ContainsRune(a.Auth.File, 0) {
			return fmt.Errorf("agent %q: auth.file requires a nonempty path without NUL bytes", id)
		}
		if connection.BaseURL != "" || connection.APIKeyEnv != "" {
			return fmt.Errorf("agent %q: select native auth.file or a connection credential, not both", id)
		}
		switch a.Harness {
		case "claude", "codex":
			if a.Auth.Provider != "" {
				return fmt.Errorf("agent %q: auth.provider applies only to Pi", id)
			}
		case "pi":
			if !idPattern.MatchString(a.Auth.Provider) {
				return fmt.Errorf("agent %q: Pi auth requires an exact auth.provider", id)
			}
		default:
			return fmt.Errorf("agent %q: native auth files are supported for Claude, Codex, and Pi", id)
		}
		for _, name := range []string{"HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "PI_CODING_AGENT_DIR", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY"} {
			if _, exists := a.Env[name]; exists {
				return fmt.Errorf("agent %q: auth.file cannot be combined with environment override %s", id, name)
			}
		}
	}
	if a.Resources != (Resources{}) && a.Sandbox != "bubblewrap" {
		return fmt.Errorf("agent %q: enforced resources require bubblewrap isolation", id)
	}
	r := a.Resources
	for _, bound := range []struct {
		name        string
		n, min, max int64
	}{
		{"memory_bytes", r.MemoryBytes, 64 << 20, 1 << 40},
		{"cpu_percent", int64(r.CPUPercent), 1, 6400},
		{"processes", int64(r.Processes), 2, 65536},
		{"open_files", int64(r.OpenFiles), 16, 1 << 20},
		{"cpu_seconds", int64(r.CPUSeconds), 1, 86400},
		{"address_space_bytes", r.AddressSpaceBytes, 64 << 20, 8 << 40},
	} {
		if bound.n != 0 && (bound.n < bound.min || bound.n > bound.max) {
			return fmt.Errorf("agent %q: resources.%s must be zero or between %d and %d", id, bound.name, bound.min, bound.max)
		}
	}
	return nil
}
