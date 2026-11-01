package harness

import (
	"strings"
	"testing"
)

func TestExplicitNativeResumeKeepsInteractiveInterface(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi"} {
		for _, selection := range []string{"", "last", "picker", "chosen-id"} {
			spec, err := BuildInteractive(Request{Harness: name, NativeResume: selection, Prompt: "Role", Model: "model"})
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Join(spec.Args, " ")
			if strings.Contains(args, "--print") || strings.Contains(args, " exec ") {
				t.Fatal(args)
			}
			if selection == "chosen-id" && !strings.Contains(args, "chosen-id") {
				t.Fatal(args)
			}
			if selection == "" && (strings.Contains(args, "resume") || strings.Contains(args, "--continue") || strings.Contains(args, "--session")) {
				t.Fatal(args)
			}
		}
	}
	if _, err := BuildInteractive(Request{Harness: "command", Command: []string{"/bin/cat"}, NativeResume: "last"}); err == nil {
		t.Fatal("command history invented")
	}
	if _, err := BuildInteractive(Request{Harness: "claude", NativeResume: "--dangerous"}); err == nil {
		t.Fatal("option interpreted as native ID")
	}
}
