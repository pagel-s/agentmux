package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConsoleArrowViewNavigationPreservesTargetSelection(t *testing.T) {
	for tab := 0; tab < 8; tab++ {
		for _, direction := range []struct {
			key  tea.KeyType
			step int
		}{{tea.KeyLeft, -1}, {tea.KeyRight, 1}} {
			t.Run(fmt.Sprintf("view%d/%d", tab, direction.step), func(t *testing.T) {
				m := teamConsoleFixture()
				m.tab, m.target = tab, 1
				next, cmd := consolePress(m, tea.KeyMsg{Type: direction.key})
				want := (tab + direction.step + 8) % 8
				if next.tab != want || next.target != m.target || next.teamMemberIndex != m.teamMemberIndex || next.selected != m.selected {
					t.Fatal("view arrows changed the selected agent or an invisible target")
				}
				if (cmd != nil) != (want == 4 || want == 0 || want == 7) {
					t.Fatal("arrow navigation did not fetch Tasks on entry")
				}
			})
		}
	}
}

func TestConsoleArrowWalksReachEveryViewInBothDirections(t *testing.T) {
	root, err := os.MkdirTemp("", "amx-nav-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socket := filepath.Join(root, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var operations []string
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request managed.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		operations = append(operations, request.Operation)
		mu.Unlock()
		// A Tasks-entry refresh must remain a read, even when the broker
		// cannot supply a registry. Do not execute real coordination writes.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"offline fixture"}}`))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	for _, scenario := range []string{"populated", "empty", "offline"} {
		t.Run(scenario, func(t *testing.T) {
			m := teamConsoleFixture()
			m.tab, m.target, m.selected = 0, 2, 1
			if scenario == "empty" {
				m.agents, m.data.teams = nil, nil
				m.target, m.teamMemberIndex, m.selected = 0, 0, 0
			}
			if scenario == "offline" {
				m.offline, m.data.teamsErr = true, errors.New("offline fixture")
			}
			wantTarget, wantMember, wantSelected := m.target, m.teamMemberIndex, m.selected
			m.client = managed.NewClient(managed.Credential{AgentID: "navigation-test", Socket: socket})
			for _, direction := range []struct {
				key  tea.KeyType
				step int
			}{{tea.KeyRight, 1}, {tea.KeyLeft, -1}} {
				// Keep the same model across repeated full laps: isolated
				// single-hop checks can miss a view that consumes the next arrow.
				for i := 0; i < 16; i++ {
					want := (m.tab + direction.step + 8) % 8
					var cmd tea.Cmd
					m, cmd = consolePress(m, tea.KeyMsg{Type: direction.key})
					if m.tab != want || m.target != wantTarget || m.teamMemberIndex != wantMember || m.selected != wantSelected {
						t.Fatalf("%s step %d reached view %d, want %d, or changed an action target", tea.KeyMsg{Type: direction.key}.String(), i, m.tab, want)
					}
					if m.busy || m.composer != "" || m.teamAction != nil {
						t.Fatal("view navigation opened an editor or prepared a mutation")
					}
					if (cmd != nil) != (want == 4 || want == 0 || want == 7) {
						t.Fatalf("view %d returned an unexpected command", want)
					}
					if cmd != nil {
						if _, ok := cmd().(consoleData); !ok {
							t.Fatal("Tasks entry issued an action instead of a read refresh")
						}
					}
				}
			}
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(operations) != 36 {
		t.Fatalf("unexpected refresh count: %v", operations)
	}
	for _, operation := range operations {
		if operation != "agent.list" {
			t.Fatalf("view arrows issued a coordination action: %s", operation)
		}
	}
}

func TestConsoleTargetKeysAreLocalToConnectionsAndTeams(t *testing.T) {
	for tab := 0; tab < 8; tab++ {
		for _, direction := range []struct {
			key  tea.KeyMsg
			step int
		}{
			{tea.KeyMsg{Type: tea.KeyLeft, Alt: true}, -1},
			{tea.KeyMsg{Type: tea.KeyRight, Alt: true}, 1},
			{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}}, -1},
			{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}}, 1},
		} {
			m := teamConsoleFixture()
			m.tab, m.target = tab, 1
			next, cmd := consolePress(m, direction.key)
			wantTarget, wantMember := m.target, m.teamMemberIndex
			if tab == 2 {
				wantTarget = (wantTarget + direction.step + len(m.agents)) % len(m.agents)
			}
			if tab == 6 {
				wantMember = (wantMember + direction.step + len(m.agents)) % len(m.agents)
			}
			if cmd != nil || next.tab != tab || next.target != wantTarget || next.teamMemberIndex != wantMember || next.selected != m.selected {
				t.Fatalf("%s from view %d changed the wrong target or switched view", direction.key.String(), tab)
			}
		}
	}
}

func TestConsoleModifiedArrowsNavigateEveryView(t *testing.T) {
	for tab := 0; tab < 8; tab++ {
		for _, direction := range []struct {
			key  tea.KeyType
			step int
		}{{tea.KeyCtrlLeft, -1}, {tea.KeyCtrlRight, 1}} {
			m := teamConsoleFixture()
			m.tab, m.target = tab, 1
			next, cmd := consolePress(m, tea.KeyMsg{Type: direction.key})
			want := (tab + direction.step + 8) % 8
			if next.tab != want || next.target != m.target || next.teamMemberIndex != m.teamMemberIndex {
				t.Fatalf("Ctrl-arrow from view %d changed a target or failed to navigate", tab)
			}
			if (cmd != nil) != (want == 4 || want == 0 || want == 7) {
				t.Fatalf("Ctrl-arrow from view %d did not fetch Tasks on entry", tab)
			}
		}
	}
}

func TestConsoleEveryTaskEntryFetchesAndTabsWrap(t *testing.T) {
	for _, entry := range []struct {
		tab int
		key tea.KeyMsg
	}{
		{3, tea.KeyMsg{Type: tea.KeyRight}},
		{5, tea.KeyMsg{Type: tea.KeyLeft}},
		{3, tea.KeyMsg{Type: tea.KeyCtrlRight}},
		{5, tea.KeyMsg{Type: tea.KeyCtrlLeft}},
		{0, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}}},
	} {
		m := console{tab: entry.tab}
		next, cmd := consolePress(m, entry.key)
		if next.tab != 4 || cmd == nil {
			t.Fatalf("%s from view %d did not load the task history page", entry.key.String(), entry.tab)
		}
	}
	for _, wrap := range []struct {
		tab, want int
		key       tea.KeyType
	}{{0, 7, tea.KeyLeft}, {7, 0, tea.KeyRight}} {
		next, _ := consolePress(console{tab: wrap.tab}, tea.KeyMsg{Type: wrap.key})
		if next.tab != wrap.want {
			t.Fatalf("%s failed to wrap at the end of the view strip", tea.KeyMsg{Type: wrap.key}.String())
		}
	}
}

func TestConsoleArrowViewChangeInvalidatesPendingMemory(t *testing.T) {
	m := console{tab: 3, memoryOpen: true, memoryLoading: true, busy: true, memoryRequest: 7, memoryNamespace: "old", agents: []managed.AgentInfo{{ID: "alice"}}}
	next, _ := consolePress(m, tea.KeyMsg{Type: tea.KeyLeft})
	if next.memoryOpen || next.memoryLoading || next.busy || next.memoryRequest != 8 || next.memoryNamespace != "" {
		t.Fatal("leaving Memory retained its open namespace or pending read")
	}
	result, cmd := next.Update(consoleMemory{agent: "alice", request: 7, namespace: "old", entries: []managed.Memory{{Key: "late", Value: "stale"}}})
	next = result.(console)
	if cmd != nil || next.memoryOpen || len(next.memoryEntries) != 0 {
		t.Fatal("late memory response reopened the namespace after arrow navigation")
	}
}

func TestConsoleViewArrowsStayInsideDraftsReadersAndReviews(t *testing.T) {
	m := teamConsoleFixture()
	m.compose("send", "alice", "", "abc")
	m.textCursor = 1
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.textCursor != 2 || m.tab != 6 || string(m.text) != "abc" {
		t.Fatal("Right escaped the draft instead of moving its cursor")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyLeft})
	for _, key := range []tea.KeyType{tea.KeyCtrlLeft, tea.KeyCtrlRight} {
		m, cmd := consolePress(m, tea.KeyMsg{Type: key})
		if cmd != nil || m.tab != 6 || m.composer != "send" || m.composeTarget != "alice" || string(m.text) != "abc" || m.textCursor != 1 {
			t.Fatal("Ctrl-arrow escaped or changed a draft")
		}
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hl")})
	if string(m.text) != "ahlbc" || m.tab != 6 {
		t.Fatal("navigation letters stopped being literal editor text")
	}

	m = console{tab: 2, width: 80, height: 24, reader: &consoleReader{title: "frozen", body: strings.Repeat("saved line\n", 40)}}
	for _, key := range []tea.KeyType{tea.KeyLeft, tea.KeyRight, tea.KeyCtrlLeft, tea.KeyCtrlRight} {
		next, cmd := consolePress(m, tea.KeyMsg{Type: key})
		if cmd != nil || next.tab != 2 || next.reader == nil || next.reader.title != "frozen" || next.reader.body != m.reader.body {
			t.Fatal("view arrows escaped the frozen reader")
		}
	}

	m = teamConsoleFixture()
	m.reviewTeamAction("team.invite")
	for _, key := range []tea.KeyType{tea.KeyLeft, tea.KeyRight, tea.KeyCtrlLeft, tea.KeyCtrlRight} {
		next, cmd := consolePress(m, tea.KeyMsg{Type: key})
		if cmd != nil || next.tab != 6 || next.teamAction == nil || next.teamAction.agentID != "bob" || next.teamAction.teamID != "team_original" || next.teamMemberIndex != m.teamMemberIndex {
			t.Fatal("view arrows escaped or retargeted an explicit action review")
		}
	}

	m = console{tab: 6, target: 1, teamMemberIndex: 2, workspacePicker: &consoleWorkspacePicker{rows: []workspaceListing{{State: "/tmp/first"}, {State: "/tmp/second"}}, selected: 1}}
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyLeft}, {Type: tea.KeyRight}, {Type: tea.KeyCtrlLeft}, {Type: tea.KeyCtrlRight},
		{Type: tea.KeyLeft, Alt: true}, {Type: tea.KeyRight, Alt: true},
	} {
		next, cmd := consolePress(m, key)
		if cmd != nil || next.tab != 6 || next.workspacePicker == nil || next.workspacePicker.selected != 1 || next.target != 1 || next.teamMemberIndex != 2 {
			t.Fatal("horizontal navigation escaped the workspace picker or changed its selection")
		}
	}
}

func TestConsoleArrowHintsMatchActiveView(t *testing.T) {
	for tab := 0; tab < 8; tab++ {
		m := console{tab: tab}
		want := "←/→ views"
		if !strings.Contains(m.contextualKeys(), want) {
			t.Fatalf("view %d hides its arrow navigation cue", tab)
		}
	}
}
