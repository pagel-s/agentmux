package toolruntime

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agentmux/internal/resolve"
	"agentmux/internal/tmux"
)

func TestExecuteKVGetDefaultsSessionFromEnv(t *testing.T) {
	executor := Executor{
		Getenv: func(key string) string {
			if key == "AGENTMUX_SESSION" {
				return "demo"
			}
			return ""
		},
		GetKV: func(session string, key string, verbose bool) (string, error) {
			if session != "demo" || key != "task" {
				t.Fatalf("GetKV(%q, %q) called, want (%q, %q)", session, key, "demo", "task")
			}
			return "go", nil
		},
	}

	result, execErr := executor.Execute("kv_get", []byte(`{"key":"task"}`), "--from-json")
	if execErr != nil {
		t.Fatalf("Execute returned error: %#v", execErr)
	}

	var payload struct {
		Kind    string `json:"kind"`
		Session string `json:"session"`
		Key     string `json:"key"`
		Value   string `json:"value"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.Kind != "kv-get" || payload.Session != "demo" || payload.Key != "task" || payload.Value != "go" {
		t.Fatalf("payload = %#v, want kv-get result for demo/task", payload)
	}
}

func TestExecuteSendReturnsStructuredDependencyError(t *testing.T) {
	executor := Executor{
		RequireTMux: func() error {
			return errors.New("tmux missing")
		},
	}

	_, execErr := executor.Execute("send", []byte(`{"session":"demo","pane":"lead","command":"echo ok"}`), "--from-json")
	if execErr == nil {
		t.Fatal("Execute returned nil error, want dependency error")
	}
	if execErr.Code != "dependency" || execErr.ExitCode != exitDependency {
		t.Fatalf("execErr = %#v, want dependency exit %d", execErr, exitDependency)
	}
	if !strings.Contains(string(execErr.DetailsJSON()), `"command":"send"`) {
		t.Fatalf("details = %s, want send error payload", execErr.DetailsJSON())
	}
}

func TestExecuteScaleUsesSharedResolver(t *testing.T) {
	executor := Executor{
		ResolveScaleConfig: func(session string) (resolve.Config, *Error) {
			if session != "demo" {
				t.Fatalf("ResolveScaleConfig(%q), want %q", session, "demo")
			}
			return resolve.Config{Session: "demo"}, nil
		},
		ScaleAgent: func(session string, agentName string, targetCount int, preferredWindow string, cfg resolve.Config, opts tmux.Options) error {
			if session != "demo" || agentName != "coder" || targetCount != 2 || preferredWindow != "eng" || cfg.Session != "demo" {
				t.Fatalf("ScaleAgent(%q, %q, %d, %q, %#v) unexpected", session, agentName, targetCount, preferredWindow, cfg)
			}
			return nil
		},
	}

	result, execErr := executor.Execute("scale", []byte(`{"session":"demo","agent":"coder","count":2,"window":"eng"}`), "--from-json")
	if execErr != nil {
		t.Fatalf("Execute returned error: %#v", execErr)
	}
	if !strings.Contains(string(result), `"kind":"scale"`) {
		t.Fatalf("result = %s, want scale payload", result)
	}
}
