package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Review metadata is immutable while its modal is open. Registry or team polls
// must never redirect a pending owner action to a different agent/team/version.
type consoleTeamAction struct {
	operation, teamID, teamName, agentID   string
	version                                int
	researchAction, reason                 string
	reasonReady                            bool
	editKind, editValue, name, description string
	editStep                               int
	budget                                 managedconfig.AgentBudget
	legacyPolicy                           string
	groupAgents                            []string
	legacyEdges                            []managed.EdgeControl
}

func (a consoleTeamAction) args() map[string]any {
	if a.operation == "control.update" {
		return map[string]any{"version": a.version, "kind": "collaboration", "agents": a.groupAgents, "enabled": true, "legacy_policy": a.legacyPolicy}
	}
	if a.operation == "capacity.update" {
		return map[string]any{"version": a.version, "agent": a.agentID, "agent_budget": a.budget}
	}
	if a.researchAction != "" {
		return map[string]any{"version": a.version, "action": a.researchAction, "reason": a.reason}
	}
	args := map[string]any{"id": a.teamID, "version": a.version}
	if a.operation == "team.update" {
		args["name"], args["description"] = a.name, a.description
	}
	if a.agentID != "" {
		args["agent"] = a.agentID
	}
	return args
}

func (m console) selectedTeam() *managed.Team {
	if len(m.data.teams) == 0 {
		return nil
	}
	t := m.data.teams[m.teamIndex%len(m.data.teams)]
	return &t
}

func (m console) teamTarget() *managed.AgentInfo {
	if len(m.agents) == 0 {
		return nil
	}
	a := m.agents[m.teamMemberIndex%len(m.agents)]
	return &a
}

func teamContains(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func (m *console) reviewTeamAction(operation string) {
	if reason := m.teamActionReason(operation); reason != "" {
		m.message = reason
		return
	}
	team := m.selectedTeam()
	action := consoleTeamAction{operation: operation, teamID: team.ID, teamName: team.Name, version: team.Version}
	if operation != "team.dissolve" && operation != "team.update" {
		target := m.teamTarget()
		action.agentID = target.ID
	}
	if operation == "team.update" {
		action.name, action.description = team.Name, team.Description
		action.editKind, action.editValue = "Team name", team.Name
	}
	m.teamAction = &action
}

func (m console) teamReviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.teamAction.operation == "control.update" {
		return m.legacyCollaborationKey(msg)
	}
	if m.teamAction.editKind != "" {
		return m.teamEditorKey(msg)
	}
	if m.teamAction.researchAction != "" && !m.teamAction.reasonReady {
		action := *m.teamAction
		switch key {
		case "esc":
			m.teamAction = nil
			return m, nil
		case "ctrl+c":
			return m, m.close()
		case "enter":
			if strings.TrimSpace(action.reason) == "" {
				m.message = "An intervention requires a reason before review."
				return m, nil
			}
			action.reasonReady = true
		case "backspace", "ctrl+h":
			r := []rune(action.reason)
			if len(r) > 0 {
				action.reason = string(r[:len(r)-1])
			}
		case "ctrl+u":
			action.reason = ""
		default:
			if msg.Type == tea.KeyRunes || key == " " {
				runes := msg.Runes
				if key == " " {
					runes = []rune{' '}
				}
				for _, r := range runes {
					if unicode.IsPrint(r) && !(r >= 0x202a && r <= 0x202e) && !(r >= 0x2066 && r <= 0x2069) && r != 0x061c && r != 0x200e && r != 0x200f && len(action.reason)+len(string(r)) <= 512 {
						action.reason += string(r)
					}
				}
			}
		}
		m.teamAction = &action
		return m, nil
	}
	switch key {
	case "esc":
		m.teamAction = nil
		return m, nil
	case "q", "ctrl+c":
		return m, m.close()
	case "enter":
		if m.busy {
			return m, nil
		}
		if m.offline {
			m.message = "Broker offline. Review kept; refresh before submitting."
			return m, nil
		}
		action := *m.teamAction
		m.teamAction = nil
		m.busy = true
		label := strings.TrimPrefix(action.operation, "team.") + " saved for " + action.teamName + " [" + action.teamID + "]. Native terminals keep running."
		if action.operation == "team.invite" {
			label = "Invited " + action.agentID + " to " + action.teamName + ". The agent must accept using its own credential."
		}
		if action.operation == "team.transfer" {
			label = "Ownership proposed to " + action.agentID + ". Current owner keeps authority until the proposed agent accepts."
		}
		if action.operation == "capacity.update" {
			label = "Admission reservations updated for " + action.agentID + ". Existing charges remain; native provider billing is not capped."
		}
		if action.researchAction != "" {
			label = "Research " + action.researchAction + " requested with an audited reason. Applies at the next phase barrier; current finite-phase work may still execute."
		}
		return m, m.mutate(action.operation, action.args(), label)
	}
	return m, nil
}

func (m console) teamReviewPanel(width, height int) string {
	a := m.teamAction
	if a.operation == "control.update" {
		return m.legacyCollaborationPanel(width, height)
	}
	if a.editKind != "" {
		return m.teamEditorPanel(width, height)
	}
	if a.operation == "capacity.update" {
		return m.budgetReviewPanel(width, height)
	}
	var out strings.Builder
	if a.researchAction != "" {
		instruction := "Type a reason · Enter reviews · Ctrl-U clears · Esc cancels"
		if a.reasonReady {
			instruction = "Enter applies reviewed action · Esc cancels"
		}
		fmt.Fprintf(&out, "%s\n%s\n\nAction: %s\nTrial: %s\nControl version: %d\n\nReason:\n%s\n\n", consoleAccent.Render("Research intervention"), consoleMuted.Render(instruction), consoleAccent.Render(a.researchAction), consoleContent(a.teamName, 160), a.version, consoleContent(a.reason, 512))
		if !a.reasonReady {
			out.WriteString(consoleMuted.Render("Type a reason · Enter reviews · Ctrl-U clears · Esc cancels"))
		} else {
			out.WriteString(consoleMuted.Render("Enter applies this exact reviewed action · Esc cancels\n\nPause/step/stop apply at phase barriers, leaving the current finite phase\nto finish. The runner's acknowledged version confirms application."))
		}
		return consoleClip(out.String(), width, height)
	}
	fmt.Fprintf(&out, "%s\n%s\n\n%s\nID: %s\nVersion: %d\n", consoleAccent.Render("Review owner team action"), consoleMuted.Render("Enter applies exact versioned action · Esc cancels"), consoleAccent.Render(a.operation), consoleContent(a.teamID, 80), a.version)
	if a.agentID != "" {
		fmt.Fprintf(&out, "Stable ID: %s\n", consoleContent(a.agentID, 80))
	}
	fmt.Fprintf(&out, "Team: %s\n", consoleClip(consoleContent(a.teamName, 160), width-6, 1))
	if a.agentID != "" {
		fmt.Fprintf(&out, "Agent: %s\n", consoleClip(consoleContent(m.agentLabel(a.agentID), 200), width-7, 1))
	}
	if a.operation == "team.invite" {
		out.WriteString("\nThis sends an invitation. Only the invited agent can consent to join.\n")
	} else if a.operation == "team.transfer" {
		out.WriteString("\nThis proposes ownership. Only the proposed member can accept.\nCurrent owner keeps authority until that separate consent.\n")
	} else if a.operation == "team.update" {
		fmt.Fprintf(&out, "\nName: %s\nDescription: %s\n", consoleContent(a.name, 128), consoleContent(a.description, 4096))
	} else {
		out.WriteString("\nThis revokes future team access. Previously read knowledge remains.\n")
	}
	out.WriteString("Native terminals and accepted tasks keep running.\n\nEnter applies this exact versioned action · Esc cancels")
	return consoleClip(out.String(), width, height)
}

func (m console) teamsPanel(width, height int) string {
	var out strings.Builder
	out.WriteString(consoleAccent.Render("Consented teams") + "\n")
	out.WriteString(consoleMuted.Render(consoleClip(m.teamKeyHints(), width, 2)) + "\n\n")
	if m.data.teamsErr != nil {
		return consoleClip(out.String()+"Teams unavailable: "+consoleContent(m.data.teamsErr.Error(), 200), width, height)
	}
	if len(m.data.teams) == 0 {
		out.WriteString(consoleMuted.Render("No agent-created teams.\n\nFor everyday collaboration: press 3 for Connections, choose a recipient\nwith h/l or Alt+←/→, then c to connect the selected pair.\n\nDynamic teams appear when a permitted agent creates one.\nInvitations require that agent's own acceptance; they grant no access alone."))
		return consoleClip(out.String(), width, height)
	}
	selected := m.teamIndex % len(m.data.teams)
	t := m.data.teams[selected]
	if m.offline {
		out.WriteString(consoleAccent.Render("Broker offline · saved team state · changes unavailable") + "\n")
	}

	fmt.Fprintf(&out, "%s · %s · %d/%d\n%s · v%d\n", consoleSelected.Render(ansi.Truncate(consoleContent(t.Name, 90), max(1, width-22), "…")), t.State, selected+1, len(m.data.teams), consoleMuted.Render(consoleContent(t.ID, 80)), t.Version)
	if target := m.teamTarget(); target != nil {
		fmt.Fprintf(&out, "Action target: %s\n", consoleAccent.Render(consoleMiddle(target.ID+" · "+m.agentLabel(target.ID), max(1, width-15))))
		membership := "not invited"
		if teamContains(t.Invited, target.ID) {
			membership = "awaiting consent"
		}
		if teamContains(t.Members, target.ID) {
			membership = "consented member"
		}
		if target.ID == t.Owner {
			membership = "team owner"
		}
		fmt.Fprintf(&out, "Target status: %s · %s\n", target.State, membership)
		if teamContains(t.Members, target.ID) {
			if access, ok := t.MemberAccess[target.ID]; ok {
				if !access.Allowed {
					out.WriteString(consoleAccent.Render("Membership retained · effective access blocked") + "\n")
					fmt.Fprintf(&out, "Reason: %s\n", consoleClip(consoleContent(access.Reason, 160), width-8, 1))
					if access.BlockedBy != "" {
						fmt.Fprintf(&out, "Blocked by ID: %s\n", consoleContent(access.BlockedBy, 100))
					}
				} else {
					out.WriteString(consoleMuted.Render("Current board + memory access allowed") + "\n")
				}
			} else {
				out.WriteString(consoleMuted.Render("Effective access unavailable; membership alone does not grant access.") + "\n")
			}
		}
	}
	if t.PendingOwner != "" {
		fmt.Fprintf(&out, "Owner proposal: %s · awaiting that agent's consent\n", consoleContent(t.PendingOwner, 100))
	}
	blocked := []string{}
	for _, id := range t.Members {
		if access, ok := t.MemberAccess[id]; ok && !access.Allowed {
			blocked = append(blocked, id)
		}
	}
	if len(blocked) > 0 {
		fmt.Fprintf(&out, "Blocked members (%d): %s\n", len(blocked), consoleClip(consoleContent(strings.Join(blocked, ", "), 500), max(1, width-22), 1))
	}
	fmt.Fprintf(&out, "%s\n%s\n%s\nBoard: %s\nMemory: %s\n", consoleClip("Owner: "+consoleContent(m.agentLabel(t.Owner), 160), width, 1), consoleClip(fmt.Sprintf("Members (%d): %s", len(t.Members), consoleContent(m.teamAgentLabels(t.Members), 500)), width, 1), consoleClip(fmt.Sprintf("Awaiting consent: %d · %s", len(t.Invited), consoleContent(m.teamAgentLabels(t.Invited), 500)), width, 1), consoleContent(t.Board, 100), consoleContent(t.Memory, 100))
	if m.data.capacityErr == nil && m.data.capacity.Agent != "" {
		fmt.Fprintf(&out, "Lifetime teams remaining: %d · team slots left: %d\n", m.data.capacity.TeamsRemaining, max(0, m.data.capacity.TeamMemberLimit-len(t.Members)-len(t.Invited)))
	}
	out.WriteString(consoleMuted.Render("Owner controls membership; invitation acceptance stays with the agent.\nRemoving/dissolving a team does not close native terminals."))
	return consoleClip(out.String(), width, height)
}

func (m console) teamAgentLabels(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	labels := make([]string, 0, len(ids))
	for _, id := range ids {
		labels = append(labels, m.agentLabel(id))
	}
	return strings.Join(labels, ", ")
}

func (m console) eventsPanel(width, height int) string {
	var out strings.Builder
	out.WriteString(m.studyPanel(width))
	out.WriteString(m.researchPanel(width))
	out.WriteString(consoleMuted.Render(fmt.Sprintf("History page %d · Shift+F search · g scope · PgUp/PgDn", len(m.eventBack)+1)) + "\n")
	if m.eventScope {
		out.WriteString("Scope: " + m.selectedID() + "\n")
	}
	if m.eventQuery != "" {
		out.WriteString("Search: " + consoleContent(m.eventQuery, 120) + "\n")
	}
	if m.eventPinned != nil {
		out.WriteString(consoleAccent.Render("Pinned event · Home returns live") + "\n")
	}
	out.WriteString(consoleAccent.Render("Events & topology") + consoleMuted.Render("  [/] select · Home latest/live · o details") + "\n\n")
	events := m.data.activity.Events
	if len(events) == 0 {
		return consoleClip(out.String()+consoleMuted.Render("No recent broker events. Agent thinking and native tool execution\nare visible only when exposed by the native terminal/provider."), width, height)
	}
	selected := m.eventIndex % len(events)
	e := events[selected]
	fmt.Fprintf(&out, "%s · %d/%d · %s\nActor: %s\nEntity: %s\n", consoleAccent.Render(consoleContent(e.Type, 100)), selected+1, len(events), consoleTime(e.At), consoleContent(m.agentLabel(e.Actor), 200), consoleContent(e.EntityID, 100))
	if len(e.Details) > 0 {
		data, _ := json.MarshalIndent(e.Details, "", "  ")
		out.WriteString(consoleContent(string(data), 2000) + "\n")
	}
	out.WriteString("\n" + consoleMuted.Render("Events record observed operations and supplied reasons. They do not\nreveal an agent's intent or prove that a message caused its behavior."))
	return consoleClip(out.String(), width, height)
}

func (m *console) reviewResearchAction(action string) {
	if m.busy {
		return
	}
	if m.data.researchErr != nil {
		m.message = "Research status unavailable; refresh before intervening."
		return
	}
	s := m.data.research
	if s == nil {
		m.message = "No live research runner is registered in this workspace."
		return
	}
	if s.Control.Mode == "stopped" || s.Progress.State == "completed" || s.Progress.State == "interrupted" || s.Progress.State == "stopped" {
		m.message = "This research attempt has ended; start/resume through the research runner CLI."
		return
	}
	m.teamAction = &consoleTeamAction{operation: "research.control", researchAction: action, version: s.Control.Version, teamName: fmt.Sprintf("%s · attempt %d · round %d / %s", s.Progress.Trial, s.Progress.Attempt, s.Progress.Round, s.Progress.Phase)}
}

func (m console) researchPanel(width int) string {
	if m.data.researchErr != nil {
		return "Research status unavailable: " + consoleContent(m.data.researchErr.Error(), 140) + "\n\n"
	}
	s := m.data.research
	if s == nil {
		return ""
	}
	p, c := s.Progress, s.Control
	ack := "applied"
	if p.AckVersion < c.Version {
		ack = "awaiting next barrier"
	}
	heartbeat := "runner heartbeat unknown"
	if at, err := time.Parse(time.RFC3339Nano, s.RunnerHeartbeatAt); err == nil {
		age := time.Since(at)
		if age >= -2*time.Second && age <= 10*time.Second {
			heartbeat = "runner heartbeat fresh"
		} else {
			heartbeat = "runner heartbeat stale; tasks may still run"
		}
	}
	if m.offline {
		heartbeat = "broker offline · saved research state · writes disabled"
	}
	return consoleClip(fmt.Sprintf("%s · %s · attempt %d\nRound %d / %s · runner %s\nadmissions %s · %s\nControl v%d / acknowledged v%d\nModel tasks %d/%d · owner mutations %d\n%s\nP pause · R resume · S step · T stop\n\n", consoleAccent.Render("Research"), consoleContent(p.Condition, 100), p.Attempt, p.Round, consoleContent(p.Phase, 50), p.State, c.Mode, ack, c.Version, p.AckVersion, p.ModelTasksReserved, p.ModelTasksLimit, s.OwnerMutations, heartbeat), width, 8)
}

func (m console) agentRuntimePanel(width int) string {
	if len(m.agents) == 0 {
		return ""
	}
	a := m.agents[m.selected%len(m.agents)]
	var out strings.Builder
	if a.Parent != "" {
		fmt.Fprintf(&out, "Parent: %s · template: %s\n", consoleContent(m.agentLabel(a.Parent), 120), consoleContent(a.Template, 100))
	}
	children := []string{}
	for _, child := range m.agents {
		if child.Parent == a.ID {
			children = append(children, child.ID+" ("+child.State+")")
		}
	}
	if len(children) > 0 {
		out.WriteString("Children: " + consoleContent(strings.Join(children, ", "), 400) + "\n")
	}
	if a.Config.Interaction == "interactive" {
		if pane, ok := m.data.runtime.Agents[a.ID]; ok && pane.State != "" {
			if m.offline {
				out.WriteString("Saved native process state: " + consoleContent(pane.State, 60) + " · current state unknown\n")
			} else if pane.State != "running" {
				out.WriteString(consoleAccent.Render("NATIVE CLI "+strings.ToUpper(consoleContent(pane.State, 60))) + " · coordination identity retained\n")
				if a.State == "active" && m.data.runtime.RelaunchVersion == 1 {
					out.WriteString("Enter or Shift+U reviews a same-ID relaunch. Nothing restarts automatically.\n")
				} else {
					out.WriteString("Enter explains recovery requirements. Nothing restarts automatically.\n")
				}
			} else {
				out.WriteString("Native process: running\n")
			}
		}
		out.WriteString("Human-led native session · direct terminal interaction available\n")
		out.WriteString(m.nativeDeliverySummary(width))
	} else if m.data.runtimeErr != nil {
		out.WriteString("Managed runtime status unavailable: " + consoleContent(m.data.runtimeErr.Error(), 150) + "\n")
	} else if m.data.autonomy.Agent == a.ID {
		s := m.data.autonomy
		mode := "automatic wakeups off"
		if s.Enabled {
			mode = "automatic admissions " + s.Mode
		}
		fmt.Fprintf(&out, "%s · in-flight %d\nQueued observations %d · queued instructions %d\n", mode, s.Inflight, s.Pending, s.PendingInstructions)
		fmt.Fprintf(&out, "Status: %s · %s\n", s.State, consoleContent(s.Reason, 150))
		if s.AdmissionBlocked && s.AdmissionReason != s.Reason {
			fmt.Fprintf(&out, "Admission: %s\n", consoleContent(s.AdmissionReason, 150))
		}
		if s.Enabled {
			fmt.Fprintf(&out, "Turns %d/%d · hop cap %d · dropped %d · suppressed %d\n", s.TurnsUsed, s.MaxTurns, a.Config.Autonomy.MaxHops, s.Dropped, s.Suppressed)
			if s.NextAllowedAt != "" {
				fmt.Fprintf(&out, "Next allowed: %s\n", consoleContent(s.NextAllowedAt, 100))
			}
			out.WriteString("a pause/resume admissions; accepted work needs Tasks → c cancel.\n")
		}
		if a.Config.Conversation == "persistent" {
			c := m.data.conversation
			if c.Agent == a.ID {
				fmt.Fprintf(&out, "Native history: %s · generation %d\n", c.State, c.Generation)
				if c.State != "ready" {
					out.WriteString("Stop/resolve uncertain delivery before explicit owner history reset.\n")
				}
			} else {
				out.WriteString("Native history status unavailable; resume is not assumed safe.\n")
			}
		}
	} else {
		out.WriteString("Managed runtime status loading or unavailable; execution is unknown.\n")
	}
	out.WriteString(m.capacityPanel(width))
	for _, t := range m.data.activity.Tasks {
		if t.To == a.ID && (t.State == "running" || t.Delivery == "uncertain" || t.Delivery == "cancel_requested" || t.Delivery == "cancel_uncertain") {
			fmt.Fprintf(&out, "Current task: %s · %s / %s\n", consoleContent(t.ID, 80), t.State, t.Delivery)
			if t.Delivery == "uncertain" || t.Delivery == "cancel_uncertain" {
				out.WriteString("Tasks → Ctrl+K reviews exact fail/retry recovery after checking side effects and stopping the old process.\n")
			}
			break
		}
	}
	return consoleClip(out.String(), width, 17)
}
