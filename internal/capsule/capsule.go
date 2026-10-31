package capsule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentmux/internal/harness"
	"agentmux/internal/sandbox"
)

type Options struct {
	StateDir, AgentID, Workspace, Executable string
	WorkspaceBytes, HistoryBytes, TempBytes  int64
	MaxEntries                               int
}

type Capsule struct {
	opts               Options
	dir                string
	ctx                context.Context
	listener           net.Listener
	mu                 sync.Mutex
	connection         net.Conn
	result             chan error
	volumes            []volume
	baseline           map[string]string
	historyBaseline    string
	maxEntries         int
	lock               *os.File
	state              capsuleState
	published, wrapped bool
}

func Validate(o Options) error {
	return validateOptions(o, false)
}
func validateOptions(o Options, allowMissing bool) error {
	if o.AgentID == "" || o.AgentID == "." || o.AgentID == ".." || strings.ContainsAny(o.AgentID, "/\\\x00") || len(o.AgentID) > 64 {
		return errors.New("invalid capsule agent ID")
	}
	for _, v := range []int64{o.WorkspaceBytes, o.HistoryBytes, o.TempBytes} {
		if v < 1<<20 || v > 1<<40 {
			return errors.New("capsule sizes must each be between 1 MiB and 1 TiB")
		}
	}
	if o.MaxEntries < 1 || o.MaxEntries > 100000 {
		return errors.New("capsule max_entries must be 1–100000")
	}
	state, err := filepath.Abs(o.StateDir)
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(state)
	if err != nil || canonical != state {
		return errors.New("capsule state requires a canonical directory")
	}
	info, err := os.Stat(state)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("capsule state must be private")
	}
	workspace, err := filepath.Abs(o.Workspace)
	if err != nil {
		return err
	}
	expected := filepath.Join(state, "workspaces", o.AgentID)
	canonical, err = filepath.EvalSymlinks(workspace)
	if allowMissing && os.IsNotExist(err) {
		parent, e := filepath.EvalSymlinks(filepath.Dir(workspace))
		if e == nil && parent == filepath.Dir(workspace) {
			canonical = workspace
			err = nil
		}
	}
	if err != nil || canonical != workspace || workspace != expected {
		return errors.New("disk caps require the agent's dedicated state-owned workspace; external/shared workdirs are unsupported")
	}
	return nil
}

func Prepare(ctx context.Context, o Options) (*Capsule, error) {
	if err := Validate(o); err != nil {
		return nil, err
	}
	o.StateDir, _ = filepath.Abs(o.StateDir)
	o.Workspace, _ = filepath.Abs(o.Workspace)
	if _, err := inspect(o.Workspace, o.WorkspaceBytes, o.MaxEntries); err != nil {
		return nil, err
	}
	root := filepath.Join(o.StateDir, "capsules")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	if canonical, err := filepath.EvalSymlinks(root); err != nil || canonical != root {
		return nil, errors.New("capsule storage parent must not contain symlinks")
	}
	dir := filepath.Join(root, o.AgentID)
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, errors.New("capsule already active or quarantined; owner recovery is required")
	}
	lock, err := takeLock(filepath.Join(dir, "controller.lock"))
	if err != nil {
		return nil, err
	}
	if err := writeDurable(filepath.Join(dir, "quarantine.json"), []byte(`{"state":"prepared","note":"incomplete capsules require explicit owner recovery"}`)); err != nil {
		lock.Close()
		return nil, err
	}
	return &Capsule{opts: o, dir: dir, ctx: ctx, result: make(chan error, 1), baseline: map[string]string{}, maxEntries: o.MaxEntries, lock: lock}, nil
}

// Wrap must be called once after the harness's generated endpoint/session
// preparation. It replaces all writable host bind mounts with capped tmpfs.
func (c *Capsule) Wrap(spec harness.Spec, opts sandbox.Options) (harness.Spec, error) {
	if c.wrapped {
		return harness.Spec{}, errors.New("capsule wrap already called")
	}
	c.wrapped = true
	if opts.Mode != "bubblewrap" || opts.Workspace != c.opts.Workspace || opts.StateDir != c.opts.StateDir {
		return harness.Spec{}, errors.New("capsule requires the matching strict bubblewrap boundary")
	}
	if len(spec.SessionDirs) > 1 {
		return harness.Spec{}, errors.New("capsule permits one native history store")
	}
	volumes := []volume{{Name: "workspace", Source: c.opts.Workspace, Target: "/workspace", Bytes: c.opts.WorkspaceBytes}}
	if len(spec.SessionDirs) == 1 {
		session := spec.SessionDirs[0]
		root := filepath.Join(c.opts.StateDir, "sessions", c.opts.AgentID)
		relative, err := filepath.Rel(root, session.Source)
		switch session.Target {
		case "/home/agent/.claude/projects", "/home/agent/.codex/sessions", "/home/agent/.pi/agent/sessions":
		default:
			return harness.Spec{}, errors.New("unsupported capsule native history target")
		}
		if err != nil || relative == "." || strings.Contains(relative, string(filepath.Separator)) || !strings.HasPrefix(relative, "g-") {
			return harness.Spec{}, errors.New("capsule history must use the agent's state-owned generation directory")
		}
		for _, p := range []string{root, session.Source} {
			canonical, e := filepath.EvalSymlinks(p)
			info, statErr := os.Stat(p)
			if e != nil || canonical != p || statErr != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
				return harness.Spec{}, errors.New("capsule history requires canonical private directories")
			}
		}
		all, err := inspect(root, c.opts.HistoryBytes, c.opts.MaxEntries)
		if err != nil {
			return harness.Spec{}, err
		}
		current, err := inspect(session.Source, c.opts.HistoryBytes, c.opts.MaxEntries)
		if err != nil {
			return harness.Spec{}, err
		}
		remaining := c.opts.HistoryBytes - (all.Bytes - current.Bytes)
		if remaining < 0 {
			return harness.Spec{}, errors.New("capsule history quota exhausted")
		}
		c.historyBaseline = all.Digest
		other, err := inspectExcept(root, c.opts.HistoryBytes, c.opts.MaxEntries, filepath.Base(session.Source))
		if err != nil {
			return harness.Spec{}, err
		}
		c.state.OtherHistorySHA256 = other.Digest
		c.maxEntries -= all.Entries - current.Entries
		if c.maxEntries < 1 {
			return harness.Spec{}, errors.New("capsule history entry quota exhausted")
		}
		volumes = append(volumes, volume{Name: "history/0", Source: session.Source, Target: session.Target, Bytes: remaining})
	}
	var entries int
	for _, v := range volumes {
		s, err := inspect(v.Source, v.Bytes, c.maxEntries)
		if err != nil {
			return harness.Spec{}, err
		}
		entries += s.Entries
		c.baseline[v.Source] = s.Digest
	}
	if entries > c.maxEntries {
		return harness.Spec{}, errors.New("capsule combined entry quota exceeded")
	}
	c.volumes = volumes
	c.state.Volumes = volumes
	c.state.Before = c.baseline
	c.state.MaxEntries = c.maxEntries
	stateData, _ := json.Marshal(c.state)
	if err := writeDurable(filepath.Join(c.dir, "state.json"), stateData); err != nil {
		return harness.Spec{}, err
	}
	workdir := spec.Workdir
	if workdir == "" {
		workdir = c.opts.Workspace
	}
	relative, err := filepath.Rel(c.opts.Workspace, workdir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
		return harness.Spec{}, errors.New("capsule workdir must stay in workspace")
	}
	m := manifest{MaxEntries: c.maxEntries, Socket: "/run/agentmux/checkpoint.sock", Workdir: filepath.Join("/workspace", relative)}
	for i, v := range volumes {
		seed := "/run/agentmux/input/" + strconv.Itoa(i)
		m.Inputs = append(m.Inputs, volume{Name: v.Name, Source: seed, Target: v.Target, Bytes: v.Bytes})
		m.Outputs = append(m.Outputs, volume{Name: v.Name, Source: v.Target, Bytes: v.Bytes})
	}
	data, _ := json.Marshal(m)
	manifestPath := filepath.Join(c.dir, "manifest.json")
	if err = os.WriteFile(manifestPath, data, 0600); err != nil {
		return harness.Spec{}, err
	}
	stage := filepath.Join(c.dir, "output")
	if err = os.Mkdir(stage, 0700); err != nil {
		return harness.Spec{}, err
	}
	socket := filepath.Join(c.dir, "checkpoint.sock")
	c.listener, err = net.Listen("unix", socket)
	if err != nil {
		return harness.Spec{}, err
	}
	if err = os.Chmod(socket, 0600); err != nil {
		return harness.Spec{}, err
	}
	go c.collect(stage)
	inputs := make([]sandbox.CapsuleInput, 0, len(volumes))
	for i, v := range volumes {
		inputs = append(inputs, sandbox.CapsuleInput{Source: v.Source, Target: "/run/agentmux/input/" + strconv.Itoa(i)})
	}
	opts.Capsule = &sandbox.CapsuleOptions{Manifest: manifestPath, Socket: socket, Executable: c.opts.Executable, Inputs: inputs, WorkspaceBytes: c.opts.WorkspaceBytes, HomeBytes: c.opts.HistoryBytes, TempBytes: c.opts.TempBytes}
	// The history is copied into the capped HOME by the trusted shim, never
	// exposed through the ordinary writable conversation bind mount.
	spec.SessionDirs = nil
	return sandbox.Wrap(spec, opts)
}

func (c *Capsule) collect(stage string) {
	conn, err := c.listener.Accept()
	if err != nil {
		c.result <- errors.New("capsule checkpoint not received")
		return
	}
	c.mu.Lock()
	c.connection = conn
	c.mu.Unlock()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	err = receiveArchive(conn, stage, c.volumes, c.maxEntries)
	if err == nil {
		end := make([]byte, len(checkpointEnd))
		_, err = io.ReadFull(conn, end)
		if err == nil && string(end) != checkpointEnd {
			err = errors.New("capsule checkpoint terminator invalid")
		}
	}
	if err == nil {
		extra := make([]byte, 1)
		n, e := conn.Read(extra)
		if n != 0 || e != io.EOF {
			err = errors.New("capsule checkpoint trailing data rejected")
		}
	}
	if err == nil {
		receipt := map[string]string{}
		for _, v := range c.volumes {
			s, e := inspect(filepath.Join(stage, filepath.FromSlash(v.Name)), v.Bytes, c.maxEntries)
			if e != nil {
				err = e
				break
			}
			receipt[v.Name] = s.Digest
		}
		if err == nil {
			data, _ := json.Marshal(receipt)
			err = writeDurable(filepath.Join(c.dir, "received.json"), data)
		}
	}
	if err == nil {
		_, err = io.WriteString(conn, "OK\n")
	} else {
		_, _ = io.WriteString(conn, "NO\n")
	}
	c.result <- err
}

// Finish runs after native exit, before transcript verification. Incomplete or
// over-quota exports never replace the previous snapshot. The fixed capsule
// directory remains quarantined, blocking repeated disk consumption.
func (c *Capsule) Finish() error {
	if c.published {
		return nil
	}
	if !c.wrapped || c.listener == nil {
		return errors.New("capsule process did not start")
	}
	select {
	case err := <-c.result:
		if err != nil {
			return err
		}
	case <-c.ctx.Done():
		return c.ctx.Err()
	case <-time.After(3 * time.Second):
		return errors.New("capsule process exited without a complete checkpoint")
	}
	for _, v := range c.volumes {
		s, err := inspect(v.Source, v.Bytes, c.opts.MaxEntries)
		if err != nil || s.Digest != c.baseline[v.Source] {
			return errors.New("capsule snapshot conflict; host workspace/history changed")
		}
	}
	if c.historyBaseline != "" {
		root := filepath.Join(c.opts.StateDir, "sessions", c.opts.AgentID)
		s, err := inspect(root, c.opts.HistoryBytes, c.opts.MaxEntries)
		if err != nil || s.Digest != c.historyBaseline {
			return errors.New("capsule history generation changed during turn")
		}
	}
	// A two-volume commit uses a durable journal. An interrupted publish is
	// quarantined instead of automatically replayed; neither old snapshot is
	// deleted until both replacements have succeeded.
	type journalVolume struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Before string `json:"before_sha256"`
		After  string `json:"after_sha256"`
	}
	journal := struct {
		State   string          `json:"state"`
		Volumes []journalVolume `json:"volumes"`
	}{State: "publishing"}
	for _, v := range c.volumes {
		after, err := inspect(filepath.Join(c.dir, "output", filepath.FromSlash(v.Name)), v.Bytes, c.maxEntries)
		if err != nil {
			return err
		}
		journal.Volumes = append(journal.Volumes, journalVolume{Name: v.Name, Source: v.Source, Before: c.baseline[v.Source], After: after.Digest})
	}
	journalData, _ := json.Marshal(journal)
	if err := writeDurable(filepath.Join(c.dir, "publish.json"), journalData); err != nil {
		return err
	}
	completed := 0
	rollback := func() {
		for i := completed - 1; i >= 0; i-- {
			v := c.volumes[i]
			replacement := filepath.Join(c.dir, "output", filepath.FromSlash(v.Name))
			_ = os.Rename(v.Source, replacement)
			_ = os.Rename(filepath.Join(c.dir, "previous-"+strconv.Itoa(i)), v.Source)
		}
	}
	for i, v := range c.volumes {
		backup := filepath.Join(c.dir, "previous-"+strconv.Itoa(i))
		if err := os.Rename(v.Source, backup); err != nil {
			rollback()
			return err
		}
		replacement := filepath.Join(c.dir, "output", filepath.FromSlash(v.Name))
		if err := os.Rename(replacement, v.Source); err != nil {
			_ = os.Rename(backup, v.Source)
			rollback()
			return err
		}
		completed++
	}
	for _, v := range c.volumes {
		if err := syncDirectory(filepath.Dir(v.Source)); err != nil {
			return err
		}
	}
	if err := syncDirectory(c.dir); err != nil {
		return err
	}
	c.published = true
	return nil
}

// Close closes the socket and removes successful temporary snapshots. A
// failed/uncertain turn keeps at most this one bounded quarantine per agent.
func (c *Capsule) Close() error {
	defer func() {
		if c.lock != nil {
			_ = c.lock.Close()
			c.lock = nil
		}
	}()
	if c.listener != nil {
		_ = c.listener.Close()
	}
	c.mu.Lock()
	if c.connection != nil {
		_ = c.connection.Close()
	}
	c.mu.Unlock()
	if c.published || !c.wrapped {
		return os.RemoveAll(c.dir)
	}
	return nil
}

func (c *Capsule) String() string { return fmt.Sprintf("capsule(%s)", c.opts.AgentID) }

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func writeDurable(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return syncDirectory(filepath.Dir(path))
}
