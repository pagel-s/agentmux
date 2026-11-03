package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExactOriginsRejectAmbiguity(t *testing.T) {
	for _, v := range []string{"https://a.test/path", "https://user:pass@a.test", "https://a.test?x=1", "https://*.test", "https://a.test.", "ftp://a.test", "https://a.test:0", "https://a.test:65536", "https://a.test\\@evil.test", "https://a.test/#x", "https://ä.test", "https://a..test"} {
		if Validate([]Endpoint{{Origin: v}}) == nil {
			t.Errorf("accepted %q", v)
		}
	}
	for _, v := range []string{"https://a.test", "http://127.0.0.1:8080", "https://[::1]:443"} {
		if err := Validate([]Endpoint{{Origin: v}}); err != nil {
			t.Errorf("rejected %q: %v", v, err)
		}
	}
	if Validate([]Endpoint{{Origin: "https://a.test", IPs: []string{"0.0.0.0"}}}) == nil {
		t.Fatal("unspecified pin accepted")
	}
}

func TestAllowlistAttenuationCannotBroadenDNSOrPins(t *testing.T) {
	parent := []Endpoint{{Origin: "https://EXAMPLE.com", IPs: []string{"192.0.2.1", "192.0.2.2"}}}
	if !IsSubset([]Endpoint{{Origin: "https://example.com:443", IPs: []string{"192.0.2.1"}}}, parent) {
		t.Fatal("narrow pinned child rejected")
	}
	for _, child := range [][]Endpoint{{{Origin: "https://example.com"}}, {{Origin: "https://evil.com", IPs: []string{"192.0.2.1"}}}, {{Origin: "https://example.com", IPs: []string{"127.0.0.1"}}}, {{Origin: "http://example.com", IPs: []string{"192.0.2.1"}}}} {
		if IsSubset(child, parent) {
			t.Fatal("broader child accepted")
		}
	}
	if IsSubset([]Endpoint{{Origin: "https://example.com", IPs: []string{"127.0.0.1"}}}, []Endpoint{{Origin: "https://example.com"}}) {
		t.Fatal("child turned public DNS origin into private service")
	}
	if Validate([]Endpoint{{Origin: "https://127.0.0.1", IPs: []string{"192.0.2.1"}}}) == nil {
		t.Fatal("literal origin silently redirected to another IP")
	}
}

func privateSocket(t *testing.T) string {
	t.Helper()
	// Unix socket paths have a small fixed limit. Go versions that include
	// full subtest names in TempDir can exceed it before the proxy is tested.
	dir, err := os.MkdirTemp("/tmp", "amx-proxy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "proxy.sock")
}

func proxyClient(t *testing.T, socket string) *http.Client {
	t.Helper()
	p, _ := url.Parse("http://unix-proxy.invalid")
	tr := &http.Transport{Proxy: http.ProxyURL(p), DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

func TestHTTPExactOriginRedirectAndHeaderIsolation(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; fmt.Fprint(w, "private") }))
	defer other.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("X-Hop") != "" {
			t.Error("proxy or hop credentials forwarded")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, other.URL, 302)
			return
		}
		fmt.Fprint(w, "allowed")
	}))
	defer up.Close()
	socket := privateSocket(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := Start(ctx, socket, []Endpoint{{Origin: up.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	client := proxyClient(t, socket)
	req, _ := http.NewRequest("GET", up.URL, nil)
	req.Header.Set("Proxy-Authorization", "secret")
	req.Header.Set("Connection", "X-Hop")
	req.Header.Set("X-Hop", "secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "allowed" {
		t.Fatalf("allowed failed: %d", resp.StatusCode)
	}
	resp, err = client.Get(up.URL + "/redirect")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 || leaked {
		t.Fatal("redirect crossed allowlist")
	}
	req, _ = http.NewRequest("GET", up.URL, nil)
	req.Host = "evil.invalid"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("Host mismatch accepted")
	}
	if info, _ := os.Stat(socket); info.Mode().Perm() != 0600 {
		t.Fatal("socket not private")
	}
	if _, err = Start(ctx, socket, []Endpoint{{Origin: up.URL}}); err == nil {
		t.Fatal("existing socket replaced")
	}
}

func TestDNSPinnedAndPrivateRequiresExplicitIPs(t *testing.T) {
	if _, err := resolve(context.Background(), []Endpoint{{Origin: "http://localhost:1234"}}); err == nil {
		t.Fatal("implicit private DNS allowed")
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "pinned") }))
	defer up.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(up.URL, "http://"))
	origin := "http://does-not-resolve.invalid:" + port
	socket := privateSocket(t)
	s, err := Start(context.Background(), socket, []Endpoint{{Origin: origin, IPs: []string{"127.0.0.1"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	resp, err := proxyClient(t, socket).Get(origin)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "pinned" {
		t.Fatal("explicit DNS pins not used")
	}
}

func TestTLSExactSNIBeforeUpstream(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "tls-allowed") }))
	defer up.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(up.URL, "https://"))
	origin := "https://allowed.invalid:" + port
	socket := privateSocket(t)
	s, err := Start(context.Background(), socket, []Endpoint{{Origin: origin, IPs: []string{"127.0.0.1"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	client := proxyClient(t, socket)
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // isolated test server
	resp, err := client.Get(origin)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "tls-allowed" {
		t.Fatal("TLS allowed origin failed")
	}
	c, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT allowed.invalid:%s HTTP/1.1\r\nHost: allowed.invalid:%s\r\n\r\n", port, port)
	br := bufio.NewReader(c)
	resp, err = http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil || resp.StatusCode != 200 {
		t.Fatal("CONNECT handshake failed", err)
	}
	bad := tls.Client(c, &tls.Config{ServerName: "evil.invalid", InsecureSkipVerify: true})
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if err = bad.Handshake(); err == nil {
		t.Fatal("domain fronting accepted")
	}
}

func hello(host string, ech bool) []byte {
	sni := []byte{0, byte(len(host) >> 8), byte(len(host))}
	sni = append(sni, []byte(host)...)
	names := []byte{byte(len(sni) >> 8), byte(len(sni))}
	names = append(names, sni...)
	ext := []byte{0, 0, byte(len(names) >> 8), byte(len(names))}
	ext = append(ext, names...)
	if ech {
		ext = append(ext, 0xfe, 0x0d, 0, 0)
	}
	b := make([]byte, 34)
	b[0] = 3
	b[1] = 3
	b = append(b, 0, 0, 2, 0x13, 1, 1, 0, byte(len(ext)>>8), byte(len(ext)))
	b = append(b, ext...)
	hand := []byte{1, byte(len(b) >> 16), byte(len(b) >> 8), byte(len(b))}
	hand = append(hand, b...)
	wire := []byte{22, 3, 1, byte(len(hand) >> 8), byte(len(hand))}
	return append(wire, hand...)
}

func TestClientHelloRejectsSmugglingAndOpaqueNames(t *testing.T) {
	for _, tc := range []struct {
		host string
		ech  bool
		want bool
	}{{"allowed.invalid", false, true}, {"evil.invalid", false, false}, {"allowed.invalid", true, false}, {"", false, false}} {
		_, err := readClientHello(strings.NewReader(string(hello(tc.host, tc.ech))), "allowed.invalid")
		if (err == nil) != tc.want {
			t.Fatalf("host=%q ech=%v err=%v", tc.host, tc.ech, err)
		}
	}
	wire := hello("allowed.invalid", false)
	binary.BigEndian.PutUint16(wire[3:5], 65535)
	if _, err := readClientHello(strings.NewReader(string(wire)), "allowed.invalid"); err == nil {
		t.Fatal("oversized TLS record accepted")
	}
	if _, err := readClientHello(strings.NewReader("GET / HTTP/1.1\r\n"), "allowed.invalid"); err == nil {
		t.Fatal("non-TLS CONNECT accepted")
	}
}

func TestCloseRevokesLiveTunnel(t *testing.T) {
	socket := privateSocket(t)
	s, err := Start(context.Background(), socket, []Endpoint{{Origin: "https://127.0.0.1:9"}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprint(c, "CONNECT 127.0.0.1:9 HTTP/1.1\r\nHost: 127.0.0.1:9\r\n\r\n")
	_, err = http.ReadResponse(bufio.NewReader(c), &http.Request{Method: "CONNECT"})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = c.Read(make([]byte, 1)); err == nil {
		t.Fatal("closed proxy left tunnel alive")
	}
}
