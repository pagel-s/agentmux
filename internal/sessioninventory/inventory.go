// Package sessioninventory discovers live native terminals without reading
// transcripts, prompts, process arguments, environments, or authentication files.
// Discovery is observational: it never connects agents or sends terminal input.
package sessioninventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Session struct {
	ID            string `json:"id"`
	Harness       string `json:"harness"`
	Project       string `json:"project"`
	Title         string `json:"title"`
	StartedAt     string `json:"started_at,omitempty"`
	Source        string `json:"source"`
	PID           int    `json:"pid"`
	TTY           string `json:"tty,omitempty"`
	Pane          string `json:"pane,omitempty"`
	TmuxSession   string `json:"tmux_session,omitempty"`
	AttachCommand string `json:"attach_command,omitempty"`
	Coordination  bool   `json:"coordination"`
}

type process struct {
	pid, parent             int
	tty, state, start, name string
}
type pane struct {
	server                           int
	socket, id                       string
	root                             int
	session, title, project, command string
}

func provider(name string) string {
	switch strings.ToLower(filepath.Base(name)) {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	case "pi":
		return "pi"
	case "agy", "antigravity":
		return "antigravity"
	}
	return ""
}

// Discover is best effort, bounded to two seconds, and requires a live TTY.
// Only the user's default tmux server is inspected. Dedicated Agentmux servers
// are represented by their authenticated registries instead. Returned IDs name
// observed process lifetimes, not provider conversation IDs. Native processes
// hidden behind a generic, unrenamed interpreter cannot be recognized safely.
func Discover(ctx context.Context, excludedPIDs []int) []Session {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data := commandOutput(ctx, "ps", "-U", strconv.Itoa(os.Getuid()), "-o", "pid=,ppid=,tty=,stat=,lstart=,comm=")
	processes := parseProcesses(string(data))
	format := "#{pid}\t#{socket_path}\t#{pane_id}\t#{pane_pid}\t#{session_name}\t#{window_name}\t#{pane_current_path}\t#{pane_current_command}"
	panes := parsePanes(string(commandOutput(ctx, "tmux", "-L", "default", "list-panes", "-a", "-F", format)))
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	identity := func(p process) string {
		start := p.start
		if raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(p.pid), "stat")); err == nil {
			if ticks := procStart(string(raw)); ticks != "" {
				start = strings.TrimSpace(string(boot)) + ":" + ticks
			}
		}
		return lifetimeID(p.pid, start)
	}
	project := func(pid int) string {
		path, _ := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "cwd"))
		return displayText(path)
	}
	return discover(ctx, processes, panes, excludedPIDs, identity, project)
}

// Cap retained command output even on machines with unusually large process
// tables. Commands never run through a shell and inherit no process arguments.
func commandOutput(ctx context.Context, name string, args ...string) []byte {
	cmd := exec.CommandContext(ctx, name, args...)
	// lstart is parsed as five whitespace-separated fields, irrespective of the
	// user's locale. The environment of discovered processes is never inspected.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var output limitedOutput
	cmd.Stdout = &output
	_ = cmd.Run()
	return output.Bytes()
}

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	const limit = 2 << 20
	n := len(p)
	if available := limit - b.Len(); available > 0 {
		if len(p) > available {
			p = p[:available]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func parseProcesses(data string) map[int]process {
	result := make(map[int]process)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		parent, err := strconv.Atoi(fields[1])
		if err != nil || parent < 0 {
			continue
		}
		if _, err := time.Parse("Mon Jan 2 15:04:05 2006", strings.Join(fields[4:9], " ")); err != nil {
			continue
		}
		result[pid] = process{pid: pid, parent: parent, tty: fields[2], state: fields[3], start: strings.Join(fields[4:9], " "), name: strings.Join(fields[9:], " ")}
	}
	return result
}

func parsePanes(data string) []pane {
	result := []pane{}
	seen := map[string]bool{}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 8 {
			continue
		}
		server, e1 := strconv.Atoi(f[0])
		root, e2 := strconv.Atoi(f[3])
		if e1 != nil || e2 != nil || server <= 0 || root <= 0 || !validPaneID(f[2]) || f[1] == "" || f[4] == "" {
			continue
		}
		if strings.ContainsAny(f[1]+f[4], "\r\x00\x1b") {
			continue
		}
		key := f[0] + ":" + f[1] + ":" + f[2]
		// Linked tmux windows can list the same pane in several sessions. Choose a
		// deterministic representative so its observation appears only once.
		entry := pane{server: server, socket: f[1], id: f[2], root: root, session: f[4], title: displayText(f[5]), project: displayText(f[6]), command: f[7]}
		if seen[key] {
			for i, old := range result {
				if old.server == server && old.socket == f[1] && old.id == f[2] && entry.session < old.session {
					result[i] = entry
				}
			}
			continue
		}
		seen[key] = true
		result = append(result, entry)
	}
	return result
}

func validPaneID(s string) bool {
	if len(s) < 2 || s[0] != '%' {
		return false
	}
	_, err := strconv.ParseUint(s[1:], 10, 64)
	return err == nil && !strings.ContainsAny(s[1:], "+-")
}

func liveTTY(p process) bool {
	return p.tty != "" && p.tty != "?" && p.tty != "??" && p.tty != "-" && !strings.ContainsAny(p.state, "ZX")
}

func descends(processes map[int]process, pid, root int) bool {
	if root <= 0 {
		return false
	}
	seen := map[int]bool{}
	for depth := 0; depth < 256 && pid > 0 && !seen[pid]; depth++ {
		if pid == root {
			return true
		}
		seen[pid] = true
		p, ok := processes[pid]
		if !ok {
			return false
		}
		pid = p.parent
	}
	return false
}

func discover(ctx context.Context, processes map[int]process, panes []pane, excluded []int, identity func(process) string, project func(int) string) []Session {
	result := []Session{}
	for _, p := range processes {
		if ctx.Err() != nil {
			break
		}
		harness := provider(p.name)
		if harness == "" || !liveTTY(p) {
			continue
		}
		skip := false
		for _, root := range excluded {
			if descends(processes, p.pid, root) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		// Some launchers retain a same-named parent process. Prefer the deepest live
		// instance on this terminal rather than reporting both wrappers and the UI.
		for _, child := range processes {
			if ctx.Err() != nil {
				skip = true
				break
			}
			if child.pid != p.pid && child.tty == p.tty && liveTTY(child) && provider(child.name) == harness && descends(processes, child.pid, p.pid) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		s := Session{ID: identity(p), Harness: harness, Project: project(p.pid), Source: "external-terminal", PID: p.pid, TTY: p.tty, StartedAt: processStartedAt(p.start, time.Local)}
		var selected *pane
		for i := range panes {
			item := &panes[i]
			if !descends(processes, p.pid, item.root) {
				continue
			}
			if selected == nil || item.id < selected.id || item.id == selected.id && item.session < selected.session {
				selected = item
			}
		}
		if selected != nil {
			s.Source = "external-tmux"
			s.Pane, s.TmuxSession, s.Title = selected.id, displayText(selected.session), selected.title
			if s.Project == "" {
				s.Project = selected.project
			}
			// Exact-session matching avoids tmux prefix matching. Each value is quoted,
			// and the pane ID has already been validated as numeric metadata.
			s.AttachCommand = "tmux -L default select-window -t " + shellQuote(s.Pane) + " \\; select-pane -t " + shellQuote(s.Pane) + " \\; attach-session -t " + shellQuote("="+selected.session)
		}
		if s.Title == "" && s.Project != "" {
			s.Title = filepath.Base(s.Project)
		}
		if s.Title == "" {
			s.Title = harness
		}
		result = append(result, s)
	}
	sortSessions(result)
	return result
}

// ps lstart is a wall-clock timestamp in the command's local timezone, not UTC.
// Normalize it for clients so displaying it never depends on the viewer's zone.
// This is the observed process start, not a conversation's creation timestamp.
func processStartedAt(raw string, location *time.Location) string {
	if location == nil {
		return ""
	}
	started, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", raw, location)
	if err != nil {
		return ""
	}
	wall := strings.Join(strings.Fields(raw), " ")
	formatWall := func(t time.Time) string { return t.In(location).Format("Mon Jan 2 15:04:05 2006") }
	if formatWall(started) != wall {
		return "" // A skipped daylight-saving wall time cannot be a real start.
	}
	// lstart omits the UTC offset. A repeated wall time during a timezone
	// transition cannot identify an instant accurately; leave it unknown.
	_, offset := started.Zone()
	for _, nearby := range []time.Time{started.Add(-48 * time.Hour), started.Add(48 * time.Hour)} {
		_, otherOffset := nearby.In(location).Zone()
		candidate := started.Add(time.Duration(offset-otherOffset) * time.Second)
		if !candidate.Equal(started) && formatWall(candidate) == wall {
			return ""
		}
	}
	return started.UTC().Format(time.RFC3339)
}

func lifetimeID(pid int, start string) string {
	digest := sha256.Sum256([]byte(start))
	return fmt.Sprintf("process:%d:%x", pid, digest[:10])
}

// proc stat's comm field may contain spaces and parentheses. Fields after its
// final ')' begin at field 3; the monotonic start time is field 22.
func procStart(raw string) string {
	end := strings.LastIndexByte(raw, ')')
	if end < 0 {
		return ""
	}
	fields := strings.Fields(raw[end+1:])
	if len(fields) < 20 {
		return ""
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return ""
	}
	return fields[19]
}

func displayText(raw string) string {
	var out strings.Builder
	for _, r := range raw {
		if unicode.IsControl(r) || r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			continue
		}
		out.WriteRune(r)
		if out.Len() >= 4096 {
			break
		}
	}
	return out.String()
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
