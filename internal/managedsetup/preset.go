package managedsetup

import "path/filepath"

// CodingPreset is human-led by default: real native terminals, a shared board,
// private memory, and opt-in collaboration. The CLI keeps all peer and shared
// access disabled until the owner enables a group. Automatic wakeups are off.
func CodingPreset(session, workspaceRoot, project, sandbox, home string) Spec {
	s := Spec{Session: session, WorkspaceRoot: workspaceRoot, Sandbox: sandbox, BrokerSandbox: sandbox, SharedBoard: true, InitGit: true}
	for _, name := range []string{"claude", "codex"} {
		a := Agent{ID: name, Harness: name, Interaction: "interactive", Workdir: project, Role: "Help the human develop and review their project. Coordinate with peers only when asked or when useful for the current human task.", Auth: "native-local"}
		if sandbox == "bubblewrap" {
			a.Auth = "file"
			if name == "claude" {
				a.AuthFile = filepath.Join(home, ".claude", ".credentials.json")
			} else {
				a.AuthFile = filepath.Join(home, ".codex", "auth.json")
				a.CodexSandbox = "external"
			}
		}
		s.Agents = append(s.Agents, a)
	}
	return s
}
