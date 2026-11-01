package main

import (
	"strings"

	"agentmux/internal/managed"
)

func (m console) selectedAgent() *managed.AgentInfo {
	if len(m.agents) == 0 {
		return nil
	}
	a := m.agents[m.selected%len(m.agents)]
	return &a
}

// A registry identity can remain active after its native CLI exits. Never
// confuse that coordination credential with a currently interactive process.
func (m console) nativeNeedsRelaunch() bool {
	a := m.selectedAgent()
	if a == nil || a.External || a.Config.Interaction != "interactive" {
		return false
	}
	pane, ok := m.data.runtime.Agents[a.ID]
	return ok && pane.State != "" && pane.State != "running"
}

func (m *console) reviewNativeRelaunch() {
	a := m.selectedAgent()
	if m.busy {
		m.message = "An action is still applying. Wait before reviewing a relaunch."
		return
	}
	if a == nil {
		m.message = "Select a workspace-owned native session before relaunching."
		return
	}
	pane, owned := m.data.runtime.Agents[a.ID]
	switch {
	case a.State != "active" || a.External || a.Config.Interaction != "interactive" || !owned:
		m.message = "Only active workspace-owned native sessions can be relaunched."
	case m.offline:
		m.message = "Broker offline. Reconnect before relaunching the selected session."
	case m.data.runtime.RelaunchVersion != 1:
		m.message = "Restart this workspace once with the updated CLI to enable individual relaunch."
	case pane.State == "running":
		m.message = "Exit the selected native CLI before relaunching it."
	default:
		m.compose("relaunch", a.ID, "", "")
		m.message = "Review this session ID. Enter starts fresh; or type last, picker, or an exact native history ID. Healthy peers remain running."
	}
}

func (m console) nativeEntryHint() string {
	a := m.selectedAgent()
	if a == nil {
		return "Shift+N new session"
	}
	if a.External {
		return "Enter bridge setup"
	}
	if m.data.headless {
		return "5 Tasks output · 8 Events"
	}
	if m.nativeNeedsRelaunch() {
		if a.State != "active" {
			return "o session details"
		}
		if m.offline || m.data.runtime.RelaunchVersion != 1 {
			return "Enter recovery guidance"
		}
		return "Enter relaunch review"
	}
	return "Enter native terminal"
}

func (m console) teamActionReason(operation string) string {
	if m.busy {
		return "An action is still applying. Wait before changing this team."
	}
	if m.data.teamsErr != nil {
		return "Team status unavailable; refresh before using team controls."
	}
	team := m.selectedTeam()
	if team == nil {
		return "No agent-created teams. Press 3 for Connections, then c to connect a pair."
	}
	// Owners may inspect retained memory after a team dissolves.
	if operation == "memory" {
		if m.offline {
			return "Broker offline; reconnect before reading team memory."
		}
		if team.Memory == "" {
			return "This team has no memory namespace."
		}
		return ""
	}
	if team.State != "active" {
		return "This team is dissolved; only its retained records can be inspected. Native terminals are unchanged."
	}
	if m.offline {
		return "Broker offline; reconnect before changing this team or posting to its board."
	}
	if operation == "board" {
		if team.Board == "" {
			return "This team has no board namespace."
		}
		return ""
	}
	if operation == "team.dissolve" || operation == "team.update" {
		return ""
	}
	target := m.teamTarget()
	if target == nil {
		return "Choose an agent with ←/→."
	}
	switch operation {
	case "team.invite":
		if target.State != "active" || teamContains(team.Members, target.ID) || teamContains(team.Invited, target.ID) {
			return "Invite a distinct active nonmember. Existing invitations require the agent's own acceptance."
		}
	case "team.remove":
		if target.ID == team.Owner {
			return "A team owner cannot be removed; dissolve the team to end its shared access."
		}
		if !teamContains(team.Members, target.ID) && !teamContains(team.Invited, target.ID) {
			return "The selected agent is neither a member nor invited."
		}
	case "team.transfer":
		if target.State != "active" || target.ID == team.Owner || !teamContains(team.Members, target.ID) {
			return "Propose an active current member as the new owner. They must accept using their own credential."
		}
		if access, ok := team.MemberAccess[target.ID]; ok && !access.Allowed {
			return "The proposed owner must have current effective team access: " + access.Reason
		}
	}
	return ""
}

func (m console) teamKeyHints() string {
	if m.selectedTeam() == nil || m.data.teamsErr != nil {
		return "3 Connections: connect existing sessions · ? team help"
	}
	keys := []string{"[/] team", "←/→ target", "o full team"}
	for _, action := range []struct{ operation, hint string }{
		{"team.invite", "i invite"}, {"team.remove", "r remove"}, {"team.transfer", "t owner"},
		{"team.update", "e edit"}, {"team.dissolve", "d dissolve"}, {"board", "b board"}, {"memory", "v memory"},
	} {
		if m.teamActionReason(action.operation) == "" {
			keys = append(keys, action.hint)
		}
	}
	// Two rows avoid hiding actionable hints on 80-column terminals.
	if len(keys) > 6 {
		return strings.Join(keys[:6], " · ") + "\n" + strings.Join(keys[6:], " · ")
	}
	return strings.Join(keys, " · ")
}

// Keep the footer's vocabulary identical to the keys it documents. Uppercase
// shortcuts are spelled Shift+... so they cannot look like lowercase no-ops.
func (m console) contextualKeys() string {
	common := "←/→ views · Tab/1–8 · / find · Shift+W workspaces · ? help · q detach/close"
	if m.arrowsSelectTarget() {
		common = "Ctrl+←/→ views · Tab/1–8 · / find · Shift+W workspaces · ? help · q detach/close"
	}
	if m.workspacePicker != nil {
		return "↑/↓ workspace · Enter opens observer · Esc/q back"
	}
	if m.teamAction != nil {
		if m.teamAction.editKind != "" || m.teamAction.researchAction != "" && !m.teamAction.reasonReady {
			return "Enter reviews · Esc cancels · Ctrl-G help · Ctrl-C detach/close"
		}
		return "Enter applies displayed action · Esc cancels · Ctrl-G help\nq detach/close"
	}
	if m.composer != "" {
		return "Esc cancels (then q detach/close) · Ctrl-C detach/close"
	}
	var context string
	switch m.tab {
	case 0:
		keys := []string{"f filter"}
		if len(m.boardMessages()) > 0 {
			keys = append(keys, "[/] message", "r reply", "o read")
		}
		if m.selectedBoard() != "" {
			keys = append(keys, "b post", "+/- board")
		}
		context = strings.Join(keys, " · ")
	case 1:
		context = "↑/↓ agent · " + m.nativeEntryHint()
		if m.selectedID() != "" {
			context += " · o details · Shift+L title"
		}
	case 2:
		if m.selectedID() != "" {
			context = "↑/↓ sender · ←/→ recipient · c connect · d isolate"
			if !m.offline && m.data.boundaries.Agent == m.selectedID() {
				context += "\nm/p/i note/ping/task · Shift+M/P/I reset override · o details"
			}
		} else {
			context = "Shift+N adds a session; connections need two sessions"
		}
	case 3:
		if m.memoryOpen {
			context = "[/] entry · n new · v refresh · Esc namespaces"
			if len(m.memoryEntries) > 0 {
				context += " · o read · e edit"
			}
		} else {
			context = "3 Connections: connect sessions to share a namespace"
			if len(m.data.boundaries.Boards)+len(m.data.boundaries.Memory) > 0 {
				context = "[/] namespace · r/w read/write · Shift+R reset · o details"
				if len(m.data.boundaries.Memory) > 0 {
					context += " · v entries"
				}
			}
		}
	case 4:
		keys := []string{"f search history"}
		if len(m.data.activity.Tasks) > 0 {
			keys = append(keys, "[/] task", "o read", "r reply", "c cancel")
		}
		if m.taskMore {
			keys = append(keys, "PgDn older")
		}
		if len(m.taskBack) > 0 {
			keys = append(keys, "PgUp newer")
		}
		context = strings.Join(keys, " · ")
	case 5:
		context = "Shift+N new session"
		if len(m.observedSessions()) > 0 {
			context = "↑/↓ terminal · Enter details · c prepare bridge · o read"
		}
	case 6:
		context = m.teamKeyHints()
	case 7:
		context = "Home latest/live"
		if len(m.data.activity.Events) > 0 {
			context = "[/] event · Home latest/live · o details"
		}
		if m.data.research != nil {
			context += "\nShift+P/R/S/T pause/resume/step/stop (reason + review)"
		}
	}
	if m.tab == 0 || m.tab == 1 {
		management := []string{"Shift+N new"}
		if a := m.selectedAgent(); a != nil {
			management = append(management, "s send")
			if !a.Config.NativeOnly {
				management = append(management, "Shift+V role")
			}
			if a.State != "revoked" {
				management = append(management, "x revoke + descendants")
			}
		}
		context += "\n" + strings.Join(management, " · ")
	}
	return context + "\n" + common
}
