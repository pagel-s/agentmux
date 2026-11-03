package egress

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxTunnelBytes int64 = 64 << 20
const maxRequestLifetime = 10 * time.Minute

// Server owns its listener and all in-flight connections. Close revokes every
// tunnel; a caller must retain it for exactly the worker lifetime.
type Server struct {
	server      *http.Server
	listener    net.Listener
	rules       map[string]rule
	mu          sync.Mutex
	connections map[net.Conn]bool
	closed      bool
	slots       chan struct{}
	done        chan struct{}
	strict      *strictProxy
	// Immutable after construction. The internal constructor permits shorter
	// fixture deadlines, never a lifetime above the production hard limit.
	requestTimeout time.Duration
}

// Start is a compatibility alias for StartLegacy. New filesystem-isolated
// workflows must use StartStrict instead.
// Deprecated: select StartLegacy or StartStrict explicitly.
func Start(ctx context.Context, socketPath string, endpoints []Endpoint) (*Server, error) {
	return StartLegacy(ctx, socketPath, endpoints)
}

// StartLegacy preserves the managed runtime's opaque HTTPS tunnel: it verifies
// CONNECT/SNI but cannot inspect the encrypted HTTP authority. This is not a
// strict HTTPS origin boundary. The socket is private and existing files are
// never replaced.
func StartLegacy(ctx context.Context, socketPath string, endpoints []Endpoint) (*Server, error) {
	return start(ctx, socketPath, endpoints, nil)
}

func start(ctx context.Context, socketPath string, endpoints []Endpoint, inspection *strictProxy) (*Server, error) {
	return startWithRequestTimeout(ctx, socketPath, endpoints, inspection, maxRequestLifetime)
}

func startWithRequestTimeout(ctx context.Context, socketPath string, endpoints []Endpoint, inspection *strictProxy, lifetime time.Duration) (*Server, error) {
	if lifetime <= 0 || lifetime > maxRequestLifetime {
		return nil, errors.New("egress request lifetime must be positive and at most ten minutes")
	}
	rules, err := resolve(ctx, endpoints)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(socketPath) {
		return nil, errors.New("egress socket path must be absolute")
	}
	parent, err := os.Lstat(filepath.Dir(socketPath))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 || parent.Mode().Perm()&0077 != 0 {
		return nil, errors.New("egress socket requires a private directory")
	}
	if _, err = os.Lstat(socketPath); !os.IsNotExist(err) {
		return nil, errors.New("egress socket already exists or cannot be inspected")
	}
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(socketPath, 0600); err != nil {
		l.Close()
		return nil, err
	}
	s := &Server{listener: l, rules: rules, connections: map[net.Conn]bool{}, slots: make(chan struct{}, 32), done: make(chan struct{}), strict: inspection, requestTimeout: lifetime}
	s.server = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 10 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 64 << 10, ErrorLog: log.New(io.Discard, "", 0), ConnState: func(c net.Conn, state http.ConnState) {
		if state == http.StateNew {
			s.track(c)
		} else if state == http.StateClosed {
			s.untrack(c)
		}
	}}
	go func() { _ = s.server.Serve(l) }()
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-s.done:
		}
	}()
	return s, nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.done)
	for c := range s.connections {
		_ = c.Close()
	}
	s.mu.Unlock()
	return s.server.Close()
}

func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.connections) >= 128 {
		c.Close()
		return false
	}
	s.connections[c] = true
	return true
}
func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.connections, c)
	s.mu.Unlock()
	_ = c.Close()
}

func (s *Server) dial(ctx context.Context, r rule) (net.Conn, error) {
	var last error
	for _, ip := range r.ips {
		c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), r.port))
		if err == nil {
			if !s.track(c) {
				return nil, errors.New("proxy closed")
			}
			return c, nil
		}
		last = err
	}
	return nil, last
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		http.Error(w, "egress connection limit", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodConnect {
		s.connect(w, r)
		return
	}
	r, finish := s.requestWithDeadline(w, r)
	defer finish()
	if !r.URL.IsAbs() || r.URL.Scheme != "http" || r.URL.User != nil || r.URL.Fragment != "" || r.Header.Get("Upgrade") != "" {
		http.Error(w, "egress request denied", http.StatusForbidden)
		return
	}
	u, err := parseOrigin(r.URL.Scheme + "://" + r.URL.Host)
	if err != nil {
		http.Error(w, "egress origin denied", http.StatusForbidden)
		return
	}
	h, err := parseOrigin("http://" + r.Host)
	if err != nil || h.authority != u.authority {
		http.Error(w, "egress Host mismatch", http.StatusForbidden)
		return
	}
	allowed, ok := s.rules["http://"+u.authority]
	if !ok {
		http.Error(w, "egress origin denied", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.Header = r.Header.Clone()
	stripHopHeaders(out.Header)
	out.Host = allowed.authority
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 30 * time.Second, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		c, e := s.dial(ctx, allowed)
		if e != nil {
			return nil, e
		}
		return &trackedConn{Conn: c, server: s}, nil
	}}
	defer transport.CloseIdleConnections()
	resp, err := transport.RoundTrip(out)
	if err != nil {
		http.Error(w, "egress upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	stripHopHeaders(resp.Header)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(flushingProxyWriter{ResponseWriter: w}, io.LimitReader(resp.Body, maxTunnelBytes)); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// A server's read/write timeouts do not interrupt a handler blocked reading an
// upstream response body. Keep one context deadline across upload, dialing,
// response headers and streaming. Expiring downstream I/O also unblocks a slow
// client upload or a write when the client stops consuming its response, while
// leaving the server's existing (possibly shorter) I/O deadlines unchanged.
func (s *Server) requestWithDeadline(w http.ResponseWriter, r *http.Request) (*http.Request, func()) {
	ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now())
		_ = controller.SetWriteDeadline(time.Now())
	})
	return r.WithContext(ctx), func() {
		// Do not expire successful responses before net/http flushes its final
		// bytes after the handler returns. Join any running callback before the
		// response writer can be recycled for the next request or HTTP/2 stream.
		if !stop() {
			<-done
		}
		cancel()
	}
}

type trackedConn struct {
	net.Conn
	server *Server
}

func (c *trackedConn) Close() error { c.server.untrack(c.Conn); return nil }

func stripHopHeaders(h http.Header) {
	for _, v := range h.Values("Connection") {
		for _, k := range strings.Split(v, ",") {
			h.Del(strings.TrimSpace(k))
		}
	}
	for _, k := range []string{"Connection", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		h.Del(k)
	}
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	u, err := parseOrigin("https://" + r.Host)
	if err != nil || r.URL.Host != r.Host || r.URL.Path != "" || r.URL.RawQuery != "" {
		http.Error(w, "egress tunnel denied", http.StatusForbidden)
		return
	}
	allowed, ok := s.rules["https://"+u.authority]
	// CONNECT does not carry a scheme. Some clients also tunnel plaintext
	// HTTP (notably undici's proxyTunnel mode). Only strict servers can admit
	// that form: they parse every inner request instead of forwarding bytes.
	// Prefer an HTTPS grant when both schemes share an authority, so an HTTP
	// grant can never downgrade an existing HTTPS tunnel.
	if !ok && s.strict != nil {
		if httpOrigin, err := parseOrigin("http://" + r.Host); err == nil {
			allowed, ok = s.rules["http://"+httpOrigin.authority]
		}
	}
	if !ok {
		http.Error(w, "egress origin denied", http.StatusForbidden)
		return
	}
	if s.strict != nil {
		s.strictConnect(w, r, allowed)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "egress tunnel unavailable", http.StatusInternalServerError)
		return
	}
	client, buf, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if !s.track(client) {
		return
	}
	defer s.untrack(client)
	if _, err = buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buf.Flush(); err != nil {
		return
	}
	_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
	prefix, err := readClientHello(buf.Reader, allowed.host)
	if err != nil {
		return
	}
	_ = client.SetReadDeadline(time.Time{})
	upstream, err := s.dial(r.Context(), allowed)
	if err != nil {
		return
	}
	defer s.untrack(upstream)
	if _, err = upstream.Write(prefix); err != nil {
		return
	}
	_ = client.SetDeadline(time.Now().Add(10 * time.Minute))
	_ = upstream.SetDeadline(time.Now().Add(10 * time.Minute))
	done := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(upstream, io.LimitReader(buf.Reader, maxTunnelBytes))
		_ = upstream.Close()
		done <- struct{}{}
	}()
	_, _ = io.Copy(client, io.LimitReader(upstream, maxTunnelBytes))
	_ = client.Close()
	_ = upstream.Close()
	<-done
}
