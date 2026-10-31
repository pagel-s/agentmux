package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
)

//go:embed workweb/index.html workweb/app.css workweb/app.js workweb/graph.js
var workWebAssets embed.FS

// This is an owner console. It binds numeric loopback only, requires a random
// bearer capability for every API call, validates Host/Origin, and serves no
// cross-origin API. Native output is data, never HTML or terminal input.
func workWebHandler(state string, client *managed.Client, token, host string) http.Handler {
	allowed := map[string]bool{"activity": true, "agent.list": true, "agent.label": true, "boundaries.get": true, "control.get": true, "control.update": true, "task.get": true, "task.list": true, "task.cancel": true, "agent.stop": true, "autonomy.status": true, "autonomy.control": true, "memory.list": true, "memory.get": true, "memory.put": true, "board.post": true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		if r.Host != host {
			http.Error(w, "unexpected host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+host {
			http.Error(w, "cross-origin access denied", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				http.Error(w, "owner capability required", http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "cross-site access denied", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			reply := func(value any) { _ = json.NewEncoder(w).Encode(value) }
			failure := func(err error) {
				w.WriteHeader(http.StatusBadRequest)
				reply(map[string]any{"ok": false, "error": err.Error()})
			}
			if r.URL.Path == "/api/snapshot" && r.Method == http.MethodGet {
				d := fetchWorkSnapshot(state, client, r.URL.Query().Get("agent"))
				if d.err != nil {
					failure(d.err)
					return
				}
				exe, exeErr := os.Executable()
				if exeErr != nil {
					failure(exeErr)
					return
				}
				attach := []string{exe, "attach"}
				if d.selected != "" {
					attach = append(attach, d.selected)
				}
				attach = append(attach, "--state", state)
				attachCommand := managedlaunch.ShellCommand(attach)
				for _, a := range d.agents {
					if a.ID == d.selected && a.External {
						attachCommand = ""
						for _, s := range d.observed {
							if s.ID == a.ExternalRef {
								attachCommand = s.AttachCommand
							}
						}
					}
				}
				reply(map[string]any{"ok": true, "groups": d.controls.Groups, "observed": d.observed, "bridge_setup": func() string {
					for _, a := range d.agents {
						if a.ID == d.selected && a.External {
							return bridgeInstructions(state, a.ID)
						}
					}
					return ""
				}(), "agents": d.agents, "boundaries": d.boundaries, "activity": d.activity, "runtime": d.runtime, "preview": d.preview, "selected": d.selected, "attach_command": attachCommand})
				return
			}
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", http.StatusMethodNotAllowed)
				return
			}
			if r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, managedCLIInputLimit))
			if err != nil {
				failure(errors.New("request body exceeds limit"))
				return
			}
			var in struct {
				Operation  string          `json:"operation"`
				Args       json.RawMessage `json:"args"`
				Target     string          `json:"target"`
				Text       string          `json:"text"`
				ReplyTo    string          `json:"reply_to"`
				Name       string          `json:"name"`
				Title      string          `json:"title"`
				Harness    string          `json:"harness"`
				Project    string          `json:"project"`
				Role       string          `json:"role"`
				Sandbox    string          `json:"sandbox"`
				Model      string          `json:"model"`
				ObservedID string          `json:"observed_id"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(body)))
			decoder.DisallowUnknownFields()
			if err = decoder.Decode(&in); err != nil || decoder.Decode(new(any)) != io.EOF {
				failure(errors.New("expected one valid request object"))
				return
			}
			switch r.URL.Path {
			case "/api/connect":
				setup, assignedID, err := connectObservedSessionWithID(r.Context(), state, in.ObservedID, in.Name, in.Title)
				if err != nil {
					failure(err)
					return
				}
				reply(map[string]any{"ok": true, "setup": setup, "id": assignedID})
			case "/api/add":
				args := []string{}
				if in.Name != "" {
					args = append(args, in.Name)
				}
				args = append(args, "--state", state, "--harness", in.Harness, "--project", in.Project, "--role", in.Role, "--title", in.Title)
				if in.Sandbox != "" {
					args = append(args, "--sandbox", in.Sandbox)
				}
				if in.Model != "" {
					args = append(args, "--model", in.Model)
				}
				var output, diagnostics bytes.Buffer
				code, id := runWorkAddWithID(args, &output, &diagnostics)
				if code != 0 {
					failure(errors.New(diagnostics.String()))
					return
				}
				reply(map[string]any{"ok": true, "id": id, "message": output.String()})
			case "/api/call":
				if !allowed[in.Operation] {
					http.Error(w, "operation is not exposed by the owner console", http.StatusForbidden)
					return
				}
				args := in.Args
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				raw, err := workCall(r.Context(), client, in.Operation, args)
				if err != nil {
					failure(err)
					return
				}
				reply(map[string]any{"ok": true, "result": raw})
			case "/api/send":
				target, text := in.Target, in.Text
				if in.ReplyTo != "" {
					target, err = workReplyTarget(r.Context(), client, in.ReplyTo)
					if err != nil {
						failure(err)
						return
					}
					text = "Follow-up to " + in.ReplyTo + ":\n" + text
				}
				id, kind, err := workSend(r.Context(), client, state, target, text)
				if err != nil {
					failure(err)
					return
				}
				reply(map[string]any{"ok": true, "id": id, "kind": kind, "target": target})
			case "/api/focus":
				exe, err := os.Executable()
				if err != nil {
					failure(err)
					return
				}
				launcher, err := managedlaunch.New(managedlaunch.Options{StateDir: state, Executable: exe})
				if err != nil {
					failure(err)
					return
				}
				runtime, err := launcher.Describe(r.Context())
				if err != nil {
					failure(err)
					return
				}
				if err = launcher.Focus(r.Context(), runtime, in.Target); err != nil {
					failure(err)
					return
				}
				reply(map[string]bool{"ok": true})
			default:
				http.NotFound(w, r)
			}
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		path, contentType := "", ""
		switch r.URL.Path {
		case "/":
			path, contentType = "index.html", "text/html; charset=utf-8"
		case "/app.css":
			path, contentType = "app.css", "text/css; charset=utf-8"
		case "/app.js":
			path, contentType = "app.js", "application/javascript; charset=utf-8"
		case "/graph.js":
			path, contentType = "graph.js", "application/javascript; charset=utf-8"
		default:
			http.NotFound(w, r)
			return
		}
		data, err := workWebAssets.ReadFile("workweb/" + path)
		if err != nil {
			http.Error(w, "asset unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(data)
	})
}

func runWorkWeb(args []string, out, diagnostics io.Writer) int {
	port, openBrowser := 0, false
	clean := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--open" {
			openBrowser = true
			continue
		}
		if args[i] == "--port" || strings.HasPrefix(args[i], "--port=") {
			_, value, inline := strings.Cut(args[i], "=")
			if !inline {
				i++
				if i >= len(args) {
					fmt.Fprintln(diagnostics, "error: --port requires a number")
					return exitValidation
				}
				value = args[i]
			}
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 65535 {
				fmt.Fprintln(diagnostics, "error: invalid --port")
				return exitValidation
			}
			port = n
			continue
		}
		if args[i] == "--help" || args[i] == "-h" {
			fmt.Fprintln(out, "Usage: agentmux web [--session work | --state DIR] [--port 0] [--open]\nOwner-only local web console. Closing it leaves agents running.")
			return exitSuccess
		}
		clean = append(clean, args[i])
	}
	o, err := parseWorkOptions(clean)
	if err != nil || len(o.args) > 0 {
		fmt.Fprintln(diagnostics, "error: web accepts --session, --state, --port and --open")
		return exitValidation
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(o.state, ""))
	if err != nil || credential.AgentID != "$owner" {
		fmt.Fprintln(diagnostics, "error: owner credential unavailable; start a work session first")
		return exitRuntime
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Fprintln(diagnostics, "error: local web listener unavailable")
		return exitRuntime
	}
	host := listener.Addr().String()
	token := rand.Text()
	url := "http://" + host + "/#" + token
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Handler: workWebHandler(o.state, managed.NewClient(credential), token, host), BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Fprintf(out, "Agentmux local owner console:\n%s\nKeep this link private. Ctrl-C stops the web console; the team keeps running.\n", url)
	if openBrowser {
		go func() {
			opener := "xdg-open"
			if runtime.GOOS == "darwin" {
				opener = "open"
			}
			_ = exec.Command(opener, url).Run()
		}()
	}
	if err = server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(diagnostics, "error: local web console stopped")
		return exitRuntime
	}
	return exitSuccess
}
