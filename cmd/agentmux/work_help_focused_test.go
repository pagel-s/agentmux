package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managedlaunch"
	"agentmux/internal/sessioninventory"
)

func TestEverydayOperationHelpIsFocusedAndNeedsNoWorkspace(t *testing.T) {
	previousDiscover, previousDescribe := discoverWorkSessions, describeWorkRuntime
	t.Cleanup(func() { discoverWorkSessions, describeWorkRuntime = previousDiscover, previousDescribe })
	discoverWorkSessions = func(context.Context, string) []sessioninventory.Session {
		t.Fatal("help discovered native sessions")
		return nil
	}
	describeWorkRuntime = func(context.Context, string) (managedlaunch.Runtime, error) {
		t.Fatal("help inspected a workspace")
		return managedlaunch.Runtime{}, nil
	}
	t.Setenv("XDG_STATE_HOME", "invalid-relative-root")
	t.Setenv("TMUX", "/unverifiable/attachment,123,0")
	state := filepath.Join(t.TempDir(), "not-created")
	for _, operation := range []string{"start", "attach", "dashboard", "send", "reply", "inbox", "board", "label", "collaborate", "disconnect", "isolate", "retire", "remove", "down"} {
		for _, help := range []string{"--help", "-h"} {
			var out, diagnostics bytes.Buffer
			code := runWorkSession(operation, []string{"--state", state, help}, strings.NewReader(""), &out, &diagnostics)
			text := out.String()
			if code != exitSuccess || diagnostics.Len() != 0 || !strings.HasPrefix(text, "Usage: amx "+operation+" ") || !strings.Contains(text, "Next:") || strings.Count(text, "\n") > 20 {
				t.Fatalf("help is not focused or needed runtime: %s %s code=%d\n%s\n%s", operation, help, code, text, diagnostics.String())
			}
			if operation != "send" && strings.Contains(text, "Legacy raw terminal input") {
				t.Fatalf("unrelated raw input controls leaked into %s help", operation)
			}
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("help created workspace state: %v", err)
	}
}

func TestSendHelpMakesIntentAndDeliveryEvidenceDistinct(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if code := runWorkSession("send", []string{"--help"}, strings.NewReader(""), &out, &diagnostics); code != exitSuccess {
		t.Fatal(diagnostics.String())
	}
	for _, contract := range []string{"--mode auto|note|instruction", "notes to native", "instruction tasks to managed", "queued task is not verified completion", "permission and receiver delivery are separate", "amx task TASK_ID"} {
		if !strings.Contains(out.String(), contract) {
			t.Fatalf("delegation help omitted %q:\n%s", contract, out.String())
		}
	}
}
