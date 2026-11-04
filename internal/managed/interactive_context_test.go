package managed

import (
	"os/exec"
	"strings"
	"testing"
)

func TestInteractiveContextExplainsPortableDeliveryAndResultChannels(t *testing.T) {
	prompt := interactiveCoordinationPrompt("claude", "Review the proposal", "/tmp/project state")
	for _, want := range []string{
		`managed call instruct --json '{"to":["RECIPIENT_ID"],"text":"REQUEST"}'`,
		"not proof that a native turn ran", "prior attempts may have run", "does not identify the receiver's mode",
		"cannot inspect or configure another session's native policy", "ask the owner to run",
		"do not execute them yourself", "Auto can start existing eligible queued items",
		"mode changes preserve subscriptions and limits", "amx delivery --help from any repository",
		"does not need a reverse message.send or command grant",
		"your own Results subscription, delivery mode and readiness",
		`managed call task.get --json '{"id":"TASK_ID"}'`,
		"Never read credentials or change the broker policy",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing portable guidance %q", want)
		}
	}
	if strings.Contains(prompt, "docs/") || strings.Contains(prompt, `native.status --json '{"agent"`) {
		t.Fatal("bootstrap depends on repository docs or teaches peer native-status access")
	}
}

func TestInteractiveContextOwnerCommandsQuoteExactState(t *testing.T) {
	state := "/tmp/a 'quoted' $(printf unsafe); workspace"
	prompt := interactiveCoordinationPrompt("claude", "review", state)
	count := 0
	for _, line := range strings.Split(prompt, "\n") {
		if !strings.HasPrefix(line, "amx delivery RECIPIENT_ID ") {
			continue
		}
		count++
		// Substitute a harmless shell function, so the test checks real POSIX
		// argument parsing without accessing a workspace or broker.
		out, err := exec.Command("sh", "-c", `amx() { printf '%s\n' "$@"; }; `+line).CombinedOutput()
		if err != nil {
			t.Fatalf("owner command cannot be parsed: %v", err)
		}
		args := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
		if len(args) != 5 || args[0] != "delivery" || args[1] != "RECIPIENT_ID" || args[3] != "--state" || args[4] != state {
			t.Fatalf("owner state argument changed: %#v", args)
		}
	}
	if count != 3 {
		t.Fatalf("got %d owner commands", count)
	}
}
