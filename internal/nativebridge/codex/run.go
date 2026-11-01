// Package codex keeps the native Codex terminal connected to its own app-server
// and submits authorized peer turns through Codex's queue protocol. It never
// reads or writes the terminal composer.
package codex

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentmux/internal/nativebridge"
	"github.com/coder/websocket"
)

const maxMessage = 32 << 20

// ContextEnv carries only owner-configured Agentmux role/integration context.
// Native Codex configuration is left intact, including developer_instructions.
const ContextEnv = "AGENTMUX_CODEX_CONTEXT"
const ContextFileEnv = "AGENTMUX_CODEX_CONTEXT_FILE"

func ownerContextFromEnvironment() (string, error) {
	const limit = 4 << 20
	if path := os.Getenv(ContextFileEnv); path != "" {
		file, err := os.Open(path)
		if err != nil {
			return "", errors.New("cannot read private owner context file")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
			return "", errors.New("owner context file must be a private regular file no larger than 4 MiB")
		}
		data, err := io.ReadAll(io.LimitReader(file, limit+1))
		if err != nil || len(data) > limit || !utf8.Valid(data) {
			return "", errors.New("owner context file is unreadable, oversized or invalid UTF-8")
		}
		return string(data), nil
	}
	value := os.Getenv(ContextEnv)
	if len(value) > limit || !utf8.ValidString(value) {
		return "", errors.New("owner context is oversized or invalid UTF-8")
	}
	return value, nil
}

// Run preserves the real Codex terminal and the calling process's environment,
// working directory and terminal descriptors. The caller applies its original
// sandbox before calling Run, so both native processes remain inside it.
func Run(ctx context.Context, argv []string, conn nativebridge.Connection) error {
	return run(ctx, argv, conn, os.Stdin, os.Stdout, os.Stderr)
}

func run(ctx context.Context, argv []string, conn nativebridge.Connection, in io.Reader, out, diagnostics io.Writer) error {
	if len(argv) == 0 || argv[0] == "" || conn == nil {
		return errors.New("Codex native delivery requires an executable and broker connection")
	}
	ownerContext, err := ownerContextFromEnvironment()
	if err != nil {
		return manualFallback(ctx, argv, in, out, diagnostics, err)
	}
	serverArgs, terminalArgs, err := arguments(argv[1:])
	if err != nil {
		return manualFallback(ctx, argv, in, out, diagnostics, err)
	}
	dir, err := os.MkdirTemp("", "amx-codex-")
	if err != nil {
		return manualFallback(ctx, argv, in, out, diagnostics, errors.New("cannot create a private native transport directory"))
	}
	defer os.RemoveAll(dir)
	if err := probeRemote(ctx, argv[0], dir); err != nil {
		return manualFallback(ctx, argv, in, out, diagnostics, err)
	}
	socketPath := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return manualFallback(ctx, argv, in, out, diagnostics, errors.New("private native Unix transport is unavailable"))
	}
	defer listener.Close()
	if err := os.Chmod(socketPath, 0600); err != nil {
		return manualFallback(ctx, argv, in, out, diagnostics, errors.New("cannot secure private native Unix transport"))
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := exec.CommandContext(ctx, argv[0], serverArgs...)
	server.Stderr = diagnostics
	configureServerProcess(server)
	upstreamIn, err := server.StdinPipe()
	if err != nil {
		return err
	}
	upstreamOut, err := server.StdoutPipe()
	if err != nil {
		return err
	}
	if err := server.Start(); err != nil {
		return fmt.Errorf("start Codex app-server: %w", err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Wait() }()
	defer func() { cancel(); <-serverDone }()
	p := newProxy(upstreamIn)
	p.ctx, p.ownerContext, p.diagnostics = ctx, ownerContext, diagnostics
	httpServer := &http.Server{Handler: p, ReadHeaderTimeout: 5 * time.Second}
	defer httpServer.Close()
	go httpServer.Serve(listener)
	readDone := make(chan error, 1)
	go func() { readDone <- p.readServer(ctx, upstreamOut) }()
	bridgeDone := make(chan struct{})
	go func() { defer close(bridgeDone); p.deliver(ctx, conn) }()
	terminalArgs = append([]string{"--remote", "unix://" + socketPath}, terminalArgs...)
	terminal := exec.CommandContext(ctx, argv[0], terminalArgs...)
	terminal.Stdin, terminal.Stdout, terminal.Stderr = in, out, diagnostics
	if err := terminal.Start(); err != nil {
		cancel()
		<-bridgeDone
		return fmt.Errorf("start Codex terminal: %w", err)
	}
	terminalDone := make(chan error, 1)
	go func() { terminalDone <- terminal.Wait() }()
	select {
	case err = <-terminalDone:
	case readErr := <-readDone:
		wasCancelled := ctx.Err() != nil
		cancel()
		<-terminalDone
		if !wasCancelled {
			err = fmt.Errorf("Codex app-server disconnected: %w", readErr)
		} else {
			err = ctx.Err()
		}
	case <-ctx.Done():
		<-terminalDone
		err = ctx.Err()
	}
	cancel()
	p.close()
	<-bridgeDone
	return err
}

func manualFallback(ctx context.Context, argv []string, in io.Reader, out, diagnostics io.Writer, reason error) error {
	fmt.Fprintf(diagnostics, "\n[agentmux] Automatic Codex delivery unavailable: %v. Native terminal continues with the manual Agentmux inbox.\n", reason)
	native := exec.CommandContext(ctx, argv[0], argv[1:]...)
	native.Stdin, native.Stdout, native.Stderr = in, out, diagnostics
	return native.Run()
}

var remoteFlag = regexp.MustCompile(`(?m)^[ \t]+--remote(?:[ \t=<]|$)`)

// Help is parsed before normal Codex configuration or model startup. Give even
// its local PATH-alias setup a disposable home, so capability discovery never
// writes into the owner's native configuration. The actual native launch keeps
// its complete original environment and arguments.
func probeRemote(ctx context.Context, binary, dir string) error {
	home := filepath.Join(dir, "probe-home")
	if err := os.Mkdir(home, 0700); err != nil {
		return errors.New("cannot verify native Codex remote support")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(probeCtx, binary, "--help")
	configureServerProcess(command)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "CODEX_HOME" && key != "HOME" && key != "XDG_CONFIG_HOME" && key != "XDG_CACHE_HOME" {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "CODEX_HOME="+home, "HOME="+home, "XDG_CONFIG_HOME="+home, "XDG_CACHE_HOME="+home)
	var output probeOutput
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return errors.New("native Codex remote support could not be verified with bounded --help")
	}
	if !remoteFlag.Match(output.data) || !strings.Contains(string(output.data), "unix://") {
		return errors.New("this Codex CLI does not advertise native --remote Unix-socket support")
	}
	return nil
}

type probeOutput struct {
	mu   sync.Mutex
	data []byte
}

func (w *probeOutput) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if remaining := (64 << 10) - len(w.data); remaining > 0 {
		if remaining > len(value) {
			remaining = len(value)
		}
		w.data = append(w.data, value[:remaining]...)
	}
	return len(value), nil
}

// The app-server loads the same normal user/project configuration. Explicit
// config/feature overrides must also reach it: the remote TUI does not own the
// server's startup configuration. Other native options stay on the real TUI.
func arguments(args []string) ([]string, []string, error) {
	server := []string{"app-server", "--listen", "stdio://"}
	terminal := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			terminal = append(terminal, args[i:]...)
			break
		}
		if a == "--no-daemon" {
			continue
		}
		if a == "--remote" || strings.HasPrefix(a, "--remote=") || a == "--remote-auth-token-env" || strings.HasPrefix(a, "--remote-auth-token-env=") {
			return nil, nil, errors.New("Codex native delivery owns its private server; remove configured --remote options")
		}
		if a == "-p" || a == "--profile" || strings.HasPrefix(a, "--profile=") || strings.HasPrefix(a, "-p") && len(a) > 2 {
			return nil, nil, errors.New("Codex native delivery cannot safely transfer a CLI profile to app-server; select equivalent normal project/user config or use manual delivery")
		}
		terminal = append(terminal, a)
		if a == "-c" || a == "--config" || a == "--enable" || a == "--disable" {
			if i+1 == len(args) {
				return nil, nil, fmt.Errorf("Codex option %s requires a value", a)
			}
			i++
			terminal = append(terminal, args[i])
			server = append(server, a, args[i])
		} else if strings.HasPrefix(a, "--config=") || strings.HasPrefix(a, "-c") && len(a) > 2 || strings.HasPrefix(a, "--enable=") || strings.HasPrefix(a, "--disable=") || a == "--strict-config" {
			server = append(server, a)
		}
	}
	return server, terminal, nil
}

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("Codex RPC %d: %s", e.Code, e.Message) }

type request struct {
	original          json.RawMessage
	bindsConversation bool
}
type notification struct {
	method string
	params json.RawMessage
}

type proxy struct {
	mu           sync.Mutex
	writeMu      sync.Mutex
	contextMu    sync.Mutex
	ctx          context.Context
	ownerContext string
	diagnostics  io.Writer
	ownedRoots   map[string]bool
	contexts     map[string]error
	questions    map[string]json.RawMessage
	writer       io.Writer
	ui           *websocket.Conn
	accepted     bool
	initialized  bool
	closed       bool
	fault        string
	current      string
	state        string
	prefix       string
	next         uint64
	requests     map[string]request
	calls        map[string]chan message
	events       chan notification
}

func newProxy(writer io.Writer) *proxy {
	var nonce [12]byte
	_, _ = rand.Read(nonce[:])
	return &proxy{writer: writer, ctx: context.Background(), diagnostics: io.Discard, ownedRoots: map[string]bool{}, contexts: map[string]error{}, questions: map[string]json.RawMessage{}, prefix: "amx_" + hex.EncodeToString(nonce[:]) + "_", requests: map[string]request{}, calls: map[string]chan message{}, events: make(chan notification, 512)}
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// This endpoint is a mode-0600 socket in a mode-0700 directory and accepts
	// only the one native TUI. It is never a web/browser control endpoint.
	p.mu.Lock()
	if p.accepted || p.closed || r.Header.Get("Origin") != "" {
		p.mu.Unlock()
		http.Error(w, "native terminal already connected or invalid origin", http.StatusForbidden)
		return
	}
	p.accepted = true
	p.mu.Unlock()
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		p.mu.Lock()
		p.accepted = false
		p.mu.Unlock()
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(maxMessage)
	p.mu.Lock()
	p.ui = ws
	p.mu.Unlock()
	for {
		kind, data, err := ws.Read(r.Context())
		if err != nil {
			return
		}
		if kind != websocket.MessageText {
			continue
		}
		if err := p.forwardClient(data); err != nil {
			p.disable(err.Error())
			return
		}
	}
}

func (p *proxy) write(m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.writer.Write(append(data, '\n'))
	return err
}

func (p *proxy) forwardClient(data []byte) error {
	var m message
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if m.Method == "turn/start" || m.Method == "thread/queue/add" || m.Method == "review/start" {
		var params struct {
			ThreadID string `json:"threadId"`
		}
		if json.Unmarshal(m.Params, &params) == nil {
			// This call runs on the frontend reader; the independent backend
			// reader must keep processing the append receipt. A failed append
			// pauses Agentmux delivery but never blocks the owner's native turn.
			_ = p.prepareContext(p.ctx, params.ThreadID)
		}
	}
	p.mu.Lock()
	if m.Method == "turn/steer" {
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"expectedTurnId"`
		}
		if json.Unmarshal(m.Params, &params) == nil {
			// Retain only attribution IDs. Human input must never enter broker
			// receipts or observability through the native transport bridge.
			marker, _ := json.Marshal(map[string]string{"threadId": params.ThreadID, "turnId": params.TurnID})
			p.humanIntervention(marker)
		}
	} else if m.Method == "" && len(m.ID) > 0 {
		if marker, ok := p.questions[string(m.ID)]; ok {
			delete(p.questions, string(m.ID))
			p.humanIntervention(marker)
		}
	}
	if m.Method == "initialized" {
		p.initialized = true
	}
	if m.Method != "" && len(m.ID) > 0 {
		p.next++
		key := fmt.Sprintf("%sui_%d", p.prefix, p.next)
		p.requests[key] = request{original: append(json.RawMessage(nil), m.ID...), bindsConversation: nativeConversationRequest(m)}
		m.ID, _ = json.Marshal(key)
	}
	p.mu.Unlock()
	return p.write(m)
}

func (p *proxy) readServer(ctx context.Context, reader io.Reader) error {
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 64<<10), maxMessage)
	for scan.Scan() {
		data, forward, err := p.serverMessage(scan.Bytes())
		if err != nil {
			return err
		}
		if !forward {
			continue
		}
		p.mu.Lock()
		ws := p.ui
		p.mu.Unlock()
		if ws == nil {
			return errors.New("Codex app-server wrote before native terminal connected")
		}
		writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = ws.Write(writeCtx, websocket.MessageText, data)
		cancel()
		if err != nil {
			return err
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	return io.EOF
}

func (p *proxy) serverMessage(data []byte) ([]byte, bool, error) {
	var m message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(m.ID) > 0 && m.Method == "" {
		var key string
		_ = json.Unmarshal(m.ID, &key)
		if call, ok := p.calls[key]; ok {
			delete(p.calls, key)
			call <- m
			return nil, false, nil
		}
		// A late adapter response after a timeout belongs to the adapter, not
		// the native UI. Its submission is already marked uncertain.
		if strings.HasPrefix(key, p.prefix+"bridge_") {
			return nil, false, nil
		}
		if req, ok := p.requests[key]; ok {
			delete(p.requests, key)
			m.ID = req.original
			if m.Error == nil && req.bindsConversation {
				var result struct {
					Thread struct {
						ID     string `json:"id"`
						Status struct {
							Type string `json:"type"`
						} `json:"status"`
					} `json:"thread"`
				}
				if json.Unmarshal(m.Result, &result) == nil && result.Thread.ID != "" {
					p.current = result.Thread.ID
					p.state = result.Thread.Status.Type
					p.ownedRoots[result.Thread.ID] = true
				}
			}
		}
		encoded, err := json.Marshal(m)
		return encoded, true, err
	}
	if m.Method != "" {
		var e struct {
			ThreadID string `json:"threadId"`
			Status   struct {
				Type        string   `json:"type"`
				ActiveFlags []string `json:"activeFlags"`
			} `json:"status"`
		}
		_ = json.Unmarshal(m.Params, &e)
		if m.Method == "item/tool/requestUserInput" && len(m.ID) > 0 {
			var params struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
			}
			if json.Unmarshal(m.Params, &params) == nil {
				marker, _ := json.Marshal(map[string]string{"threadId": params.ThreadID, "turnId": params.TurnID})
				p.questions[string(m.ID)] = marker
			}
		}
		if e.ThreadID == p.current {
			switch m.Method {
			case "thread/status/changed":
				p.state = e.Status.Type
				if len(e.Status.ActiveFlags) > 0 {
					p.state = e.Status.ActiveFlags[0]
				}
			case "turn/started":
				p.state = "active"
			case "turn/completed":
				p.state = "idle"
			case "thread/closed":
				p.state = "notLoaded"
			}
		}
		userMessage := false
		if m.Method == "item/started" {
			var item struct {
				Item struct {
					Type string `json:"type"`
				} `json:"item"`
			}
			_ = json.Unmarshal(m.Params, &item)
			userMessage = item.Item.Type == "userMessage"
		}
		if userMessage || m.Method == "turn/completed" || m.Method == "thread/closed" {
			select {
			case p.events <- notification{m.Method, append(json.RawMessage(nil), m.Params...)}:
			default:
				p.fault = "native lifecycle event buffer overflow; delivery paused"
			}
		}
	}
	return append([]byte(nil), data...), true, nil
}

// Called under p.mu. A marker carries no human text, only the affected native
// thread/turn. Missing a marker would risk forwarding private human context.
func (p *proxy) humanIntervention(marker json.RawMessage) {
	select {
	case p.events <- notification{"agentmux/humanIntervention", marker}:
	default:
		p.fault = "native human-intervention event buffer overflow; delivery paused"
	}
}

// Append a separate developer message instead of replacing Codex's layered or
// persisted developer instructions. Resume/fork ignore developerInstructions
// overrides in Codex 0.166.0, whereas this documented append works for all three
// native root operations. An attachment refreshes this owner-configured context
// once per selected root. It never promotes peer messages to developer authority.
func (p *proxy) prepareContext(ctx context.Context, thread string) error {
	p.contextMu.Lock()
	defer p.contextMu.Unlock()
	p.mu.Lock()
	owned := p.ownedRoots[thread]
	text := p.ownerContext
	p.mu.Unlock()
	if text == "" || !owned {
		return nil
	}
	if prior, tried := p.contexts[thread]; tried {
		return prior
	}
	// Reserve before sending: a timeout can mean that the native append
	// succeeded. Repeating it would silently duplicate persistent context.
	p.contexts[thread] = errors.New("owner context append is unconfirmed")
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_, err := p.call(callCtx, "thread/inject_items", map[string]any{
		"threadId": thread,
		"items":    []map[string]any{{"type": "message", "role": "developer", "content": []map[string]any{{"type": "input_text", "text": "[Agentmux owner-configured session context]\nThis is the owner's Agentmux role and integration configuration for this session attachment. Native Codex instructions and approvals remain in effect.\n\n" + text + "\n[End Agentmux owner-configured session context]"}}}},
	})
	cancel()
	p.contexts[thread] = err
	if err != nil {
		reason := "Codex owner context append is unsupported or unconfirmed; automatic Agentmux delivery is paused. Native Codex remains interactive; use the manual inbox or relaunch after updating Codex."
		p.disable(reason)
		fmt.Fprintf(p.diagnostics, "\n[agentmux] %s\n", reason)
	}
	return err
}

func nativeConversationRequest(m message) bool {
	if m.Method != "thread/start" && m.Method != "thread/resume" && m.Method != "thread/fork" {
		return false
	}
	var params struct {
		Ephemeral bool   `json:"ephemeral"`
		Source    string `json:"threadSource"`
	}
	if json.Unmarshal(m.Params, &params) != nil {
		return false
	}
	// The native TUI also creates internal ephemeral threads, including title
	// generation. Those requests are not a human switching conversations.
	return !params.Ephemeral && (params.Source == "" || params.Source == "user")
}

func (p *proxy) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("Codex connection closed")
	}
	p.next++
	key := fmt.Sprintf("%sbridge_%d", p.prefix, p.next)
	id, _ := json.Marshal(key)
	ch := make(chan message, 1)
	p.calls[key] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.calls, key); p.mu.Unlock() }()
	if err := p.write(message{ID: id, Method: method, Params: encoded}); err != nil {
		return nil, err
	}
	select {
	case result := <-ch:
		if result.Error != nil {
			return nil, result.Error
		}
		return result.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *proxy) disable(reason string) {
	p.mu.Lock()
	p.fault = truncateUTF8(reason, 512)
	p.mu.Unlock()
}
func (p *proxy) close() {
	p.mu.Lock()
	p.closed = true
	ws := p.ui
	p.mu.Unlock()
	if ws != nil {
		ws.CloseNow()
	}
}
