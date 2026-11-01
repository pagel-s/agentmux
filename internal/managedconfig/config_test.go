package managedconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const example = `version: 2
mode: managed
session: demo
connections:
  local:
    protocol: anthropic
    base_url: http://127.0.0.1:11434
    api_key_env: MODEL_API_KEY
agents:
  lead:
    role: coordinator
    harness: command
    command: [printf, "%s", "a literal $(expression)"]
  worker:
    harness: claude
    connection: local
    sandbox: bubblewrap
    env:
      EXAMPLE: literal-value
boards:
  common:
    readers: ["*"]
    writers: [lead]
memory:
  project:
    readers: [lead, worker]
    writers: [lead]
policy:
  default: deny
  grants:
    - from: [lead]
      to: [worker]
      actions: [message.send, ping.send, instruct]
limits:
  max_messages: 3
`

func loadText(t *testing.T, input string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "managed.yaml")
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func validConfig() Config {
	return Config{
		Version: 2,
		Mode:    "managed",
		Session: "demo",
		Agents: map[string]Agent{
			"lead":   {Harness: "command", Command: []string{"printf", "hello"}, Role: "coordinator"},
			"worker": {Harness: "claude"},
		},
	}
}

func TestLoadNormalizesAndKeepsCredentialReferencesLiteral(t *testing.T) {
	cfg, err := loadText(t, example)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents["lead"].Sandbox != "local" || cfg.Agents["worker"].Sandbox != "bubblewrap" {
		t.Fatalf("sandbox normalization: %+v", cfg.Agents)
	}
	if cfg.Agents["lead"].Network != "host" || cfg.Agents["worker"].Network != "none" {
		t.Fatalf("network normalization: %+v", cfg.Agents)
	}
	if cfg.Limits.MaxMessages != 3 || cfg.Limits.MaxMessageBytes != Defaults().MaxMessageBytes || cfg.Limits.MaxRuntimeSeconds != Defaults().MaxRuntimeSeconds {
		t.Fatalf("limits = %+v", cfg.Limits)
	}
	if cfg.Connections["local"].APIKeyEnv != "MODEL_API_KEY" {
		t.Fatal("credential reference changed")
	}
	if cfg.Agents["lead"].Command[2] != "a literal $(expression)" {
		t.Fatal("command arguments must remain literal")
	}
	if !cfg.Can("lead", "instruct", "worker") || cfg.Can("worker", "instruct", "lead") {
		t.Fatal("unexpected instruction authorization")
	}
}

func TestLoadRejectsUnknownFieldsAtEveryLevel(t *testing.T) {
	cases := map[string]string{
		"root":       example + "spawning: true\n",
		"connection": strings.Replace(example, "    protocol: anthropic", "    unexpected: true\n    protocol: anthropic", 1),
		"agent":      strings.Replace(example, "    role: coordinator", "    permissions: all\n    role: coordinator", 1),
		"board":      strings.Replace(example, "  common:\n", "  common:\n    public: true\n", 1),
		"memory":     strings.Replace(example, "  project:\n", "  project:\n    global: true\n", 1),
		"policy":     strings.Replace(example, "policy:\n", "policy:\n  allow: true\n", 1),
		"grant":      strings.Replace(example, "    - from:", "    - unexpected: true\n      from:", 1),
		"limits":     strings.Replace(example, "limits:\n", "limits:\n  unlimited: true\n", 1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadText(t, input); err == nil || !strings.Contains(err.Error(), "field ") {
				t.Fatalf("expected unknown-field error, got %v", err)
			}
		})
	}
}

func TestLoadRejectsAmbiguousOrMalformedDocuments(t *testing.T) {
	for name, input := range map[string]string{
		"empty":           "",
		"null":            "null\n",
		"sequence":        "- version: 2\n",
		"duplicate root":  example + "session: other\n",
		"duplicate agent": strings.Replace(example, "  worker:\n", "  lead:\n", 1),
		"second document": example + "---\n" + example,
		"empty document":  example + "---\n",
		"wrong type":      strings.Replace(example, "max_messages: 3", "max_messages: \"three\"", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadText(t, input); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("expected read error")
	}
}

func TestValidationFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Config)
	}{
		{"version", func(c *Config) { c.Version = 1 }},
		{"mode", func(c *Config) { c.Mode = "workspace" }},
		{"session path", func(c *Config) { c.Session = "../demo" }},
		{"session length", func(c *Config) { c.Session = strings.Repeat("a", 65) }},
		{"no agents", func(c *Config) { c.Agents = nil }},
		{"wildcard agent", func(c *Config) { c.Agents["*"] = Agent{Harness: "pi"} }},
		{"unknown harness", func(c *Config) { c.Agents["worker"] = Agent{Harness: "gemini-cli"} }},
		{"unknown sandbox", func(c *Config) { c.Agents["worker"] = Agent{Harness: "claude", Sandbox: "container"} }},
		{"unknown network", func(c *Config) {
			c.Agents["worker"] = Agent{Harness: "claude", Sandbox: "bubblewrap", Network: "allowlist"}
		}},
		{"unsupported local isolation", func(c *Config) {
			c.Agents["worker"] = Agent{Harness: "claude", Sandbox: "local", Network: "none"}
		}},
		{"missing argv", func(c *Config) { c.Agents["worker"] = Agent{Harness: "command"} }},
		{"empty executable", func(c *Config) { c.Agents["worker"] = Agent{Harness: "command", Command: []string{""}} }},
		{"nul argv", func(c *Config) { c.Agents["worker"] = Agent{Harness: "command", Command: []string{"echo", "\x00"}} }},
		{"unknown connection", func(c *Config) { c.Agents["worker"] = Agent{Harness: "pi", Connection: "absent"} }},
		{"protocol", func(c *Config) { c.Connections = map[string]Connection{"x": {Protocol: "magic"}} }},
		{"URL scheme", func(c *Config) {
			c.Connections = map[string]Connection{"x": {Protocol: "ollama", BaseURL: "file:///tmp/model"}}
		}},
		{"URL host", func(c *Config) {
			c.Connections = map[string]Connection{"x": {Protocol: "ollama", BaseURL: "http:///path"}}
		}},
		{"URL credentials", func(c *Config) {
			c.Connections = map[string]Connection{"x": {Protocol: "ollama", BaseURL: "https://secret@example.com"}}
		}},
		{"credential env", func(c *Config) {
			c.Connections = map[string]Connection{"x": {Protocol: "ollama", APIKeyEnv: "${SECRET}"}}
		}},
		{"agent env", func(c *Config) { c.Agents["worker"] = Agent{Harness: "pi", Env: map[string]string{"A=B": "value"}} }},
		{"board ID", func(c *Config) { c.Boards = map[string]Access{"team/private": {Readers: []string{"lead"}}} }},
		{"board reader", func(c *Config) { c.Boards = map[string]Access{"board": {Readers: []string{"absent"}}} }},
		{"memory writer", func(c *Config) { c.Memory = map[string]Access{"memory": {Writers: []string{"absent"}}} }},
		{"allow default", func(c *Config) { c.Policy.Default = "allow" }},
		{"empty grant", func(c *Config) { c.Policy.Grants = []Grant{{}} }},
		{"unknown selector", func(c *Config) {
			c.Policy.Grants = []Grant{{From: []string{"absent"}, To: []string{"worker"}, Actions: []string{"instruct"}}}
		}},
		{"partial wildcard", func(c *Config) {
			c.Policy.Grants = []Grant{{From: []string{"lead*"}, To: []string{"worker"}, Actions: []string{"instruct"}}}
		}},
		{"unsupported action", func(c *Config) {
			c.Policy.Grants = []Grant{{From: []string{"lead"}, To: []string{"worker"}, Actions: []string{"spawn"}}}
		}},
		{"negative messages", func(c *Config) { c.Limits.MaxMessages = -1 }},
		{"negative bytes", func(c *Config) { c.Limits.MaxMessageBytes = -1 }},
		{"negative tasks", func(c *Config) { c.Limits.MaxTasks = -1 }},
		{"negative records", func(c *Config) { c.Limits.MaxRecords = -1 }},
		{"negative requests", func(c *Config) { c.Limits.MaxRequests = -1 }},
		{"oversized message", func(c *Config) { c.Limits.MaxMessageBytes = 1024*1024 + 1 }},
		{"too many records", func(c *Config) { c.Limits.MaxRecords = 1000001 }},
		{"too many requests", func(c *Config) { c.Limits.MaxRequests = 1000001 }},
		{"reserved memory", func(c *Config) { c.Memory = map[string]Access{"private": {Readers: []string{"lead"}}} }},
		{"empty readonly path", func(c *Config) { c.Agents["worker"] = Agent{Harness: "pi", ReadOnlyPaths: []string{""}} }},
		{"nul readonly path", func(c *Config) { c.Agents["worker"] = Agent{Harness: "pi", ReadOnlyPaths: []string{"a\x00b"}} }},
		{"negative runtime", func(c *Config) { c.Limits.MaxRuntimeSeconds = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.change(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestNormalizedDoesNotMutateSourceAndValidationAcceptsDefaults(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	normalized := cfg.Normalized()
	if cfg.Agents["lead"].Sandbox != "" || cfg.Policy.Default != "" || cfg.Limits.MaxMessages != 0 {
		t.Fatal("source config mutated")
	}
	if normalized.Policy.Default != "deny" || normalized.Limits != Defaults() || normalized.Agents["lead"].Sandbox != "local" {
		t.Fatalf("unexpected normalized config: %+v", normalized)
	}
}

func TestHarnessConnectionCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, harness, protocol, baseURL, model string
		wantError                               bool
	}{
		{"claude native", "claude", "", "", "", false},
		{"claude anthropic", "claude", "anthropic", "https://example.com/v1", "model", false},
		{"claude wrong protocol", "claude", "openai-compatible", "https://example.com/v1", "model", true},
		{"codex native", "codex", "", "", "model", false},
		{"codex openai", "codex", "openai-responses", "", "model", false},
		{"codex custom endpoint", "codex", "openai-responses", "https://example.com/v1", "model", false},
		{"codex wrong protocol", "codex", "anthropic", "", "model", true},
		{"antigravity native", "antigravity", "", "", "model", false},
		{"antigravity connection", "antigravity", "openai-responses", "", "", true},
		{"command native", "command", "", "", "", false},
		{"command connection", "command", "ollama", "http://localhost:11434/v1", "", true},
		{"command model", "command", "", "", "model", true},
		{"pi native", "pi", "", "", "", false},
		{"pi compatible", "pi", "openai-compatible", "https://example.com/v1", "model", false},
		{"pi ollama", "pi", "ollama", "http://localhost:11434/v1", "model", false},
		{"pi missing model", "pi", "ollama", "http://localhost:11434/v1", "", true},
		{"pi missing endpoint", "pi", "ollama", "", "model", true},
		{"pi wrong protocol", "pi", "anthropic", "https://example.com/v1", "model", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			agent := Agent{Harness: tc.harness, Command: []string{"owner-harness", ""}, Model: tc.model}
			if tc.protocol != "" {
				agent.Connection = "endpoint"
				cfg.Connections = map[string]Connection{"endpoint": {Protocol: tc.protocol, BaseURL: tc.baseURL}}
			}
			cfg.Agents["worker"] = agent
			if err := cfg.Validate(); (err != nil) != tc.wantError {
				t.Fatalf("Validate = %v, want error %v", err, tc.wantError)
			}
		})
	}
}

func TestNormalizedDeepClonesEveryMutableField(t *testing.T) {
	cfg := validConfig()
	agent := cfg.Agents["lead"]
	agent.Env = map[string]string{"EXPLICIT": "original"}
	agent.ReadOnlyPaths = []string{"/some/package"}
	cfg.Agents["lead"] = agent
	cfg.Connections = map[string]Connection{"model": {Protocol: "ollama"}}
	cfg.Boards = map[string]Access{"board": {Readers: []string{"lead"}, Writers: []string{"worker"}}}
	cfg.Memory = map[string]Access{"project": {Readers: []string{"lead"}, Writers: []string{"worker"}}}
	cfg.Policy.Grants = []Grant{{From: []string{"lead"}, To: []string{"worker"}, Actions: []string{"instruct"}}}
	before, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	clone := cfg.Normalized()
	clone.Agents["lead"].Env["EXPLICIT"] = "changed"
	clone.Agents["lead"].Command[0] = "changed"
	clone.Agents["lead"].ReadOnlyPaths[0] = "/changed/package"
	clone.Agents["extra"] = Agent{Harness: "pi"}
	clone.Connections["model"] = Connection{Protocol: "anthropic"}
	clone.Boards["board"].Readers[0] = "worker"
	clone.Boards["board"].Writers[0] = "lead"
	clone.Boards["extra"] = Access{}
	clone.Memory["project"].Readers[0] = "worker"
	clone.Memory["project"].Writers[0] = "lead"
	clone.Memory["extra"] = Access{}
	clone.Policy.Grants[0].From[0] = "worker"
	clone.Policy.Grants[0].To[0] = "lead"
	clone.Policy.Grants[0].Actions[0] = "message.send"
	clone.Policy.Grants = append(clone.Policy.Grants, Grant{Actions: []string{"*"}})
	after, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("normalization shares mutable configuration: before %s, after %s", before, after)
	}
}

func TestRuntimeDurationReservesLeaseGrace(t *testing.T) {
	maximum := int64((1<<63-1)/1000000000 - 15)
	if int64(int(maximum)) != maximum {
		t.Skip("duration overflow boundary only representable on 64-bit platforms")
	}
	cfg := validConfig()
	cfg.Limits.MaxRuntimeSeconds = int(maximum)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Limits.MaxRuntimeSeconds++
	if err := cfg.Validate(); err == nil {
		t.Fatal("runtime duration must leave 15 seconds for lease grace")
	}
}

func TestBubblewrapHostNetworkIsExplicitlySupported(t *testing.T) {
	cfg := validConfig()
	cfg.Agents["worker"] = Agent{Harness: "pi", Sandbox: "bubblewrap", Network: "host"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Normalized().Agents["worker"].Network; got != "host" {
		t.Fatalf("explicit network mode changed to %q", got)
	}
}
