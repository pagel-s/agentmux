// Package harness executes one managed agent turn without typing into a terminal.
// It is a process adapter, not an isolation boundary: the caller must choose the
// sandbox, working directory, environment, and owner-controlled executable.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"agentmux/internal/modelmeta"
)

// Spec describes a process. Env contains the complete child environment.
type Spec struct {
	Name    string
	Binary  string
	Args    []string
	Env     []string
	Workdir string
	// ReadOnlyPaths are private generated resources a sandbox wrapper must
	// expose to this process. They are removed when Run returns.
	ReadOnlyPaths []string
	// HomeFiles are private generated configuration files. A namespace wrapper
	// mounts each file at its fixed private-home target, leaving its parent
	// directory writable. Local execution keeps using its generated temp dir.
	HomeFiles []HomeFile
	// SessionDirs are explicit per-agent writable transcript stores. They never
	// include authentication/config directories or another agent's history.
	SessionDirs []SessionDir
	// ExtraFiles are explicit controller-owned capabilities for an outer
	// namespace launcher. Native requests cannot name arbitrary descriptors.
	ExtraFiles   []*os.File
	conversation *Conversation
}

type HomeFile struct {
	Source, Target string
	// Descriptor is a controller-owned sealed anonymous credential capability.
	// Namespace wrappers bind that one object; no host login directory is exposed.
	Descriptor *os.File
}
type SessionDir struct{ Source, Target string }

// Request selects a harness and supplies a single fresh turn. Command is an
// owner-controlled executable and prefix argv override, never model-authored
// input. For the command harness it is the complete argv. Prompts use stdin.
// BaseURL is an Anthropic-compatible base for Claude, a Responses API base for
// Codex, or a Chat Completions API base for Pi (including /v1 when required).
// No global settings are changed.
// APIKeyEnv names a credential in Env or the worker's environment; its value is
// never placed on the command line. Env overlays the worker environment, so a
// parent environment is reduced to runtime paths and locale variables; other
// credentials must be explicitly selected. Wrap applies the caller's sandbox
// after private endpoint files have been prepared, immediately before exec.
type Request struct {
	// NativeResume is an explicit owner-selected native conversation ID, last,
	// or picker. It is only used by BuildInteractive; no history is guessed.
	NativeResume string
	Harness      string
	Model        string
	Prompt       string
	Workdir      string
	BaseURL      string
	APIKeyEnv    string
	Command      []string
	Env          map[string]string
	Wrap         func(Spec) (Spec, error)
	// ExternalSandbox is an explicit owner choice for Codex commands to rely
	// on the caller's outer isolation boundary. It requires Wrap and is never
	// automatically selected after a native sandbox failure.
	ExternalSandbox bool
	// ReapDescendants makes a headless turn terminate remaining members of its
	// Unix process group after the direct child exits. It is opt-in so ordinary
	// native/interactive lifecycles retain their existing behavior. It cannot
	// contain descendants that deliberately create another session or group.
	// Unsupported platforms reject the request before execution.
	ReapDescendants bool
	// InheritEnvironment is only honored by native interactive sessions explicitly
	// running with trusted host authority. Managed turns keep filtered environments.
	InheritEnvironment bool
	Conversation       *Conversation
	ModelMetadata      modelmeta.Metadata
	// OnEvent receives an independent copy after each bounded JSON-object frame
	// enters the partial result, before the next event is read. This includes
	// the frame causing a semantic ErrProtocol, so traces retain its evidence.
	// Malformed non-JSON is excluded. Returning an error stops the process.
	// Command harness output is returned in Result.Text rather than streamed.
	OnEvent func(json.RawMessage) error
	// AfterProcess runs once after process execution/wait has finished and
	// before native transcript verification. It can commit a private bounded
	// checkpoint. Its failure fails the turn; Build never invokes callbacks.
	AfterProcess func() error
}

// Result preserves native evidence. Text is a completed answer only when Run
// returns nil error; otherwise it is partial output and must not be promoted to
// a verified result. Events/usage do not establish semantic task correctness.
type Result struct {
	Text      string
	SessionID string
	Events    []json.RawMessage
	Usage     json.RawMessage
}

var (
	ErrOutputLimit = errors.New("harness output limit exceeded")
	ErrIncomplete  = errors.New("harness exited without a terminal result")
	ErrToolDenied  = errors.New("harness reported a tool permission denial")
	ErrProtocol    = errors.New("invalid harness protocol")
)

// Build validates a request and constructs argv. It does not create files or
// launch a process. Pi endpoint configuration is prepared privately by Run.
func Build(req Request) (Spec, error) {
	if req.ReapDescendants && !supportsDescendantReaping() {
		return Spec{}, errors.New("bounded turn descendant cleanup requires Unix process groups on this platform")
	}
	if err := req.ModelMetadata.Validate(); err != nil {
		return Spec{}, err
	}
	name := strings.ToLower(strings.TrimSpace(req.Harness))
	switch name {
	case "agy":
		name = "antigravity"
	case "claude-code":
		name = "claude"
	}
	if !req.ModelMetadata.Empty() && (name != "pi" || req.BaseURL == "") {
		return Spec{}, errors.New("model metadata overrides require a Pi compatible endpoint")
	}
	if len(req.Prompt) > maxPromptBytes {
		return Spec{}, fmt.Errorf("prompt exceeds %d bytes", maxPromptBytes)
	}
	if req.ExternalSandbox && (name != "codex" || req.Wrap == nil) {
		return Spec{}, errors.New("external Codex execution requires an explicit outer sandbox wrapper")
	}
	for _, value := range append([]string{req.Model, req.Workdir, req.BaseURL}, req.Command...) {
		if strings.ContainsRune(value, 0) {
			return Spec{}, errors.New("harness arguments must not contain NUL")
		}
	}
	if req.APIKeyEnv != "" && !validEnvName(req.APIKeyEnv) {
		return Spec{}, errors.New("api_key_env must be an environment variable name")
	}
	if req.BaseURL != "" {
		if err := validateBaseURL(req.BaseURL); err != nil {
			return Spec{}, err
		}
	}
	overrides := make(map[string]string, len(req.Env)+2)
	for key, value := range req.Env {
		if !validEnvName(key) || strings.ContainsRune(value, 0) {
			return Spec{}, errors.New("environment variables require valid names and values without NUL")
		}
		overrides[key] = value
	}
	credential := ""
	if req.APIKeyEnv != "" {
		var ok bool
		credential, ok = req.Env[req.APIKeyEnv]
		if !ok {
			credential, ok = os.LookupEnv(req.APIKeyEnv)
		}
		if !ok || credential == "" {
			return Spec{}, errors.New("selected credential environment variable is not set")
		}
	}

	spec := Spec{Name: name, Workdir: req.Workdir}
	switch name {
	case "command":
		if len(req.Command) == 0 || req.Command[0] == "" {
			return Spec{}, errors.New("command harness requires an executable argv")
		}
		if req.Model != "" || req.BaseURL != "" || req.APIKeyEnv != "" {
			return Spec{}, errors.New("command harness does not interpret model, base_url, or api_key_env; configure its argv and environment explicitly")
		}
		spec.Binary, spec.Args = req.Command[0], append([]string(nil), req.Command[1:]...)
	case "claude":
		spec.Binary = "claude"
		spec.Args = []string{"--safe-mode", "--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--permission-mode", "dontAsk"}
		if req.BaseURL != "" {
			overrides["ANTHROPIC_BASE_URL"] = req.BaseURL
		}
		if req.APIKeyEnv != "" {
			overrides["ANTHROPIC_API_KEY"] = credential
		}
	case "codex":
		spec.Binary = "codex"
		spec.Args = []string{"exec", "--json", "--color", "never", "--sandbox", "workspace-write"}
		if req.ExternalSandbox {
			// This access is inside the caller's required sandbox, never the
			// host. Native approval/rule handling is not globally bypassed.
			spec.Args[len(spec.Args)-1] = "danger-full-access"
		}
		if req.APIKeyEnv != "" {
			if req.BaseURL == "" {
				overrides["OPENAI_API_KEY"] = credential
			} else {
				overrides["AGENTMUX_CODEX_API_KEY"] = credential
			}
		}
		spec.Args = codexEndpointArgs(req, spec.Args)
	case "antigravity":
		if req.BaseURL != "" || req.APIKeyEnv != "" {
			return Spec{}, errors.New("antigravity endpoint and api_key_env overrides are not supported; use its configured local authentication or Pi for a custom endpoint")
		}
		spec.Binary = "agy"
		spec.Args = []string{"--input-format", "stream-json", "--output-format", "stream-json", "--disable-slash-commands"}
	case "pi":
		spec.Binary = "pi"
		spec.Args = []string{"--mode", "rpc", "--no-session"}
		if req.BaseURL != "" {
			if req.Model == "" {
				return Spec{}, errors.New("pi with base_url requires an exact model ID")
			}
			spec.Args = append(spec.Args, "--provider", "agentmux")
			if req.APIKeyEnv != "" {
				overrides["AGENTMUX_PI_API_KEY"] = credential
			}
		} else if req.APIKeyEnv != "" {
			return Spec{}, errors.New("pi api_key_env requires base_url; otherwise configure the selected provider's environment explicitly")
		}
		if req.ModelMetadata.Tools != nil && !*req.ModelMetadata.Tools {
			spec.Args = append(spec.Args, "--no-tools")
		}
	default:
		return Spec{}, errors.New("unsupported managed harness (supported: command, claude, codex, antigravity, pi)")
	}
	if name != "command" {
		if req.Model != "" {
			spec.Args = append(spec.Args, "--model", req.Model)
		}
		if err := configureConversation(req, &spec, overrides); err != nil {
			return Spec{}, err
		}
		if name == "codex" {
			spec.Args = append(spec.Args, "-")
		}
		if len(req.Command) > 0 {
			if req.Command[0] == "" {
				return Spec{}, errors.New("harness executable must not be empty")
			}
			spec.Binary = req.Command[0]
			spec.Args = append(append([]string(nil), req.Command[1:]...), spec.Args...)
		}
	}
	if name == "command" && req.Conversation != nil {
		if req.Conversation.Mode != "" && req.Conversation.Mode != "fresh" {
			return Spec{}, ErrConversationUnsupported
		}
		if req.Conversation.ID != "" || req.Conversation.Dir != "" {
			return Spec{}, errors.New("fresh conversation must not supply a native ID or directory")
		}
	}
	spec.Env = mergeEnv(runtimeEnv(), overrides)
	return spec, nil
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("base_url must be an http(s) API base without credentials, query, or fragment")
	}
	return nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, char := range name {
		if char != '_' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (i == 0 || char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func mergeEnv(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func runtimeEnv() []string {
	var result []string
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "COLORTERM", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+value)
		}
	}
	return result
}

// Run executes exactly one turn, optionally resuming an explicitly identified
// native conversation. It never steers an existing run. Callers serialize turns and supply a
// deadline. Partial output and raw events are returned even when the run fails.
func Run(ctx context.Context, req Request) (Result, error) {
	if req.Conversation != nil && req.Conversation.Mode == "create" && req.Conversation.ID == "" && (req.Harness == "claude" || req.Harness == "claude-code" || req.Harness == "pi") {
		conversation := *req.Conversation
		var err error
		conversation.ID, err = newConversationID()
		if err != nil {
			return Result{}, errors.New("could not generate a private conversation identity")
		}
		req.Conversation = &conversation
	}
	spec, err := Build(req)
	if err != nil {
		return Result{}, err
	}
	conversation := spec.conversation
	cleanup, err := preparePiEndpoint(req, &spec)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	if req.Wrap != nil {
		spec, err = req.Wrap(spec)
		if err != nil {
			return Result{}, fmt.Errorf("wrap harness sandbox: %w", err)
		}
	}
	result, err := run(ctx, req, spec)
	if req.AfterProcess != nil {
		if finishErr := req.AfterProcess(); finishErr != nil {
			err = errors.Join(err, fmt.Errorf("finish private process checkpoint: %w", finishErr))
		}
	}
	if err == nil && conversation != nil {
		err = verifyConversation(spec.Name, conversation, result.SessionID)
	}
	return result, err
}
