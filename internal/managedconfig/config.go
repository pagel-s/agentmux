// Package managedconfig defines the deliberately separate, fail-closed v2
// managed configuration. It does not reinterpret legacy workspace YAML.
package managedconfig

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version     int                   `yaml:"version" json:"version"`
	Mode        string                `yaml:"mode" json:"mode"`
	Session     string                `yaml:"session" json:"session"`
	Connections map[string]Connection `yaml:"connections" json:"connections,omitempty"`
	Agents      map[string]Agent      `yaml:"agents" json:"agents"`
	Boards      map[string]Access     `yaml:"boards" json:"boards,omitempty"`
	Memory      map[string]Access     `yaml:"memory" json:"memory,omitempty"`
	Policy      Policy                `yaml:"policy" json:"policy"`
	Limits      Limits                `yaml:"limits" json:"limits"`
	Spawn       Spawn                 `yaml:"spawn" json:"spawn"`
	Teams       Teams                 `yaml:"teams" json:"teams"`
}

type Connection struct {
	Protocol  string `yaml:"protocol" json:"protocol"`
	BaseURL   string `yaml:"base_url" json:"base_url,omitempty"`
	APIKeyEnv string `yaml:"api_key_env" json:"api_key_env,omitempty"`
}

type Agent struct {
	Role          string            `yaml:"role" json:"role,omitempty"`
	Harness       string            `yaml:"harness" json:"harness"`
	Model         string            `yaml:"model" json:"model,omitempty"`
	Connection    string            `yaml:"connection" json:"connection,omitempty"`
	Sandbox       string            `yaml:"sandbox" json:"sandbox"`
	Network       string            `yaml:"network" json:"network"`
	Command       []string          `yaml:"command" json:"command,omitempty"`
	Workdir       string            `yaml:"workdir" json:"workdir,omitempty"`
	Env           map[string]string `yaml:"env" json:"env,omitempty"`
	ReadOnlyPaths []string          `yaml:"read_only_paths" json:"read_only_paths,omitempty"`
	Auth          NativeAuth        `yaml:"auth" json:"auth,omitempty"`
	Resources     Resources         `yaml:"resources" json:"resources,omitempty"`
	CodexSandbox  string            `yaml:"codex_sandbox" json:"codex_sandbox,omitempty"`
}

// NativeAuth selects one owner-provided credential file, never a home or a
// native configuration directory. A private, filtered snapshot is supplied to
// a bubblewrap worker; the original file is never writable from that worker.
type NativeAuth struct {
	File     string `yaml:"file" json:"file,omitempty"`
	Provider string `yaml:"provider" json:"provider,omitempty"`
}

// Resources are optional enforced Linux process-tree limits. Zero leaves a
// particular limit unset; configured limits never silently degrade to hints.
type Resources struct {
	MemoryBytes       int64 `yaml:"memory_bytes" json:"memory_bytes,omitempty"`
	CPUPercent        int   `yaml:"cpu_percent" json:"cpu_percent,omitempty"`
	Processes         int   `yaml:"processes" json:"processes,omitempty"`
	OpenFiles         int   `yaml:"open_files" json:"open_files,omitempty"`
	CPUSeconds        int   `yaml:"cpu_seconds" json:"cpu_seconds,omitempty"`
	AddressSpaceBytes int64 `yaml:"address_space_bytes" json:"address_space_bytes,omitempty"`
}

// Access grants exact namespace access. Empty lists grant no agent access.
// A literal "*" explicitly grants access to every configured agent.
type Access struct {
	Readers []string `yaml:"readers" json:"readers,omitempty"`
	Writers []string `yaml:"writers" json:"writers,omitempty"`
}

type Policy struct {
	Default string  `yaml:"default" json:"default"`
	Grants  []Grant `yaml:"grants" json:"grants,omitempty"`
}

type Grant struct {
	From    []string `yaml:"from" json:"from"`
	To      []string `yaml:"to" json:"to"`
	Actions []string `yaml:"actions" json:"actions"`
}

type Limits struct {
	MaxMessages        int `yaml:"max_messages" json:"max_messages"`
	MaxMessageBytes    int `yaml:"max_message_bytes" json:"max_message_bytes"`
	MaxTasks           int `yaml:"max_tasks" json:"max_tasks"`
	MaxRecords         int `yaml:"max_records" json:"max_records"`
	MaxRequests        int `yaml:"max_requests" json:"max_requests"`
	MaxRuntimeSeconds  int `yaml:"max_runtime_seconds" json:"max_runtime_seconds"`
	MaxConcurrentTasks int `yaml:"max_concurrent_tasks" json:"max_concurrent_tasks"`
	MaxSpawns          int `yaml:"max_spawns" json:"max_spawns"`
	MaxSpawnDepth      int `yaml:"max_spawn_depth" json:"max_spawn_depth"`
	MaxTeams           int `yaml:"max_teams" json:"max_teams"`
	MaxTeamMembers     int `yaml:"max_team_members" json:"max_team_members"`
}

// Defaults supplies finite small-team limits. These bound runtime resources,
// not provider spending, which a harness may not report or expose to control.
func Defaults() Limits {
	return Limits{
		MaxMessages:        10000,
		MaxMessageBytes:    65536,
		MaxTasks:           1000,
		MaxRecords:         30000,
		MaxRequests:        50000,
		MaxRuntimeSeconds:  3600,
		MaxConcurrentTasks: 8, MaxSpawns: 32, MaxSpawnDepth: 2, MaxTeams: 32, MaxTeamMembers: 32,
	}
}

// Normalized returns a copy with safe defaults. Local sandboxes are explicitly
// trusted processes, not an isolation boundary. No permissions are added.
func (c Config) Normalized() Config {
	if c.Teams.History == "" {
		c.Teams.History = "since-join"
	}
	if c.Policy.Default == "" {
		c.Policy.Default = "deny"
	}
	if c.Agents != nil {
		agents := make(map[string]Agent, len(c.Agents))
		for id, agent := range c.Agents {
			agent.Command = append([]string(nil), agent.Command...)
			agent.Env = cloneMap(agent.Env)
			agent.ReadOnlyPaths = append([]string(nil), agent.ReadOnlyPaths...)
			if agent.Sandbox == "" {
				agent.Sandbox = "local"
			}
			if agent.Network == "" {
				agent.Network = "host"
				if agent.Sandbox == "bubblewrap" {
					agent.Network = "none"
				}
			}
			agents[id] = agent
		}
		c.Agents = agents
	}
	c.Connections = cloneMap(c.Connections)
	templates := make(map[string]Agent, len(c.Spawn.Templates))
	for id, a := range c.Spawn.Templates {
		a.Command = append([]string(nil), a.Command...)
		a.Env = cloneMap(a.Env)
		a.ReadOnlyPaths = append([]string(nil), a.ReadOnlyPaths...)
		templates[id] = a
	}
	c.Spawn.Templates = templates
	allow := make(map[string][]string, len(c.Spawn.Allow))
	for id, v := range c.Spawn.Allow {
		allow[id] = append([]string(nil), v...)
	}
	c.Spawn.Allow = allow
	c.Teams.Creators = append([]string(nil), c.Teams.Creators...)
	c.Boards = cloneAccess(c.Boards)
	c.Memory = cloneAccess(c.Memory)
	grants := make([]Grant, len(c.Policy.Grants))
	for i, grant := range c.Policy.Grants {
		grant.From = append([]string(nil), grant.From...)
		grant.To = append([]string(nil), grant.To...)
		grant.Actions = append([]string(nil), grant.Actions...)
		grants[i] = grant
	}
	c.Policy.Grants = grants
	d := Defaults()
	if c.Limits.MaxConcurrentTasks == 0 {
		c.Limits.MaxConcurrentTasks = d.MaxConcurrentTasks
	}
	if c.Limits.MaxSpawns == 0 {
		c.Limits.MaxSpawns = d.MaxSpawns
	}
	if c.Limits.MaxSpawnDepth == 0 {
		c.Limits.MaxSpawnDepth = d.MaxSpawnDepth
	}
	if c.Limits.MaxTeams == 0 {
		c.Limits.MaxTeams = d.MaxTeams
	}
	if c.Limits.MaxTeamMembers == 0 {
		c.Limits.MaxTeamMembers = d.MaxTeamMembers
	}
	if c.Limits.MaxMessages == 0 {
		c.Limits.MaxMessages = d.MaxMessages
	}
	if c.Limits.MaxMessageBytes == 0 {
		c.Limits.MaxMessageBytes = d.MaxMessageBytes
	}
	if c.Limits.MaxTasks == 0 {
		c.Limits.MaxTasks = d.MaxTasks
	}
	if c.Limits.MaxRecords == 0 {
		c.Limits.MaxRecords = d.MaxRecords
	}
	if c.Limits.MaxRequests == 0 {
		c.Limits.MaxRequests = d.MaxRequests
	}
	if c.Limits.MaxRuntimeSeconds == 0 {
		c.Limits.MaxRuntimeSeconds = d.MaxRuntimeSeconds
	}
	return c
}

func cloneMap[V any](source map[string]V) map[string]V {
	if source == nil {
		return nil
	}
	result := make(map[string]V, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneAccess(source map[string]Access) map[string]Access {
	result := cloneMap(source)
	for key, access := range result {
		access.Readers = append([]string(nil), access.Readers...)
		access.Writers = append([]string(nil), access.Writers...)
		result[key] = access
	}
	return result
}

// Load accepts exactly one YAML mapping, rejects unknown fields at every
// nesting level, applies defaults, and validates all references. It never
// expands environment variables or reads provider credentials.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("read managed config: %w", err)
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	var c Config
	if err := decoder.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("decode managed config: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return Config{}, fmt.Errorf("decode managed config: %w", err)
		}
		return Config{}, fmt.Errorf("managed config must contain exactly one YAML document")
	}
	c = c.Normalized()
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

var (
	idPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
	envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Validate checks a normalized copy, allowing programmatic callers to omit
// defaulted fields. Runtime constructors should retain c.Normalized().
func (c Config) Validate() error {
	c = c.Normalized()
	if c.Version != 2 {
		return fmt.Errorf("managed config version must be 2")
	}
	if c.Mode != "managed" {
		return fmt.Errorf("managed config mode must be managed")
	}
	if !idPattern.MatchString(c.Session) {
		return invalidID("session", c.Session)
	}
	if c.Policy.Default != "deny" {
		return fmt.Errorf("policy.default must be deny")
	}
	if len(c.Agents) == 0 {
		return fmt.Errorf("managed config requires at least one agent")
	}
	if err := c.validateDynamic(); err != nil {
		return err
	}
	for _, id := range sortedKeys(c.Connections) {
		if !idPattern.MatchString(id) {
			return invalidID("connection", id)
		}
		connection := c.Connections[id]
		switch connection.Protocol {
		case "anthropic", "openai-responses", "openai-compatible", "ollama":
		default:
			return fmt.Errorf("connection %q: unsupported protocol %q", id, connection.Protocol)
		}
		if connection.BaseURL != "" {
			u, err := url.Parse(connection.BaseURL)
			if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("connection %q: base_url must be an http(s) URL with a host and without credentials, query, or fragment", id)
			}
		}
		if connection.APIKeyEnv != "" && !envPattern.MatchString(connection.APIKeyEnv) {
			return fmt.Errorf("connection %q: invalid api_key_env name", id)
		}
	}
	for _, id := range sortedKeys(c.Agents) {
		if !idPattern.MatchString(id) {
			return invalidID("agent", id)
		}
		agent := c.Agents[id]
		encoded, _ := json.Marshal(agent)
		if len(encoded) > 512*1024 {
			return fmt.Errorf("agent %q: process definition exceeds 512 KiB", id)
		}
		switch agent.Harness {
		case "command", "claude", "codex", "antigravity", "pi":
		default:
			return fmt.Errorf("agent %q: unsupported harness %q", id, agent.Harness)
		}
		switch agent.Sandbox {
		case "local", "bubblewrap":
		default:
			return fmt.Errorf("agent %q: unsupported sandbox %q", id, agent.Sandbox)
		}
		if agent.Network != "none" && agent.Network != "host" {
			return fmt.Errorf("agent %q: unsupported network %q", id, agent.Network)
		}
		if agent.Sandbox == "local" && agent.Network != "host" {
			return fmt.Errorf("agent %q: local sandbox cannot enforce network isolation; network must be host", id)
		}
		if agent.Connection != "" {
			if _, ok := c.Connections[agent.Connection]; !ok {
				return fmt.Errorf("agent %q: unknown connection %q", id, agent.Connection)
			}
		}
		if err := validateCompatibility(id, agent, c.Connections[agent.Connection]); err != nil {
			return err
		}
		if err := validateExecution(id, agent, c.Connections[agent.Connection]); err != nil {
			return err
		}
		if agent.Harness == "command" && len(agent.Command) == 0 {
			return fmt.Errorf("agent %q: command harness requires command argv", id)
		}
		for i, arg := range agent.Command {
			if strings.ContainsRune(arg, '\x00') || (i == 0 && strings.TrimSpace(arg) == "") {
				return fmt.Errorf("agent %q: invalid command argv element %d", id, i)
			}
		}
		if strings.ContainsRune(agent.Workdir, '\x00') {
			return fmt.Errorf("agent %q: workdir contains a NUL byte", id)
		}
		for _, name := range sortedKeys(agent.Env) {
			if !envPattern.MatchString(name) || strings.ContainsRune(agent.Env[name], '\x00') {
				return fmt.Errorf("agent %q: invalid environment entry %q", id, name)
			}
		}
		for _, path := range agent.ReadOnlyPaths {
			if path == "" || strings.ContainsRune(path, '\x00') {
				return fmt.Errorf("agent %q: read_only_paths requires nonempty paths without NUL bytes", id)
			}
		}
	}
	if err := c.validateNamespaces("board", c.Boards); err != nil {
		return err
	}
	if err := c.validateNamespaces("memory namespace", c.Memory); err != nil {
		return err
	}
	if _, reserved := c.Memory["private"]; reserved {
		return fmt.Errorf("memory namespace %q is reserved for implicit per-agent private memory", "private")
	}
	for i, grant := range c.Policy.Grants {
		if len(grant.From) == 0 || len(grant.To) == 0 || len(grant.Actions) == 0 {
			return fmt.Errorf("policy.grants[%d]: from, to, and actions must be nonempty", i)
		}
		for _, list := range [][]string{grant.From, grant.To} {
			if err := c.validateSelectors(list); err != nil {
				return fmt.Errorf("policy.grants[%d]: %w", i, err)
			}
		}
		for _, action := range grant.Actions {
			if action != "*" && !supportedAction(action) {
				return fmt.Errorf("policy.grants[%d]: unsupported action %q", i, action)
			}
		}
	}
	for _, limit := range []struct {
		name  string
		value int
	}{
		{"max_messages", c.Limits.MaxMessages},
		{"max_message_bytes", c.Limits.MaxMessageBytes},
		{"max_tasks", c.Limits.MaxTasks},
		{"max_records", c.Limits.MaxRecords},
		{"max_requests", c.Limits.MaxRequests},
		{"max_runtime_seconds", c.Limits.MaxRuntimeSeconds},
	} {
		if limit.value <= 0 {
			return fmt.Errorf("limits.%s must be positive (or omitted for the default)", limit.name)
		}
	}
	if c.Limits.MaxMessageBytes > 1024*1024 {
		return fmt.Errorf("limits.max_message_bytes must not exceed 1048576 (1 MiB)")
	}
	if c.Limits.MaxRecords > 1000000 || c.Limits.MaxRequests > 1000000 {
		return fmt.Errorf("limits.max_records and limits.max_requests must not exceed 1000000")
	}
	if len(c.Agents) > 500000 || c.Limits.MaxRecords < 2*len(c.Agents) || c.Limits.MaxRequests < 2*len(c.Agents) {
		return fmt.Errorf("limits.max_records and max_requests must each reserve at least two slots per configured agent for credential revocation")
	}
	// Runtime durations are expressed in nanoseconds. Reject overflow before
	// a caller converts the configured seconds to time.Duration.
	if int64(c.Limits.MaxRuntimeSeconds) > (1<<63-1)/1000000000-15 {
		return fmt.Errorf("limits.max_runtime_seconds exceeds the supported duration")
	}
	return nil
}

func validateCompatibility(id string, agent Agent, connection Connection) error {
	switch agent.Harness {
	case "command":
		if agent.Connection != "" || agent.Model != "" {
			return fmt.Errorf("agent %q: command harness does not interpret connection or model; configure literal command argv and env instead", id)
		}
	case "claude":
		if agent.Connection != "" && connection.Protocol != "anthropic" {
			return fmt.Errorf("agent %q: claude requires an anthropic connection", id)
		}
	case "codex":
		if agent.Connection != "" && (connection.Protocol != "openai-responses" || connection.BaseURL != "") {
			return fmt.Errorf("agent %q: codex supports only an openai-responses connection without base_url; use Pi for a custom endpoint", id)
		}
	case "pi":
		if agent.Connection != "" && ((connection.Protocol != "openai-compatible" && connection.Protocol != "ollama") || connection.BaseURL == "" || agent.Model == "") {
			return fmt.Errorf("agent %q: Pi custom connections require openai-compatible or ollama protocol, base_url, and an exact model ID", id)
		}
	case "antigravity":
		if agent.Connection != "" {
			return fmt.Errorf("agent %q: antigravity does not support an explicit connection; use its configured authentication or Pi for a custom endpoint", id)
		}
	}
	return nil
}

func (c Config) validateNamespaces(kind string, namespaces map[string]Access) error {
	for _, id := range sortedKeys(namespaces) {
		if !idPattern.MatchString(id) {
			return invalidID(kind, id)
		}
		access := namespaces[id]
		for _, list := range [][]string{access.Readers, access.Writers} {
			if err := c.validateSelectors(list); err != nil {
				return fmt.Errorf("%s %q: %w", kind, id, err)
			}
		}
	}
	return nil
}

func (c Config) validateSelectors(selectors []string) error {
	for _, selector := range selectors {
		if selector == "*" {
			continue
		}
		if _, ok := c.Agents[selector]; !ok {
			return fmt.Errorf("unknown agent selector %q", selector)
		}
	}
	return nil
}

func invalidID(kind, value string) error {
	return fmt.Errorf("invalid %s ID %q: expected a letter followed by up to 63 letters, digits, underscores, or hyphens", kind, value)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
