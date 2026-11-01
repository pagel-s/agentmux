package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestConsoleLongListsKeepExactSelectionVisibleAndIndependent(t *testing.T) {
	m := console{width: 80, height: 20, tab: 3, agents: []managed.AgentInfo{{ID: "alice"}}, data: consoleData{selected: "alice", boundaries: managed.Boundaries{Agent: "alice"}}}
	for i := range 40 {
		m.data.boundaries.Memory = append(m.data.boundaries.Memory, managed.BoundaryScope{Name: fmt.Sprintf("scope_%02d", i), Read: true})
		m.memoryEntries = append(m.memoryEntries, managed.Memory{Key: fmt.Sprintf("key_%02d", i), Value: "selected memory value"})
		m.data.activity.Tasks = append(m.data.activity.Tasks, managed.Task{ID: fmt.Sprintf("task_%02d", i), To: "alice", State: "failed", Result: "selected task result"})
	}
	m.task = 7
	for range 39 {
		m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	}
	if m.scope != 39 || m.task != 7 || !strings.Contains(m.View(), "scope_39") {
		t.Fatal("scope selection moved another view or became invisible")
	}
	m.memoryOpen = true
	m.memoryNamespace = "scope_39"
	m.memoryEntry = 39
	if view := m.View(); !strings.Contains(view, "key_39") || !strings.Contains(view, "selected memory value") {
		t.Fatal("selected memory row/value hidden in short terminal")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if m.memoryEntry != 0 || !strings.Contains(m.View(), "key_00") {
		t.Fatal("wrapped memory selection disappeared")
	}
	m.tab = 4
	m.task = 39
	if !strings.Contains(m.View(), "selected task result") {
		t.Fatal("task result hidden behind its instruction")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if m.task != 0 || !strings.Contains(m.View(), "task_00") {
		t.Fatal("wrapped task list lost selected row")
	}
}

func TestConsoleFullReaderFreezesContentAndIgnoresMutationKeys(t *testing.T) {
	m := console{tab: 4, width: 80, height: 20, agents: []managed.AgentInfo{{ID: "alice"}}, data: consoleData{selected: "alice", activity: managedActivitySnapshot{Tasks: []managed.Task{{ID: "original", To: "alice", Text: strings.Repeat("long instruction\n", 70), Result: "RESULT FIRST\n" + strings.Repeat("finding\n", 100) + "RESULT TAIL"}}}}}
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd != nil || m.reader == nil || !strings.Contains(m.View(), "RESULT FIRST") {
		t.Fatal("full reader did not expose task result first")
	}
	m.data.activity.Tasks[0] = managed.Task{ID: "replacement", To: "bob", Result: "different task"}
	for _, key := range []rune{'x', 'c', 's', 'i', 'd'} {
		m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if cmd != nil || m.busy {
			t.Fatal("reader key issued an owner mutation")
		}
	}
	if m.reader.title != "Task original" || strings.Contains(m.reader.body, "different task") || !strings.Contains(m.reader.body, "RESULT TAIL") {
		t.Fatal("poll redirected or truncated frozen reader")
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || m.reader != nil || m.tab != 4 {
		t.Fatal("reader Escape did not return to original view")
	}
}

func TestConsoleReaderScrollResizeAndEscapePreserveSelection(t *testing.T) {
	m := console{tab: 3, width: 80, height: 20, memoryOpen: true, memoryNamespace: "original", memoryEntries: []managed.Memory{{Key: "note", Value: strings.Repeat("界", 120) + "\n" + strings.Repeat("line\n", 60) + "END OF NOTE"}}}
	m.openReader()
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEnd})
	if !strings.Contains(m.View(), "END OF NOTE") {
		t.Fatal("reader End cannot reach long content")
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m = next.(console)
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEnd})
	if !strings.Contains(m.View(), "END OF NOTE") {
		t.Fatal("resize made reader tail inaccessible")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyHome})
	if m.reader.offset != 0 {
		t.Fatal("reader Home did not restore beginning")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m.reader != nil || m.memoryNamespace != "original" || !m.memoryOpen {
		t.Fatal("reader q closed the workspace or lost memory context")
	}
}

func TestConsoleEditorKeepsLongUnicodeDraftCursorVisible(t *testing.T) {
	text := []rune(strings.Repeat("earlier line\n", 30) + strings.Repeat("界", 90) + "LATEST EDIT")
	for _, cursor := range []int{len(text), 0, len(text) - 3} {
		view := consoleEditor(text, cursor, 60, 4)
		if !strings.Contains(view, "▏") || lipgloss.Height(view) > 4 {
			t.Fatal("draft cursor scrolled outside editor viewport")
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > 60 {
				t.Fatal("editor exceeded terminal columns")
			}
		}
		if cursor == len(text) && !strings.Contains(view, "LATEST EDIT") {
			t.Fatal("typing at long draft tail is invisible")
		}
	}
}

func TestConsoleMemoryRefreshKeepsEntryByKey(t *testing.T) {
	m := console{tab: 3, memoryOpen: true, memoryNamespace: "group", memoryEntry: 1, memoryLoading: true, memoryRequest: 9, agents: []managed.AgentInfo{{ID: "alice"}}, memoryEntries: []managed.Memory{{Key: "a"}, {Key: "selected", Value: "old"}}}
	next, _ := m.Update(consoleMemory{agent: "alice", request: 9, namespace: "group", entries: []managed.Memory{{Key: "selected", Value: "updated"}, {Key: "new"}}})
	m = next.(console)
	if m.memoryEntry != 0 || m.memoryEntries[m.memoryEntry].Key != "selected" {
		t.Fatal("refresh lost memory entry identity")
	}
}

func TestConsoleTeamsEnterUsesExplicitActionTarget(t *testing.T) {
	m := teamConsoleFixture()
	m.agents[1].External = true
	m.agents[1].Config = managedconfig.Agent{Interaction: "interactive"}
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.selectedID() != "bob" {
		t.Fatal("Teams Enter targeted unrelated roster selection")
	}
	action := cmd().(consoleAction)
	if !strings.Contains(action.details, "bob") {
		t.Fatal("external bridge guidance did not use exact Teams target")
	}
}

func TestConsoleSingleCharacterPastesNeverExecuteControls(t *testing.T) {
	for _, key := range []rune{'x', 'c', 'N', 'q', 'o', ']'} {
		m := teamConsoleFixture()
		for _, review := range []bool{false, true} {
			if review {
				m.teamAction = &consoleTeamAction{operation: "team.dissolve", teamID: "frozen", version: 2}
			}
			next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}, Paste: true})
			if cmd != nil || next.busy || next.composer != "" || next.teamIndex != m.teamIndex || next.reader != nil {
				t.Fatalf("single-character pasted %q executed a control", key)
			}
		}
	}
}

func TestConsolePhysicalSpaceEditsMainComposer(t *testing.T) {
	m := console{composer: "send", composeTarget: "alice"}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("Fix")}, {Type: tea.KeySpace}, {Type: tea.KeyRunes, Runes: []rune("login")}} {
		m, _ = consolePress(m, key)
	}
	if string(m.text) != "Fix login" {
		t.Fatalf("physical spaces disappeared from draft: %q", m.text)
	}
}

func TestConsoleFailedDraftRecoveryNeverRetargetsOrOverwritesAnotherDraft(t *testing.T) {
	draft := &consoleDraft{kind: "memory-value", target: "original/note", text: "my unsaved note", namespace: "original", key: "note", version: 4}
	m := console{composer: "send", composeTarget: "other", text: []rune("new unrelated draft"), textCursor: 19}
	next, _ := m.Update(consoleAction{err: errors.New("permission revoked"), draft: draft})
	m = next.(console)
	if m.composer != "send" || string(m.text) != "new unrelated draft" || m.failedDraft == nil {
		t.Fatal("failed response overwrote current editor or lost original draft")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd != nil || m.composer != "memory-value" || m.composeTarget != "original/note" || string(m.text) != "my unsaved note" || m.memoryNamespace != "original" || m.memoryVersion != 4 {
		t.Fatal("recovery submitted automatically or changed target/CAS version")
	}
}

func TestConsoleFailedNativeSetupRetainsProviderProjectAndRole(t *testing.T) {
	draft := consoleNativeDraft{title: "Original task", provider: "pi", project: "/project/original", role: "Reviewer"}
	m := console{}
	next, _ := m.Update(consoleAction{err: errors.New("provider not installed"), draft: &consoleDraft{kind: "new-title", target: "task", text: draft.title, native: &draft}})
	m = next.(console)
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	for range 4 {
		m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	if m.composer != "new-role" || m.newNative.provider != "pi" || m.newNative.project != "/project/original" || string(m.text) != "Reviewer" {
		t.Fatal("failed native setup lost reviewable draft")
	}
}

func TestConsoleLongNamesCannotHideReviewedStableTarget(t *testing.T) {
	m := teamConsoleFixture()
	m.height = 20
	m.data.teams[0].Name = strings.Repeat("Long team name ", 10)
	m.agents[1].Title = strings.Repeat("Same very long human label ", 6)
	m.reviewTeamAction("team.invite")
	for _, text := range []string{"team_original", "Stable ID: bob", "Version: 7", "Enter applies"} {
		if !strings.Contains(m.View(), text) {
			t.Fatalf("long labels obscured exact review target %q", text)
		}
	}
	m.teamAction = nil
	m.tab = 3
	m.scope = 19
	m.agents[0].Title = strings.Repeat("Same title ", 15)
	m.data.boundaries = managed.Boundaries{Agent: "alice"}
	for i := range 20 {
		m.data.boundaries.Memory = append(m.data.boundaries.Memory, managed.BoundaryScope{Name: fmt.Sprintf("scope_%02d", i)})
	}
	if !strings.Contains(m.View(), "scope_19") {
		t.Fatal("long session label hid selected boundary")
	}
}

func TestConsoleReaderPagingDoesNotSkipUnreadLinesWithWrappedFooter(t *testing.T) {
	m := console{width: 60, height: 20, message: strings.Repeat("status message ", 12), reader: &consoleReader{title: "frozen", body: strings.Repeat("line\n", 100)}}
	rows := m.readerPageRows()
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if cmd != nil || m.reader.offset > rows {
		t.Fatal("page scroll skipped unread rows")
	}
	// Footer wrapping and status feedback use the same height budget as View.
	view := m.View()
	if lipgloss.Height(view) > 20 {
		t.Fatal("reader exceeded resized terminal height")
	}
}

func TestConsoleNarrowTeamLayoutDoesNotIndentFollowingSections(t *testing.T) {
	// Styling a string ending in newlines pads its last empty line. Appending
	// another styled section there must not push the team identity off screen.
	m := teamConsoleFixture()
	for _, width := range []int{60, 80} {
		panel := ansi.Strip(m.teamsPanel(width, 13))
		lines := strings.Split(panel, "\n")
		for _, want := range []string{"Research pair", "team_original", "Action target: bob"} {
			found := false
			for i, line := range lines {
				if strings.HasPrefix(line, want) {
					if i > 6 {
						t.Fatalf("%d columns: important target moved below first seven rows: %q", width, panel)
					}
					found = true
				}
			}
			if !found {
				t.Fatalf("%d columns: section acquired padding or disappeared: %q in %q", width, want, panel)
			}
		}
	}
}

func TestConsoleRevocationIsDisclosedBeforeActionAndNeverClaimsProcessExit(t *testing.T) {
	m := console{width: 80, height: 24, agents: []managed.AgentInfo{{ID: "parent"}}}
	if !strings.Contains(m.View(), "x revoke + descendants") {
		t.Fatal("close shortcut concealed descendant revocation")
	}
	message := consoleRevocationMessage("parent", 3)
	for _, want := range []string{"active descendants (3 sessions)", "external native terminals stay open", "termination is not confirmed"} {
		if !strings.Contains(message, want) {
			t.Fatalf("misleading stop result: %q", message)
		}
	}
}

func TestConsoleMemoryConflictReviewRequiresExplicitAdoptionAndSeparateSave(t *testing.T) {
	m := console{width: 80, height: 20, composer: "memory-value", composeTarget: "shared/note", memoryNamespace: "shared", memoryKey: "note", memoryVersion: 3, text: []rune("my long retained draft"), textCursor: 22}
	review := consoleMemoryVersionReview{namespace: "shared", key: "note", draft: string(m.text), oldVersion: 3, version: 4, value: "other writer's saved value"}
	m.receiveMemoryReview(review)
	if strings.Contains(m.View(), "Enter saves · Ctrl-R") {
		t.Fatal("active reader displayed editor controls and stole its page budget")
	}
	if m.reader == nil || !strings.Contains(m.View(), "other writer's saved value") || m.memoryVersion != 3 {
		t.Fatal("fetch adopted version before review")
	}
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || m.memoryVersion != 3 || string(m.text) != review.draft {
		t.Fatal("cancel altered the draft or version")
	}
	m.receiveMemoryReview(review)
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.busy || m.reader != nil || m.memoryVersion != 4 || string(m.text) != review.draft || m.composer != "memory-value" {
		t.Fatal("version adoption submitted, erased or retargeted the draft")
	}
	// Ctrl-R in this editor reviews; outside it restores. It must not reuse a
	// previous unrelated failed draft or overwrite the current memory value.
	m.failedDraft = &consoleDraft{kind: "send", target: "someone-else", text: "unrelated"}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd == nil || !m.busy || m.composer != "memory-value" || string(m.text) != review.draft {
		t.Fatal("memory review shortcut restored an unrelated draft")
	}
}

func TestConsoleMemoryReviewDiscardsLateResponseAndKeepsOfflineDraft(t *testing.T) {
	m := console{composer: "memory-value", memoryNamespace: "shared", memoryKey: "new-key", memoryVersion: 1, text: []rune("newer edit")}
	m.receiveMemoryReview(consoleMemoryVersionReview{namespace: "shared", key: "old-key", draft: "old draft", oldVersion: 1, version: 2, value: "saved old note"})
	if m.reader != nil || m.memoryKey != "new-key" || string(m.text) != "newer edit" || m.memoryVersion != 1 {
		t.Fatal("late review followed navigation or draft edits")
	}
	m.offline = true
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd != nil || m.busy || string(m.text) != "newer edit" || !strings.Contains(m.message, "offline") {
		t.Fatal("offline version review lost draft or issued a request")
	}
}
