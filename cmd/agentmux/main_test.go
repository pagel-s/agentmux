package main

import (
	"encoding/json"
	"strings"
	"testing"

	"agentmux/internal/tmux"
)

func TestVersionString(t *testing.T) {
	original := version
	version = "test"
	t.Cleanup(func() {
		version = original
	})

	if got, want := versionString(), "agentmux test"; got != want {
		t.Fatalf("versionString() = %q, want %q", got, want)
	}
}

func TestRunVersionReturnsSuccess(t *testing.T) {
	if got, want := run([]string{"version"}), exitSuccess; got != want {
		t.Fatalf("run(version) = %d, want %d", got, want)
	}
	if got, want := run([]string{"--version"}), exitSuccess; got != want {
		t.Fatalf("run(--version) = %d, want %d", got, want)
	}
}

func TestRenderStatusJSON(t *testing.T) {
	out, err := renderStatusJSON("dogfood", []tmux.PaneStatus{
		{
			WindowIndex:  1,
			WindowName:   "grid",
			WindowID:     "@1",
			WindowActive: true,
			PaneIndex:    4,
			PaneID:       "%9",
			PaneActive:   true,
			Name:         "resolve",
			Title:        "Resolve",
			SourceType:   "agent",
			AgentName:    "reviewer",
			OnExit:       "restart",
			CurrentCmd:   "bash",
		},
	})
	if err != nil {
		t.Fatalf("renderStatusJSON returned error: %v", err)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("renderStatusJSON output %q is missing trailing newline", out)
	}

	var decoded struct {
		Session string            `json:"session"`
		Panes   []tmux.PaneStatus `json:"panes"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.Session != "dogfood" {
		t.Fatalf("Session = %q, want %q", decoded.Session, "dogfood")
	}
	if len(decoded.Panes) != 1 || decoded.Panes[0].Name != "resolve" {
		t.Fatalf("decoded panes = %#v, want resolve pane", decoded.Panes)
	}
	if decoded.Panes[0].SourceType != "agent" || decoded.Panes[0].AgentName != "reviewer" || decoded.Panes[0].OnExit != "restart" || decoded.Panes[0].CurrentCmd != "bash" {
		t.Fatalf("decoded pane metadata = %#v, want agent reviewer restart bash", decoded.Panes[0])
	}
}

func TestRenderCaptureJSON(t *testing.T) {
	out, err := renderCaptureJSON("dogfood", "review", 5, "hello\nworld\n")
	if err != nil {
		t.Fatalf("renderCaptureJSON returned error: %v", err)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("renderCaptureJSON output %q is missing trailing newline", out)
	}

	var decoded struct {
		Session   string `json:"session"`
		Pane      string `json:"pane"`
		TailLines int    `json:"tail_lines"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.Session != "dogfood" || decoded.Pane != "review" || decoded.TailLines != 5 || decoded.Content != "hello\nworld\n" {
		t.Fatalf("decoded = %#v, want session dogfood pane review content preserved", decoded)
	}
}

func TestRenderStatusIncludesRuntimeMetadata(t *testing.T) {
	out := renderStatus("dogfood", []tmux.PaneStatus{
		{
			WindowIndex: 1,
			WindowName:  "grid",
			PaneIndex:   5,
			PaneID:      "%10",
			Name:        "resolve",
			Title:       "Resolve",
			SourceType:  "agent",
			AgentName:   "reviewer",
			OnExit:      "restart",
			CurrentCmd:  "bash",
			PaneActive:  true,
		},
	})

	for _, want := range []string{
		`name="resolve"`,
		`source=agent`,
		`agent="reviewer"`,
		`on_exit="restart"`,
		`current="bash"`,
		`active=true`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderStatus output %q does not include %q", out, want)
		}
	}
}
