package managedsetup

import (
	"fmt"
	"path/filepath"
)

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

// Preset selects a complete, bounded starting definition. Managed presets
// launch idle workers; they never create a task or infer an autonomous goal.
func Preset(name, session, workspaceRoot, project, sandbox, home string) (Spec, error) {
	s := CodingPreset(session, workspaceRoot, project, sandbox, home)
	switch name {
	case "coding":
		return s, nil
	case "reviewer-pair":
		s.Agents[0].ID, s.Agents[0].Role = "reviewer", "Review the supplied change for correctness, security, failure cases and maintainability. Publish concrete findings with evidence. Do not edit files unless the owner explicitly asks."
		s.Agents[1].ID, s.Agents[1].Role = "challenger", "Independently challenge the supplied proposal or review. Seek counterexamples and missing tests, explain uncertainty and compare evidence. Do not edit files unless the owner explicitly asks."
	case "research-team":
		s.Agents[0].ID, s.Agents[0].Role = "coordinator", "Plan the owner-supplied study, record assumptions and provenance, and distinguish observations from causal claims. Coordinate only through permitted broker channels. Do not begin a study without explicit bounded tasks."
		s.Agents[1].ID, s.Agents[1].Role = "analyst", "Analyze the owner-supplied data or experimental design. Report sample size, exclusions, uncertainty, alternative explanations and reproducible procedures. Do not infer causal effects from descriptive or scripted evidence."
		critic := s.Agents[0]
		critic.ID, critic.Role = "critic", "Audit the study for confounding, leakage, selection bias, invalid contrasts, unobserved behavior and unsupported conclusions. Distinguish verified evidence from assumptions."
		s.Agents = append(s.Agents, critic)
	default:
		return Spec{}, fmt.Errorf("unknown preset %q; choose coding, reviewer-pair, or research-team", name)
	}
	for i := range s.Agents {
		s.Agents[i].Interaction = "managed"
	}
	return s, nil
}
