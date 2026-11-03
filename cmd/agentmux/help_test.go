package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDefaultHelpIsShortAndProvidesFocusedTopics(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if code := runHelp(nil, &out, &diagnostics); code != exitSuccess {
		t.Fatalf("help failed: %d %s", code, &diagnostics)
	}
	if lines := strings.Count(out.String(), "\n"); lines > 30 {
		t.Fatalf("default help overwhelms the first run: %d lines", lines)
	}
	for _, text := range []string{"amx demo", "amx start", "amx a [ID]", "amx sessions", "amx work", "amx help all", "amx help automation", "amx help legacy"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("missing first-run route %q", text)
		}
	}
	for _, text := range []string{"amx kv-set", "amx mcp-server", "amx experiment", "amx research"} {
		if strings.Contains(out.String(), text) {
			t.Fatalf("default help includes advanced command %q", text)
		}
	}
}

func TestHelpAllRetainsCompatibilityAndFocusedTopics(t *testing.T) {
	for topic, texts := range map[string][]string{
		"all":        {"amx demo", "amx kv-set", "amx mcp-server", "amx experiment", "amx research", "amx load", "amx work", "Aliases:", "Legacy workspace flow:"},
		"automation": {"amx work", "amx flow", "amx managed", "amx research", "--allow-models", "experimental"},
		"legacy":     {"amx templates", "amx load", "amx status", "default tmux socket", "amx help all"},
	} {
		t.Run(topic, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			if code := runHelp([]string{topic}, &out, &diagnostics); code != exitSuccess {
				t.Fatalf("topic failed: %d %s", code, &diagnostics)
			}
			for _, text := range texts {
				if !strings.Contains(out.String(), text) {
					t.Fatalf("topic missing %q: %s", text, &out)
				}
			}
		})
	}
	for _, args := range [][]string{{"bogus"}, {"all", "extra"}} {
		var out, diagnostics bytes.Buffer
		if code := runHelp(args, &out, &diagnostics); code != exitValidation || diagnostics.Len() == 0 {
			t.Fatalf("invalid topic accepted: %q %d %s", args, code, &diagnostics)
		}
	}
}
