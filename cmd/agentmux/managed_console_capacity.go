package main

import (
	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strconv"
	"strings"
	"unicode"
)

var budgetFields = []string{"Turn tokens", "Turn cost (micro USD)", "Subtree tokens", "Subtree cost (micro USD)"}

func budgetValues(b managedconfig.AgentBudget) []int64 {
	return []int64{b.MaxTurnTokens, b.MaxTurnCostMicrousd, b.MaxSubtreeTokens, b.MaxSubtreeCostMicrousd}
}
func (m *console) reviewBudget() {
	if m.busy {
		return
	}
	if m.offline || m.data.capacityErr != nil || m.data.capacity.Agent != m.selectedID() {
		m.message = "Current admission limits unavailable; refresh before editing."
		return
	}
	a := consoleTeamAction{operation: "capacity.update", agentID: m.selectedID(), version: m.data.capacity.ResourceVersion, budget: m.data.budget, editKind: budgetFields[0], editValue: strconv.FormatInt(m.data.budget.MaxTurnTokens, 10)}
	m.teamAction = &a
}
func (m console) teamEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := *m.teamAction
	key := msg.String()
	switch key {
	case "esc":
		m.teamAction = nil
		return m, nil
	case "ctrl+c":
		return m, m.close()
	case "ctrl+u":
		a.editValue = ""
	case "backspace", "ctrl+h":
		r := []rune(a.editValue)
		if len(r) > 0 {
			a.editValue = string(r[:len(r)-1])
		}
	case "enter":
		if a.operation == "team.update" {
			if a.editKind == "Team name" {
				if strings.TrimSpace(a.editValue) == "" {
					m.message = "A team needs a name."
					return m, nil
				}
				a.name = a.editValue
				a.editKind, a.editValue = "Description", a.description
			} else {
				a.description = a.editValue
				a.editKind = ""
			}
		} else {
			value, err := strconv.ParseInt(a.editValue, 10, 64)
			if err != nil || value < 0 {
				m.message = "Enter a nonnegative whole number; 0 leaves that reservation/ceiling unset."
				return m, nil
			}
			switch a.editStep {
			case 0:
				a.budget.MaxTurnTokens = value
			case 1:
				a.budget.MaxTurnCostMicrousd = value
			case 2:
				a.budget.MaxSubtreeTokens = value
			case 3:
				a.budget.MaxSubtreeCostMicrousd = value
			}
			a.editStep++
			if a.editStep == len(budgetFields) {
				a.editKind = ""
			} else {
				a.editKind = budgetFields[a.editStep]
				a.editValue = strconv.FormatInt(budgetValues(a.budget)[a.editStep], 10)
			}
		}
	default:
		if msg.Type == tea.KeyRunes || key == " " {
			runes := msg.Runes
			if key == " " {
				runes = []rune{' '}
			}
			for _, r := range runes {
				if !unicode.IsPrint(r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0x061c || r == 0x200e || r == 0x200f {
					continue
				}
				limit := 4096
				if a.editKind == "Team name" {
					limit = 128
				}
				if a.operation == "capacity.update" {
					limit = 19
					if r < '0' || r > '9' {
						continue
					}
				}
				if len(a.editValue)+len(string(r)) <= limit {
					a.editValue += string(r)
				}
			}
		}
	}
	m.teamAction = &a
	return m, nil
}
func (m console) teamEditorPanel(width, height int) string {
	a := m.teamAction
	title := "Edit team metadata"
	context := "Team: " + a.teamName + " [" + a.teamID + "]"
	note := "Enter advances to review · Ctrl-U clears · Esc cancels"
	if a.operation == "capacity.update" {
		title = "Edit broker admission reservations"
		context = "Session ID: " + a.agentID
		note = "0 = unset · 1 USD = 1,000,000 micro USD\nPermanent charges remain. Native CLI billing is not capped.\nEnter advances through four fields, then reviews · Esc cancels"
	}
	return consoleClip(fmt.Sprintf("%s\n%s\n\n%s\nCAS version: %d\n\n%s\n%s▏\n\n%s", consoleAccent.Render(title), consoleMuted.Render("No changes apply until final review confirmation."), consoleContent(context, 250), a.version, consoleAccent.Render(a.editKind), consoleContent(a.editValue, 4096), consoleMuted.Render(note)), width, height)
}
func (m console) budgetReviewPanel(width, height int) string {
	a := m.teamAction
	b := a.budget
	return consoleClip(fmt.Sprintf("%s\n%s\n\nSession ID: %s\nResource version: %d\n\nTurn: %d tokens · %d micro USD\nSubtree: %d tokens · %d micro USD\n\n0 means no declared reservation/ceiling.\nEach attempt permanently reserves its full declared allowance.\nExisting charges cannot be reduced or refunded.\nApplies to broker-managed turns; native inference/tools are separate.\n\n%s", consoleAccent.Render("Review admission reservation limits"), consoleMuted.Render("Enter applies exact resource version · Esc cancels"), consoleContent(a.agentID, 100), a.version, b.MaxTurnTokens, b.MaxTurnCostMicrousd, b.MaxSubtreeTokens, b.MaxSubtreeCostMicrousd, consoleMuted.Render("Admission control only; provider billing is not hard capped.")), width, height)
}
func (m console) capacityPanel(width int) string {
	if m.data.capacityErr != nil {
		return "Capacity unavailable: " + consoleContent(m.data.capacityErr.Error(), 100) + "\n"
	}
	c := m.data.capacity
	if c.Agent != m.selectedID() {
		return ""
	}
	text := fmt.Sprintf("Capacity: %d spawns · depth %d · %d teams left\n", c.SpawnRemaining, c.SpawnDepthRemaining, c.TeamsRemaining)
	if c.RuntimeRemainingSeconds != nil {
		text += fmt.Sprintf("Admission runtime remaining: %ds\n", *c.RuntimeRemainingSeconds)
	}
	if c.AdmissionBlocked {
		text += "Admission blocked: " + consoleContent(c.AdmissionReason, 150) + "\n"
	}
	text += "B edits admission reservations (provider billing is not capped).\n"
	return consoleClip(text, width, 5)
}

func (m *console) resetScope() tea.Cmd {
	if m.busy || m.offline || m.data.boundaries.Agent != m.selectedID() {
		return nil
	}
	scopes := append(append([]managed.BoundaryScope{}, m.data.boundaries.Boards...), m.data.boundaries.Memory...)
	if len(scopes) == 0 {
		return nil
	}
	index := m.scope % len(scopes)
	scope := scopes[index]
	if scope.Source == "team" {
		m.message = "Team access follows consent and lineage; change membership in Teams."
		return nil
	}
	kind := "board"
	if index >= len(m.data.boundaries.Boards) {
		kind = "memory"
	}
	m.busy = true
	return m.mutate("control.update", map[string]any{"version": m.data.boundaries.Version, "kind": kind, "agent": m.selectedID(), "namespace": scope.Name, "reset": true}, "Stored override removed. Effective access still requires active membership and delegation.")
}
