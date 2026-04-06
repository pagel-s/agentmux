package toolruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"agentmux/internal/resolve"
	"agentmux/internal/tmux"
	"agentmux/internal/tools"
)

const (
	jsonSchemaV1    = 1
	exitSuccess     = 0
	exitRuntime     = 1
	exitValidation  = 2
	exitConfigParse = 3
	exitDependency  = 4
)

type Error struct {
	Code     string
	ExitCode int
	Message  string
	Command  string
	Session  string
	Pane     string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *Error) DetailsJSON() json.RawMessage {
	if e == nil {
		return nil
	}
	payload, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Command       string `json:"command"`
		ErrorCode     string `json:"error_code"`
		ExitCode      int    `json:"exit_code"`
		Message       string `json:"message"`
		Session       string `json:"session,omitempty"`
		Pane          string `json:"pane,omitempty"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          "error",
		Command:       e.Command,
		ErrorCode:     e.Code,
		ExitCode:      e.ExitCode,
		Message:       e.Message,
		Session:       e.Session,
		Pane:          e.Pane,
	})
	if err != nil {
		return nil
	}
	return payload
}

type ScaleConfigResolver func(session string) (resolve.Config, *Error)

type Executor struct {
	Getenv                  func(string) string
	RequireTMux             func() error
	ResolveScaleConfig      ScaleConfigResolver
	SetKV                   func(session string, key string, value string, verbose bool) error
	GetKV                   func(session string, key string, verbose bool) (string, error)
	WaitForPattern          func(session string, pane string, pattern string, timeout time.Duration, verbose bool) (string, error)
	ScaleAgent              func(session string, agentName string, targetCount int, preferredWindow string, cfg resolve.Config, opts tmux.Options) error
	SendCommandToPaneByName func(session string, name string, commandText string, pressEnter bool, verbose bool) error
}

func New(requireTMux func() error, resolveScaleConfig ScaleConfigResolver) Executor {
	return Executor{
		Getenv:                  os.Getenv,
		RequireTMux:             requireTMux,
		ResolveScaleConfig:      resolveScaleConfig,
		SetKV:                   tmux.SetKV,
		GetKV:                   tmux.GetKV,
		WaitForPattern:          tmux.WaitForPattern,
		ScaleAgent:              tmux.ScaleAgent,
		SendCommandToPaneByName: tmux.SendCommandToPaneByName,
	}
}

func (e Executor) Execute(name string, raw []byte, source string) (json.RawMessage, *Error) {
	spec, ok := tools.Lookup(name)
	if !ok {
		return nil, validationError(name, fmt.Sprintf("unsupported tool %q", name), "", "")
	}

	return e.withDefaults().execute(spec, string(raw), source)
}

func (e Executor) ExecuteArgs(name string, args map[string]any) (json.RawMessage, *Error) {
	raw, err := json.Marshal(defaultMap(args))
	if err != nil {
		return nil, validationError(name, fmt.Sprintf("marshal tool arguments: %v", err), "", "")
	}
	return e.Execute(name, raw, "arguments")
}

func (e Executor) withDefaults() Executor {
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.RequireTMux == nil {
		e.RequireTMux = func() error { return nil }
	}
	if e.SetKV == nil {
		e.SetKV = tmux.SetKV
	}
	if e.GetKV == nil {
		e.GetKV = tmux.GetKV
	}
	if e.WaitForPattern == nil {
		e.WaitForPattern = tmux.WaitForPattern
	}
	if e.ScaleAgent == nil {
		e.ScaleAgent = tmux.ScaleAgent
	}
	if e.SendCommandToPaneByName == nil {
		e.SendCommandToPaneByName = tmux.SendCommandToPaneByName
	}
	return e
}

func (e Executor) execute(spec tools.ToolSpec, raw string, source string) (json.RawMessage, *Error) {
	switch spec.Name {
	case "kv_set":
		var input struct {
			Session string `json:"session"`
			Key     string `json:"key"`
			Value   string `json:"value"`
		}
		if err := decodeJSON(raw, source, &input); err != nil {
			return nil, validationError(spec.Command, err.Error(), "", "")
		}
		input.Session = e.defaultSession(input.Session)
		if strings.TrimSpace(input.Session) == "" || strings.TrimSpace(input.Key) == "" {
			return nil, validationError(spec.Command, "kv_set requires non-empty key and session or $AGENTMUX_SESSION", input.Session, "")
		}
		if err := e.RequireTMux(); err != nil {
			return nil, dependencyError(spec.Command, err, input.Session, "")
		}
		if err := e.SetKV(input.Session, input.Key, input.Value, false); err != nil {
			return nil, runtimeError(spec.Command, err, input.Session, "")
		}
		return marshalJSON(struct {
			SchemaVersion int    `json:"schema_version"`
			Kind          string `json:"kind"`
			Session       string `json:"session"`
			Key           string `json:"key"`
			Value         string `json:"value,omitempty"`
			Message       string `json:"message"`
		}{
			SchemaVersion: jsonSchemaV1,
			Kind:          "kv-set",
			Session:       input.Session,
			Key:           input.Key,
			Value:         input.Value,
			Message:       fmt.Sprintf("set blackboard key %q to %q in session %q", input.Key, input.Value, input.Session),
		})
	case "kv_get":
		var input struct {
			Session string `json:"session"`
			Key     string `json:"key"`
		}
		if err := decodeJSON(raw, source, &input); err != nil {
			return nil, validationError(spec.Command, err.Error(), "", "")
		}
		input.Session = e.defaultSession(input.Session)
		if strings.TrimSpace(input.Session) == "" || strings.TrimSpace(input.Key) == "" {
			return nil, validationError(spec.Command, "kv_get requires non-empty key and session or $AGENTMUX_SESSION", input.Session, "")
		}
		if err := e.RequireTMux(); err != nil {
			return nil, dependencyError(spec.Command, err, input.Session, "")
		}
		value, err := e.GetKV(input.Session, input.Key, false)
		if err != nil {
			return nil, runtimeError(spec.Command, err, input.Session, "")
		}
		return marshalJSON(struct {
			SchemaVersion int    `json:"schema_version"`
			Kind          string `json:"kind"`
			Session       string `json:"session"`
			Key           string `json:"key"`
			Value         string `json:"value"`
		}{
			SchemaVersion: jsonSchemaV1,
			Kind:          "kv-get",
			Session:       input.Session,
			Key:           input.Key,
			Value:         value,
		})
	case "wait":
		var input struct {
			Session string `json:"session"`
			Pane    string `json:"pane"`
			Pattern string `json:"pattern"`
			Timeout string `json:"timeout"`
		}
		if err := decodeJSON(raw, source, &input); err != nil {
			return nil, validationError(spec.Command, err.Error(), "", "")
		}
		input.Session = e.defaultSession(input.Session)
		if strings.TrimSpace(input.Session) == "" || strings.TrimSpace(input.Pane) == "" || strings.TrimSpace(input.Pattern) == "" {
			return nil, validationError(spec.Command, "wait requires non-empty pane, pattern, and session or $AGENTMUX_SESSION", input.Session, input.Pane)
		}
		timeout := time.Duration(0)
		if strings.TrimSpace(input.Timeout) != "" {
			parsed, err := time.ParseDuration(input.Timeout)
			if err != nil {
				return nil, validationError(spec.Command, fmt.Sprintf("invalid timeout %q: %v", input.Timeout, err), input.Session, input.Pane)
			}
			timeout = parsed
		}
		if err := e.RequireTMux(); err != nil {
			return nil, dependencyError(spec.Command, err, input.Session, input.Pane)
		}
		match, err := e.WaitForPattern(input.Session, input.Pane, input.Pattern, timeout, false)
		if err != nil {
			return nil, runtimeError(spec.Command, err, input.Session, input.Pane)
		}
		return marshalJSON(struct {
			SchemaVersion int    `json:"schema_version"`
			Kind          string `json:"kind"`
			Session       string `json:"session"`
			Pane          string `json:"pane"`
			Pattern       string `json:"pattern"`
			Match         string `json:"match"`
		}{
			SchemaVersion: jsonSchemaV1,
			Kind:          "wait",
			Session:       input.Session,
			Pane:          input.Pane,
			Pattern:       input.Pattern,
			Match:         match,
		})
	case "scale":
		var input struct {
			Session string `json:"session"`
			Agent   string `json:"agent"`
			Count   int    `json:"count"`
			Window  string `json:"window"`
		}
		if err := decodeJSON(raw, source, &input); err != nil {
			return nil, validationError(spec.Command, err.Error(), "", "")
		}
		input.Session = e.defaultSession(input.Session)
		if strings.TrimSpace(input.Session) == "" || strings.TrimSpace(input.Agent) == "" {
			return nil, validationError(spec.Command, "scale requires non-empty agent and session or $AGENTMUX_SESSION", input.Session, "")
		}
		if input.Count < 0 {
			return nil, validationError(spec.Command, "scale requires count >= 0", input.Session, "")
		}
		if err := e.RequireTMux(); err != nil {
			return nil, dependencyError(spec.Command, err, input.Session, "")
		}
		if e.ResolveScaleConfig == nil {
			return nil, runtimeError(spec.Command, fmt.Errorf("scale tool is not configured"), input.Session, "")
		}
		cfg, cfgErr := e.ResolveScaleConfig(input.Session)
		if cfgErr != nil {
			if cfgErr.Command == "" {
				cfgErr.Command = spec.Command
			}
			if cfgErr.Session == "" {
				cfgErr.Session = input.Session
			}
			return nil, cfgErr
		}
		if err := e.ScaleAgent(input.Session, input.Agent, input.Count, input.Window, cfg, tmux.Options{Verbose: false}); err != nil {
			return nil, runtimeError(spec.Command, err, input.Session, "")
		}
		return marshalJSON(struct {
			SchemaVersion int    `json:"schema_version"`
			Kind          string `json:"kind"`
			Session       string `json:"session"`
			Agent         string `json:"agent"`
			Count         int    `json:"count"`
			Message       string `json:"message"`
		}{
			SchemaVersion: jsonSchemaV1,
			Kind:          "scale",
			Session:       input.Session,
			Agent:         input.Agent,
			Count:         input.Count,
			Message:       fmt.Sprintf("scaled agent %q to %d instances in session %q", input.Agent, input.Count, input.Session),
		})
	case "send":
		var input struct {
			Session string `json:"session"`
			Pane    string `json:"pane"`
			Command string `json:"command"`
			NoEnter bool   `json:"no_enter"`
		}
		if err := decodeJSON(raw, source, &input); err != nil {
			return nil, validationError(spec.Command, err.Error(), "", "")
		}
		input.Session = e.defaultSession(input.Session)
		if strings.TrimSpace(input.Session) == "" || strings.TrimSpace(input.Pane) == "" || strings.TrimSpace(input.Command) == "" {
			return nil, validationError(spec.Command, "send requires non-empty pane, command, and session or $AGENTMUX_SESSION", input.Session, input.Pane)
		}
		if err := e.RequireTMux(); err != nil {
			return nil, dependencyError(spec.Command, err, input.Session, input.Pane)
		}
		if err := e.SendCommandToPaneByName(input.Session, input.Pane, input.Command, !input.NoEnter, false); err != nil {
			return nil, runtimeError(spec.Command, err, input.Session, input.Pane)
		}
		message := fmt.Sprintf("sent command to pane %q in session %q", input.Pane, input.Session)
		if input.NoEnter {
			message = fmt.Sprintf("sent command text to pane %q in session %q without pressing Enter", input.Pane, input.Session)
		}
		return marshalJSON(struct {
			SchemaVersion int    `json:"schema_version"`
			Kind          string `json:"kind"`
			Session       string `json:"session"`
			Pane          string `json:"pane"`
			Message       string `json:"message"`
			NoEnter       bool   `json:"no_enter,omitempty"`
		}{
			SchemaVersion: jsonSchemaV1,
			Kind:          "send",
			Session:       input.Session,
			Pane:          input.Pane,
			Message:       message,
			NoEnter:       input.NoEnter,
		})
	default:
		return nil, validationError(spec.Command, fmt.Sprintf("unsupported tool %q", spec.Name), "", "")
	}
}

func (e Executor) defaultSession(session string) string {
	session = strings.TrimSpace(session)
	if session != "" {
		return session
	}
	if e.Getenv == nil {
		return ""
	}
	return strings.TrimSpace(e.Getenv("AGENTMUX_SESSION"))
}

func decodeJSON(raw string, source string, dst any) error {
	label := "payload"
	if strings.TrimSpace(source) != "" {
		label = source + " payload"
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid %s: %w", label, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("invalid %s: unexpected trailing data", label)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid %s: %w", label, err)
	}
	return nil
}

func marshalJSON(v any) (json.RawMessage, *Error) {
	out, err := json.Marshal(v)
	if err != nil {
		return nil, &Error{
			Code:     exitCodeName(exitRuntime),
			ExitCode: exitRuntime,
			Message:  fmt.Sprintf("marshal tool result: %v", err),
		}
	}
	return json.RawMessage(out), nil
}

func defaultMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	return values
}

func validationError(command string, message string, session string, pane string) *Error {
	return &Error{Code: exitCodeName(exitValidation), ExitCode: exitValidation, Message: message, Command: command, Session: session, Pane: pane}
}

func runtimeError(command string, err error, session string, pane string) *Error {
	return &Error{Code: exitCodeName(exitRuntime), ExitCode: exitRuntime, Message: err.Error(), Command: command, Session: session, Pane: pane}
}

func dependencyError(command string, err error, session string, pane string) *Error {
	return &Error{Code: exitCodeName(exitDependency), ExitCode: exitDependency, Message: err.Error(), Command: command, Session: session, Pane: pane}
}

func exitCodeName(code int) string {
	switch code {
	case exitSuccess:
		return "success"
	case exitRuntime:
		return "runtime"
	case exitValidation:
		return "validation"
	case exitConfigParse:
		return "config_parse"
	case exitDependency:
		return "dependency"
	default:
		return "unknown"
	}
}
