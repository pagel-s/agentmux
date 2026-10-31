package managedlaunch

import (
	"context"
	"testing"
)

func TestFocusRejectsDeadForeignOrReplacedAgentPane(t *testing.T) {
	for _, metadata := range []string{"%7\t1\trun_owned\tworker\talice\n", "%7\t0\trun_other\tworker\talice\n", "%7\t0\trun_owned\tworker\tbob\n", "%7\t0\trun_owned\tbroker\talice\n"} {
		l := &Launcher{options: Options{StateDir: "/private/state"}}
		selected := false
		l.run = func(_ context.Context, args ...string) (string, error) {
			switch args[4] {
			case "show-options":
				return "run_owned\n", nil
			case "list-panes":
				return metadata, nil
			case "select-window", "select-pane":
				selected = true
			}
			return "", nil
		}
		err := l.Focus(context.Background(), Runtime{AttemptID: "run_owned", Session: "work", Agents: map[string]AgentPane{"alice": {PaneID: "%7"}}}, "alice")
		if err == nil || selected {
			t.Fatalf("focused an unowned/dead pane: %q", metadata)
		}
	}
}
