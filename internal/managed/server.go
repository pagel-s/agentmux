package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxWireBytes = 8 << 20

type wireResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Fault          `json:"error,omitempty"`
}

// Serve exposes only the authenticated managed API on a private Unix socket.
// The exclusive controller lock is held through recovery and shutdown. The
// operator owns the Store and closes it after Serve returns.
func Serve(ctx context.Context, s *Store) error {
	unlock, err := acquireLock(filepath.Join(s.dir, "broker.lock"))
	if err != nil {
		return fmt.Errorf("broker already running or lock unavailable: %w", err)
	}
	defer unlock()
	path := SocketPath(s.dir)
	if len(path) > 100 {
		return errors.New("state directory path is too long for a Unix socket (maximum socket path 100 bytes)")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing to replace a non-socket broker path")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = s.RecoverInflight(ctx); err != nil {
		return fmt.Errorf("recover managed state: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen on managed socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(path)
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	if err = s.startRuntime(ctx); err != nil {
		return fmt.Errorf("start runtime budget: %w", err)
	}
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = server.Close()
		case <-done:
		}
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) || ctx.Err() != nil {
		return nil
	}
	return err
}

// Handler is exported for in-process transport testing. Production callers use
// Serve so filesystem permissions, single-controller fencing and recovery apply.
func (s *Store) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		respond := func(status int, value wireResponse) { w.WriteHeader(status); _ = json.NewEncoder(w).Encode(value) }
		if r.Method != "POST" || r.URL.Path != "/v1/call" || r.URL.RawQuery != "" {
			respond(http.StatusNotFound, wireResponse{Error: fault("not_found", "unknown endpoint")})
			return
		}
		// No browser-origin requests or cookie-based authentication. Unix socket
		// permissions are defense in depth; each request still needs its token.
		if r.Header.Get("Origin") != "" {
			respond(http.StatusForbidden, wireResponse{Error: forbidden()})
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			respond(http.StatusUnauthorized, wireResponse{Error: fault("unauthorized", "invalid credential")})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWireBytes))
		if err != nil {
			respond(http.StatusRequestEntityTooLarge, wireResponse{Error: fault("invalid", "request body exceeds limit")})
			return
		}
		var request Request
		if err = strictJSON(body, &request); err != nil {
			respond(http.StatusBadRequest, wireResponse{Error: invalid(err)})
			return
		}
		result, problem := s.Call(r.Context(), strings.TrimPrefix(auth, "Bearer "), request)
		if len(result) > maxWireBytes {
			result = nil
			problem = fault("limit", "response exceeds transport limit; request a smaller page")
		}
		status := http.StatusOK
		if problem != nil {
			switch problem.Code {
			case "unauthorized":
				status = http.StatusUnauthorized
			case "forbidden":
				status = http.StatusForbidden
			case "conflict":
				status = http.StatusConflict
			case "internal":
				status = http.StatusInternalServerError
			default:
				status = http.StatusBadRequest
			}
		}
		respond(status, wireResponse{Result: result, Error: problem})
	})
}

type Client struct {
	credential Credential
	http       *http.Client
}

func NewClient(c Credential) *Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", c.Socket)
	}, DisableKeepAlives: true, ResponseHeaderTimeout: 30 * time.Second}
	return &Client{credential: c, http: &http.Client{Transport: transport, Timeout: 45 * time.Second}}
}

// Call never retries automatically. An interrupted write may have committed;
// retry it explicitly with the same request ID to retrieve its durable result.
func (c *Client) Call(ctx context.Context, r Request) (json.RawMessage, *Fault) {
	if isWrite(r.Operation) && r.RequestID == "" {
		return nil, fault("invalid", "mutating operations require an explicit request_id for safe retries")
	}
	if len(r.Args) == 0 {
		r.Args = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(r)
	if err != nil {
		return nil, invalid(err)
	}
	if len(body) > maxWireBytes {
		return nil, fault("invalid", "request body exceeds limit")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://agentmux/v1/call", strings.NewReader(string(body)))
	if err != nil {
		return nil, invalid(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.credential.Token)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, fault("transport", "broker response unavailable; write outcome may be uncertain; retry only with the same request_id")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxWireBytes+1))
	if err != nil || len(data) > maxWireBytes {
		return nil, fault("transport", "invalid or oversized broker response")
	}
	var result wireResponse
	if err = strictJSON(data, &result); err != nil {
		return nil, fault("transport", "invalid broker response")
	}
	if response.StatusCode != http.StatusOK && result.Error == nil {
		return nil, fault("transport", "unexpected broker response status")
	}
	return result.Result, result.Error
}
