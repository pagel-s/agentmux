package egress

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// StrictOptions can explicitly add an owner's upstream trust roots. Nil uses
// normal host system trust. Upstream hostname/certificate verification remains
// mandatory; this API never provides an insecure verification switch.
type StrictOptions struct{ UpstreamRoots *x509.CertPool }

type strictProxy struct {
	ca            *x509.Certificate
	key           *ecdsa.PrivateKey
	public        []byte
	upstreamRoots *x509.CertPool
	requests      chan struct{}
}

// StartStrict inspects every request inside CONNECT, including plaintext HTTP
// for explicitly allowed HTTP origins. HTTPS terminates TLS with an ephemeral
// per-server CA; only its public certificate is exposed. Every upstream uses
// pinned dialing and HTTPS retains normal hostname/certificate validation.
func StartStrict(ctx context.Context, socket string, endpoints []Endpoint, opts StrictOptions) (*Server, error) {
	inspection, err := newStrictProxy(opts)
	if err != nil {
		return nil, err
	}
	return start(ctx, socket, endpoints, inspection)
}

func (s *Server) PublicCertificate() []byte {
	if s.strict == nil {
		return nil
	}
	return append([]byte(nil), s.strict.public...)
}

func certificateSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func newStrictProxy(opts StrictOptions) (*strictProxy, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := certificateSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Agentmux ephemeral workflow egress"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	var roots *x509.CertPool
	if opts.UpstreamRoots != nil {
		roots = opts.UpstreamRoots.Clone()
	}
	return &strictProxy{ca: cert, key: key, public: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), upstreamRoots: roots, requests: make(chan struct{}, 32)}, nil
}

func (p *strictProxy) leaf(host string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := certificateSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: host}, NotBefore: p.ca.NotBefore, NotAfter: p.ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, p.ca, &key.PublicKey, p.key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der, p.ca.Raw}, PrivateKey: key}, nil
}

func (s *Server) strictConnect(w http.ResponseWriter, outer *http.Request, allowed rule) {
	var config *tls.Config
	if allowed.scheme == "https" {
		cert, err := s.strict.leaf(allowed.host)
		if err != nil {
			http.Error(w, "egress TLS unavailable", 503)
			return
		}
		config = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			if strings.ToLower(hello.ServerName) != allowed.host && !(hello.ServerName == "" && net.ParseIP(allowed.host) != nil) {
				return nil, errors.New("egress TLS name mismatch")
			}
			return nil, nil
		}}
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "egress tunnel unavailable", 500)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if !s.track(client) {
		return
	}
	defer s.untrack(client)
	ctx, cancel := context.WithTimeout(outer.Context(), s.requestTimeout)
	defer cancel()
	// Consume bytes already read with CONNECT, then use the raw hijacked
	// connection. Reading through the outer server's connReader afterwards
	// would let an inner HTTP/1 idle-read deadline cancel the outer context.
	prefetched := io.LimitReader(buffered.Reader, int64(buffered.Reader.Buffered()))
	listener := newSingleConnectionListener(&proxyBufferedConn{Conn: client, reader: io.MultiReader(prefetched, client)})
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buffered.Flush(); err != nil {
		return
	}
	inner := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.strictRequest(w, r, allowed) }), DisableGeneralOptionsHandler: true, TLSConfig: config, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 10 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 64 << 10, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return ctx }, ConnState: func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			_ = listener.Close()
		}
	}}
	// Every keepalive request is parsed and inspected. Plaintext CONNECT uses
	// only HTTP/1; HTTPS additionally supports HTTP/2 stream authorities.
	if config == nil {
		_ = inner.Serve(listener)
	} else {
		_ = inner.ServeTLS(listener, "", "")
	}
	_ = inner.Close()
}

func (s *Server) strictRequest(w http.ResponseWriter, r *http.Request, allowed rule) {
	r, finish := s.requestWithDeadline(w, r)
	defer finish()
	upgrade := len(r.Header.Values("Upgrade")) != 0
	for _, connection := range r.Header.Values("Connection") {
		for _, token := range strings.Split(connection, ",") {
			upgrade = upgrade || strings.EqualFold(strings.TrimSpace(token), "upgrade")
		}
	}
	if r.Method == http.MethodConnect || upgrade || !strings.HasPrefix(r.RequestURI, "/") || r.URL.IsAbs() || r.URL.Opaque != "" || r.URL.User != nil || r.URL.Fragment != "" || r.URL.Host != "" {
		http.Error(w, "egress inner request denied", http.StatusForbidden)
		return
	}
	host, err := parseOrigin(allowed.scheme + "://" + r.Host)
	if err != nil || host.authority != allowed.authority {
		http.Error(w, "egress inner authority mismatch", http.StatusForbidden)
		return
	}
	select {
	case s.strict.requests <- struct{}{}:
		defer func() { <-s.strict.requests }()
	default:
		http.Error(w, "egress request limit", 503)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.URL.Scheme = allowed.scheme
	out.URL.Host = allowed.authority
	out.Host = allowed.authority
	out.Header = r.Header.Clone()
	stripHopHeaders(out.Header)
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: true, ResponseHeaderTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, MaxResponseHeaderBytes: 64 << 10, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: allowed.host, RootCAs: s.strict.upstreamRoots}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		c, err := s.dial(ctx, allowed)
		if err != nil {
			return nil, err
		}
		return &trackedConn{Conn: c, server: s}, nil
	}}
	defer transport.CloseIdleConnections()
	response, err := transport.RoundTrip(out)
	if err != nil {
		http.Error(w, "egress upstream unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusSwitchingProtocols {
		http.Error(w, "egress upstream upgrade denied", http.StatusBadGateway)
		return
	}
	stripHopHeaders(response.Header)
	for name, values := range response.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	if _, err := io.Copy(flushingProxyWriter{ResponseWriter: w}, io.LimitReader(response.Body, maxTunnelBytes)); err != nil {
		panic(http.ErrAbortHandler)
	}
}

type flushingProxyWriter struct{ http.ResponseWriter }

func (w flushingProxyWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}

type proxyBufferedConn struct {
	net.Conn
	reader io.Reader
}

func (c *proxyBufferedConn) Read(data []byte) (int, error) { return c.reader.Read(data) }

type singleConnectionListener struct {
	conn      net.Conn
	mu        sync.Mutex
	delivered bool
	done      chan struct{}
	once      sync.Once
}

func newSingleConnectionListener(c net.Conn) *singleConnectionListener {
	return &singleConnectionListener{conn: c, done: make(chan struct{})}
}
func (l *singleConnectionListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.delivered {
		l.delivered = true
		l.mu.Unlock()
		return l.conn, nil
	}
	l.mu.Unlock()
	<-l.done
	return nil, net.ErrClosed
}
func (l *singleConnectionListener) Close() error {
	var err error
	l.once.Do(func() { close(l.done); err = l.conn.Close() })
	return err
}
func (l *singleConnectionListener) Addr() net.Addr { return l.conn.LocalAddr() }
