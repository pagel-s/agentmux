package managedlaunch

import (
	"context"
	"errors"
	"testing"
)

func TestDescribeMarksMissingOrForeignPaneUnavailableOnlyAfterSuccessfulInventory(t *testing.T) {
	for _, tc := range []struct {
		name, first, want string
		inventoryError    bool
	}{
		{name: "missing", want: "unavailable"},
		{name: "foreign-attempt", first: "%1\t0\tforeign\tworker\talice\n", want: "unavailable"},
		{name: "wrong-agent", first: "%1\t0\towned\tworker\tother\n", want: "unavailable"},
		{name: "wrong-role", first: "%1\t0\towned\tbroker\talice\n", want: "unavailable"},
		{name: "observed-exit", first: "%1\t1\towned\tworker\talice\n", want: "exited"},
		{name: "inventory-failure", want: "running", inventoryError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := t.TempDir()
			runtime := Runtime{Version: 1, Session: "workspace", AttemptID: "owned", State: "running", Agents: map[string]AgentPane{
				"alice": {PaneID: "%1", State: "running"},
				"bob":   {PaneID: "%2", State: "running"},
			}}
			if err := writeRuntime(state, runtime); err != nil {
				t.Fatal(err)
			}
			l := &Launcher{options: Options{StateDir: state}}
			l.run = func(_ context.Context, args ...string) (string, error) {
				switch args[4] {
				case "show-options":
					return "owned\n", nil
				case "list-panes":
					if tc.inventoryError {
						return "", errors.New("inventory unavailable")
					}
					return tc.first + "%2\t0\towned\tworker\tbob\n", nil
				default:
					t.Fatalf("observation changed a process/window: %q", args)
					return "", nil
				}
			}
			observed, err := l.Describe(context.Background())
			if (err != nil) != tc.inventoryError || observed.Agents["alice"].State != tc.want || observed.Agents["alice"].PaneID != "%1" {
				t.Fatalf("wrong missing-pane status/identity: %+v %v", observed, err)
			}
			if observed.Agents["bob"] != runtime.Agents["bob"] || observed.State != "running" {
				t.Fatal("one unavailable pane changed unrelated worker or workspace state")
			}
		})
	}
}
