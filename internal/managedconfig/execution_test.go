package managedconfig

import "testing"

func TestExecutionBoundaries(t *testing.T) {
	base := Agent{Harness: "claude", Sandbox: "bubblewrap", Network: "host", Auth: NativeAuth{File: "/private/auth.json"}}
	if err := validateExecution("worker", base, Connection{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		modify     func(*Agent)
		connection Connection
	}{
		{"local auth", func(a *Agent) { a.Sandbox = "local" }, Connection{}},
		{"unsupported", func(a *Agent) { a.Harness = "command" }, Connection{}},
		{"native and key", func(a *Agent) {}, Connection{APIKeyEnv: "KEY"}},
		{"config override", func(a *Agent) { a.Env = map[string]string{"CODEX_HOME": "/other"} }, Connection{}},
		{"Pi selection", func(a *Agent) { a.Harness = "pi" }, Connection{}},
		{"negative memory", func(a *Agent) { a.Resources.MemoryBytes = -1 }, Connection{}},
		{"unbounded processes", func(a *Agent) { a.Resources.Processes = 65537 }, Connection{}},
		{"local resources", func(a *Agent) { a.Auth = NativeAuth{}; a.Sandbox = "local"; a.Resources.CPUPercent = 100 }, Connection{}},
		{"external wrong harness", func(a *Agent) { a.CodexSandbox = "external" }, Connection{}},
		{"external on host", func(a *Agent) {
			a.Harness = "codex"
			a.Auth = NativeAuth{}
			a.Sandbox = "local"
			a.CodexSandbox = "external"
		}, Connection{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := base
			tc.modify(&a)
			if validateExecution("worker", a, tc.connection) == nil {
				t.Fatal("unsafe execution accepted")
			}
		})
	}
	pi := base
	pi.Harness = "pi"
	pi.Auth.Provider = "anthropic"
	if err := validateExecution("worker", pi, Connection{}); err != nil {
		t.Fatal(err)
	}
	codex := base
	codex.Harness = "codex"
	codex.CodexSandbox = "external"
	if err := validateExecution("worker", codex, Connection{}); err != nil {
		t.Fatal(err)
	}
}
