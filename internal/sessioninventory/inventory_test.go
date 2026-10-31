package sessioninventory

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

const started = "Sat Oct 31 19:51:40 2026"

func fixtureProcess(pid, parent int, tty, name string) process {
	return process{pid: pid, parent: parent, tty: tty, state: "S+", start: started, name: name}
}
func discoverFixture(processes map[int]process, panes []pane, excluded ...int) []Session {
	return discover(context.Background(), processes, panes, excluded, func(p process) string { return lifetimeID(p.pid, p.start) }, func(pid int) string { return fmt.Sprintf("/work/project-%d", pid) })
}

func TestLifetimeStableAcrossTmuxDiscoveryAndDistinctAfterPIDReuse(t *testing.T) {
	processes := map[int]process{10: fixtureProcess(10, 1, "pts/1", "bash"), 20: fixtureProcess(20, 10, "pts/1", "claude")}
	standalone := discoverFixture(processes, nil)
	panes := parsePanes("70\t/tmp/tmux-1000/default\t%9\t10\twork\tClaude\t/work\tclaude\n")
	attached := discoverFixture(processes, panes)
	if len(standalone) != 1 || len(attached) != 1 {
		t.Fatalf("unexpected inventories: %#v %#v", standalone, attached)
	}
	if standalone[0].ID != attached[0].ID {
		t.Fatal("tmux discovery changed session identity")
	}
	if attached[0].Source != "external-tmux" || attached[0].Pane != "%9" || attached[0].TTY != "pts/1" || attached[0].Coordination {
		t.Fatalf("incorrect metadata: %#v", attached[0])
	}
	replacement := processes[20]
	replacement.start = "Sat Oct 31 20:51:40 2026"
	processes[20] = replacement
	if discoverFixture(processes, panes)[0].ID == attached[0].ID {
		t.Fatal("reused process PID retained identity")
	}
}

func TestExcludesManagedTreesHeadlessZombiesAndDuplicateLaunchers(t *testing.T) {
	processes := map[int]process{
		10: fixtureProcess(10, 1, "pts/1", "claude"),
		11: fixtureProcess(11, 10, "pts/1", "claude"),
		12: fixtureProcess(12, 11, "pts/1", "bash"),
		13: fixtureProcess(13, 12, "pts/2", "codex"),
		20: fixtureProcess(20, 1, "?", "claude"),
		30: fixtureProcess(30, 1, "pts/3", "pi"),
		40: fixtureProcess(40, 1, "pts/4", "node"),
	}
	zombie := processes[30]
	zombie.state = "Z+"
	processes[30] = zombie
	inventory := discoverFixture(processes, nil)
	if len(inventory) != 2 || inventory[0].PID != 11 || inventory[1].PID != 13 {
		t.Fatalf("unexpected sessions: %#v", inventory)
	}
	if excluded := discoverFixture(processes, nil, 10); len(excluded) != 0 {
		t.Fatalf("excluded managed descendants remain: %#v", excluded)
	}
}

func TestLinkedTmuxPanesChooseDeterministicSessionAndQuoteCommands(t *testing.T) {
	rows := []string{
		"70\t/tmp/default\t%9\t10\tzeta\twindow\t/work\tclaude",
		"70\t/tmp/default\t%9\t10\ta'b;$(touch /tmp/never)\twindow\t/work\tclaude",
	}
	processes := map[int]process{10: fixtureProcess(10, 1, "pts/1", "bash"), 11: fixtureProcess(11, 10, "pts/1", "claude")}
	first := discoverFixture(processes, parsePanes(strings.Join(rows, "\n")))
	second := discoverFixture(processes, parsePanes(rows[1]+"\n"+rows[0]))
	if len(first) != 1 || len(second) != 1 || first[0] != second[0] {
		t.Fatalf("linked windows duplicated/unstable: %#v %#v", first, second)
	}
	command := first[0].AttachCommand
	if !strings.Contains(command, "-t '%9'") || !strings.Contains(command, "'\"'\"'") || !strings.Contains(command, "attach-session -t '=a") {
		t.Fatalf("unsafe or imprecise command: %s", command)
	}
}

func TestParsersRejectMalformedInputAndPreserveSpacedExecutableNames(t *testing.T) {
	raw := "7 1 pts/7 S+ " + started + " /Applications/Antigravity\n" +
		"8 1 pts/8 S+ " + started + " /Applications/something else\n" +
		"9 invalid pts/9 S+ " + started + " claude\n" +
		"-10 1 pts/10 S+ " + started + " claude\n" +
		"11 1 pts/11 S+ not a valid timestamp claude\n"
	p := parseProcesses(raw)
	if len(p) != 2 || provider(p[7].name) != "antigravity" || p[8].name != "/Applications/something else" {
		t.Fatalf("bad process parse: %#v", p)
	}
	bad := []string{
		"70\t/tmp/default\t%9;evil\t10\twork\twindow\t/work\tclaude",
		"70\t/tmp/default\t%+9\t10\twork\twindow\t/work\tclaude",
		"70\t/tmp/default\t%9\t0\twork\twindow\t/work\tclaude",
		"70\t/tmp/default\t%9\t10\twork\r\twindow\t/work\tclaude",
		"70\t/tmp/default\t%9\t10\twork\twindow\t/work\tclaude\textra",
	}
	if parsed := parsePanes(strings.Join(bad, "\n")); len(parsed) != 0 {
		t.Fatalf("malformed panes accepted: %#v", parsed)
	}
}

func TestProcessCyclesAndLifetimeStartParsing(t *testing.T) {
	processes := map[int]process{1: fixtureProcess(1, 2, "pts/1", "bash"), 2: fixtureProcess(2, 1, "pts/1", "claude")}
	if descends(processes, 1, 99) || !descends(processes, 2, 1) || descends(processes, 2, 0) {
		t.Fatal("cyclic process ancestry mishandled")
	}
	tail := make([]string, 20)
	for i := range tail {
		tail[i] = "0"
	}
	tail[0] = "S"
	tail[19] = "123456789"
	if got := procStart("45 (name with ) parentheses) " + strings.Join(tail, " ")); got != "123456789" {
		t.Fatalf("start ticks: %q", got)
	}
	if procStart("malformed") != "" || procStart("45 (x) S 1") != "" {
		t.Fatal("malformed proc stat accepted")
	}
}

func TestMetadataCannotEmitTerminalControlsAndOutputIsBounded(t *testing.T) {
	clean := displayText("proj\x1b[31m\n\r\u202ename\u2066")
	if strings.ContainsAny(clean, "\x1b\n\r\u202e\u2066") {
		t.Fatalf("unsafe metadata: %q", clean)
	}
	var b limitedOutput
	input := []byte(strings.Repeat("x", 3<<20))
	n, err := b.Write(input)
	if err != nil || n != len(input) || b.Len() != 2<<20 {
		t.Fatalf("output cap failed: %d %d %v", n, b.Len(), err)
	}
}

func TestCanceledDiscoveryDoesNotReturnPartialObservations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	processes := map[int]process{10: fixtureProcess(10, 1, "pts/1", "claude")}
	got := discover(ctx, processes, nil, nil, func(p process) string { return lifetimeID(p.pid, p.start) }, func(int) string { return "" })
	if len(got) != 0 {
		t.Fatalf("canceled inventory returned observations: %#v", got)
	}
}
