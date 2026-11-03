package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func strictTLSFixture(t *testing.T, handler http.Handler) (*httptest.Server, string, *x509.CertPool) {
	t.Helper()
	ca, err := newStrictProxy(StrictOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.leaf("allowed.invalid")
	if err != nil {
		t.Fatal(err)
	}
	up := httptest.NewUnstartedServer(handler)
	up.EnableHTTP2 = true
	up.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	up.Config.ErrorLog = log.New(io.Discard, "", 0)
	up.StartTLS()
	t.Cleanup(up.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(up.URL, "https://"))
	roots := x509.NewCertPool()
	roots.AddCert(ca.ca)
	return up, "https://allowed.invalid:" + port, roots
}
func strictProxyFixture(t *testing.T, origin string, roots *x509.CertPool) (*Server, string) {
	t.Helper()
	socket := privateSocket(t)
	proxy, err := StartStrict(context.Background(), socket, []Endpoint{{Origin: origin, IPs: []string{"127.0.0.1"}}}, StrictOptions{UpstreamRoots: roots})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proxy.Close() })
	return proxy, socket
}
func strictClient(t *testing.T, proxy *Server, socket string, h2 bool) *http.Client {
	t.Helper()
	client := proxyClient(t, socket)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(proxy.PublicCertificate()) {
		t.Fatal("missing public proxy CA")
	}
	transport := client.Transport.(*http.Transport)
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	transport.ForceAttemptHTTP2 = h2
	return client
}

func TestStrictHTTPSChecksInnerAuthorityForHTTP1AndHTTP2(t *testing.T) {
	var reached atomic.Int64
	_, origin, roots := strictTLSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1); fmt.Fprint(w, r.Host) }))
	proxy, socket := strictProxyFixture(t, origin, roots)
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint(h2), func(t *testing.T) {
			client := strictClient(t, proxy, socket, h2)
			request, _ := http.NewRequest("GET", origin+"/v1/models", nil)
			request.Host = "denied.invalid"
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 403 || reached.Load() != 0 {
				t.Fatalf("inner authority escaped: status=%d proto=%s body=%s reached=%d", response.StatusCode, response.Proto, data, reached.Load())
			}
			if h2 && response.ProtoMajor != 2 {
				t.Fatal("HTTP/2 authority fixture did not negotiate h2")
			}
			response, err = client.Get(origin + "/v1/models")
			if err != nil {
				t.Fatal(err)
			}
			data, _ = io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 200 || string(data) != strings.TrimPrefix(origin, "https://") {
				t.Fatalf("valid inspected TLS request failed: %d %s", response.StatusCode, data)
			}
			reached.Store(0)
		})
	}
}

func TestStrictHTTPSRejectsAbsoluteTargetsAndWrongSNI(t *testing.T) {
	var reached atomic.Bool
	_, origin, roots := strictTLSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true); fmt.Fprint(w, "forbidden") }))
	proxy, socket := strictProxyFixture(t, origin, roots)
	authority := strings.TrimPrefix(origin, "https://")
	for _, scenario := range []string{"absolute-cross-origin", "absolute-ambiguous-host", "wrong-sni"} {
		t.Run(scenario, func(t *testing.T) {
			raw, err := net.Dial("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			raw.SetDeadline(time.Now().Add(5 * time.Second))
			fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", authority, authority)
			reader := bufio.NewReader(raw)
			response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("CONNECT failed %v %v", response, err)
			}
			roots := x509.NewCertPool()
			roots.AppendCertsFromPEM(proxy.PublicCertificate())
			name := "allowed.invalid"
			if scenario == "wrong-sni" {
				name = "denied.invalid"
			}
			client := tls.Client(&proxyBufferedConn{Conn: raw, reader: reader}, &tls.Config{RootCAs: roots, ServerName: name})
			if err = client.Handshake(); scenario == "wrong-sni" {
				if err == nil {
					t.Fatal("wrong SNI accepted")
				}
				return
			} else if err != nil {
				t.Fatal(err)
			}
			target := "https://denied.invalid/"
			if scenario == "absolute-ambiguous-host" {
				target = origin + "/"
			}
			fmt.Fprintf(client, "GET %s HTTP/1.1\r\nHost: denied.invalid\r\nConnection: close\r\n\r\n", target)
			response, err = http.ReadResponse(bufio.NewReader(client), &http.Request{Method: "GET"})
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 403 || reached.Load() {
				t.Fatal("absolute inner request bypassed CONNECT authority")
			}
		})
	}
}

func TestStrictHTTPSValidatesUpstreamAndPinnedClientsFailClosed(t *testing.T) {
	var reached atomic.Bool
	_, origin, upstreamRoots := strictTLSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true); fmt.Fprint(w, "verified") }))
	bad, socket := strictProxyFixture(t, origin, nil)
	response, err := strictClient(t, bad, socket, false).Get(origin)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 502 || reached.Load() {
		t.Fatal("untrusted upstream certificate accepted")
	}
	good, socket := strictProxyFixture(t, origin, upstreamRoots)
	client := proxyClient(t, socket)
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: upstreamRoots}
	if response, err := client.Get(origin); err == nil {
		response.Body.Close()
		t.Fatal("client pinned to upstream certificate silently bypassed inspection")
	}
	if reached.Load() {
		t.Fatal("failed client handshake reached upstream HTTP")
	}
	if strings.Contains(string(good.PublicCertificate()), "PRIVATE KEY") {
		t.Fatal("CA private key exposed")
	}
}

func TestStrictHTTPSStreamsBeforeUpstreamCompletion(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	_, origin, roots := strictTLSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: final\n\n")
	}))
	proxy, socket := strictProxyFixture(t, origin, roots)
	client := strictClient(t, proxy, socket, true)
	response, err := client.Get(origin + "/v1/responses")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("SSE buffered until completion: %q %v", line, err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
}

func strictHTTPConnect(t *testing.T, socket, authority string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", authority, authority)
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP CONNECT failed: response=%v error=%v", response, err)
	}
	return conn, reader
}

func TestStrictHTTPConnectInspectsEveryRequestAndPinsUpstream(t *testing.T) {
	var reached atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("X-Hop") != "" {
			t.Error("proxy or hop credentials reached HTTP upstream")
		}
		fmt.Fprint(w, r.Host+r.URL.RequestURI())
	}))
	defer upstream.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	authority := "bridge.invalid:" + port
	_, socket := strictProxyFixture(t, "http://"+authority, nil)
	conn, reader := strictHTTPConnect(t, socket, authority)
	// Keep the tunnel alive to prove a successful first request does not turn
	// later requests into an opaque stream or authorize a different host.
	for _, request := range []struct {
		wire   string
		status int
		body   string
	}{
		{"GET /bridge HTTP/1.1\r\nHost: " + authority + "\r\nProxy-Authorization: secret\r\nConnection: X-Hop\r\nX-Hop: secret\r\n\r\n", 200, authority + "/bridge"},
		{"GET /private HTTP/1.1\r\nHost: denied.invalid\r\n\r\n", 403, ""},
		{"POST /bridge?stream=true HTTP/1.1\r\nHost: " + authority + "\r\nContent-Length: 0\r\n\r\n", 200, authority + "/bridge?stream=true"},
	} {
		fmt.Fprint(conn, request.wire)
		response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
		if err != nil {
			t.Fatalf("request %q response: %v", request.wire, err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != request.status || request.body != "" && string(body) != request.body {
			t.Fatalf("unexpected tunneled response: status=%d body=%q error=%v", response.StatusCode, body, err)
		}
	}
	if reached.Load() != 2 {
		t.Fatalf("HTTP authority inspection admitted %d requests, want 2", reached.Load())
	}
}

func TestStrictHTTPConnectRejectsUninspectedProtocolsAndTargets(t *testing.T) {
	var reached atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1); fmt.Fprint(w, "unexpected") }))
	defer upstream.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	authority := "bridge.invalid:" + port
	_, socket := strictProxyFixture(t, "http://"+authority, nil)
	for _, scenario := range []struct{ name, target, headers string }{
		{"wrong-authority", "GET / HTTP/1.1", "Host: denied.invalid"},
		{"absolute-allowed", "GET http://" + authority + "/ HTTP/1.1", "Host: " + authority},
		{"absolute-denied", "GET http://denied.invalid/ HTTP/1.1", "Host: " + authority},
		{"nested-connect", "CONNECT " + authority + " HTTP/1.1", "Host: " + authority},
		{"upgrade", "GET / HTTP/1.1", "Host: " + authority + "\r\nConnection: Upgrade\r\nUpgrade: websocket"},
		{"upgrade-token", "GET / HTTP/1.1", "Host: " + authority + "\r\nConnection: keep-alive, uPgRaDe"},
		{"http2-upgrade", "GET / HTTP/1.1", "Host: " + authority + "\r\nUpgrade: h2c\r\nHTTP2-Settings: AAMAAABkAAQAAP__"},
		{"asterisk", "OPTIONS * HTTP/1.1", "Host: " + authority},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			conn, reader := strictHTTPConnect(t, socket, authority)
			fmt.Fprintf(conn, "%s\r\n%s\r\nConnection: close\r\n\r\n", scenario.target, scenario.headers)
			response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusForbidden || reached.Load() != 0 {
				t.Fatalf("unsafe HTTP tunnel request admitted: status=%d upstream=%d", response.StatusCode, reached.Load())
			}
		})
	}
}

func TestHTTPConnectRequiresStrictExplicitHTTPGrant(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected HTTP upstream admission") }))
	defer upstream.Close()
	authority := strings.TrimPrefix(upstream.URL, "http://")
	for _, scenario := range []struct {
		name      string
		strict    bool
		endpoints []Endpoint
	}{
		{"legacy-http", false, []Endpoint{{Origin: upstream.URL}}},
		{"strict-https", true, []Endpoint{{Origin: "https://" + authority}}},
		{"strict-both-prefers-https", true, []Endpoint{{Origin: upstream.URL}, {Origin: "https://" + authority}}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			socket := privateSocket(t)
			var proxy *Server
			var err error
			if scenario.strict {
				proxy, err = StartStrict(context.Background(), socket, scenario.endpoints, StrictOptions{})
			} else {
				proxy, err = StartLegacy(context.Background(), socket, scenario.endpoints)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			conn, err := net.Dial("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", authority, authority)
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
			if err != nil {
				t.Fatal(err)
			}
			if !scenario.strict {
				if response.StatusCode != http.StatusForbidden {
					t.Fatal("legacy proxy acquired plaintext CONNECT support")
				}
			} else {
				if response.StatusCode != http.StatusOK {
					t.Fatal("HTTPS CONNECT handshake failed")
				}
				fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", authority)
				if response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet}); err == nil && response.StatusCode == http.StatusOK {
					t.Fatal("HTTPS grant downgraded to plaintext")
				}
			}
		})
	}
}

func TestStrictHTTPConnectRetainsPrefetchedRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.URL.Path) }))
	defer upstream.Close()
	authority := strings.TrimPrefix(upstream.URL, "http://")
	_, socket := strictProxyFixture(t, upstream.URL, nil)
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	// A single write can leave some or all of the inner request in the outer
	// HTTP server's buffer when CONNECT is hijacked.
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\nGET /prefetched HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", authority, authority, authority)
	reader := bufio.NewReader(conn)
	connected, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || connected.StatusCode != http.StatusOK {
		t.Fatalf("prefetched CONNECT failed: %v %v", connected, err)
	}
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "/prefetched" {
		t.Fatalf("prefetched request lost: status=%d body=%q error=%v", response.StatusCode, body, err)
	}
}

func TestStrictHTTPConnectStreamsAndCloseRevokes(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: bridge-first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: bridge-final\n\n")
	}))
	defer upstream.Close()
	authority := strings.TrimPrefix(upstream.URL, "http://")
	proxy, socket := strictProxyFixture(t, upstream.URL, nil)
	conn, reader := strictHTTPConnect(t, socket, authority)
	fmt.Fprintf(conn, "GET /chat/completions HTTP/1.1\r\nHost: %s\r\n\r\n", authority)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	stream := bufio.NewReader(response.Body)
	if line, err := stream.ReadString('\n'); err != nil || line != "data: bridge-first\n" {
		t.Fatalf("HTTP CONNECT buffered streaming response: %q %v", line, err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if body, err := io.ReadAll(stream); err == nil || strings.Contains(string(body), "bridge-final") {
		t.Fatalf("closed proxy left HTTP tunnel active: body=%q error=%v", body, err)
	}
}
