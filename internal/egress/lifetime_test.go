package egress

import (
	"bufio"
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const fixtureRequestLifetime = 200 * time.Millisecond

func proxyLifetimeFixture(t *testing.T, origin string, roots *x509.CertPool) (*Server, string) {
	t.Helper()
	inspection, err := newStrictProxy(StrictOptions{UpstreamRoots: roots})
	if err != nil {
		t.Fatal(err)
	}
	socket := privateSocket(t)
	proxy, err := startWithRequestTimeout(context.Background(), socket, []Endpoint{{Origin: origin, IPs: []string{"127.0.0.1"}}}, inspection, fixtureRequestLifetime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proxy.Close() })
	return proxy, socket
}

func assertProxySlotsReleased(t *testing.T, proxy *Server) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for len(proxy.slots) != 0 || len(proxy.strict.requests) != 0 {
		select {
		case <-deadline.C:
			t.Fatalf("deadline retained handlers: tunnels=%d requests=%d", len(proxy.slots), len(proxy.strict.requests))
		case <-poll.C:
		}
	}
}

func TestProxyRequestLifetimeCannotBeDisabledOrExtended(t *testing.T) {
	for _, duration := range []time.Duration{0, -time.Second, maxRequestLifetime + time.Nanosecond} {
		if _, err := startWithRequestTimeout(context.Background(), privateSocket(t), []Endpoint{{Origin: "http://127.0.0.1:1"}}, nil, duration); err == nil {
			t.Fatalf("unbounded request lifetime accepted: %v", duration)
		}
	}
}

func TestStrictRequestLifetimeCancelsStalledUpstreamAndStreaming(t *testing.T) {
	for _, route := range []string{"absolute-http", "connect-http", "connect-https-http1", "connect-https-http2"} {
		for _, stage := range []string{"headers", "body", "stream"} {
			t.Run(route+"/"+stage, func(t *testing.T) {
				canceled := make(chan struct{})
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if stage == "body" {
						w.Header().Set("Content-Length", "1")
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
					} else if stage == "stream" {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: bounded-first\n\n")
						w.(http.Flusher).Flush()
					}
					<-r.Context().Done()
					close(canceled)
				})
				var origin string
				var roots *x509.CertPool
				if strings.Contains(route, "https") {
					_, origin, roots = strictTLSFixture(t, handler)
				} else {
					upstream := httptest.NewServer(handler)
					t.Cleanup(upstream.Close)
					origin = upstream.URL
				}
				proxy, socket := proxyLifetimeFixture(t, origin, roots)
				client := proxyClient(t, socket)
				if strings.Contains(route, "https") {
					client = strictClient(t, proxy, socket, strings.HasSuffix(route, "http2"))
				} else if route == "connect-http" {
					transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
						conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
						if err != nil {
							return nil, err
						}
						conn.SetDeadline(time.Now().Add(2 * time.Second))
						authority := strings.TrimPrefix(origin, "http://")
						fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", authority, authority)
						reader := bufio.NewReader(conn)
						response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
						if err != nil || response.StatusCode != http.StatusOK {
							conn.Close()
							return nil, fmt.Errorf("CONNECT failed: %v %v", response, err)
						}
						return &proxyBufferedConn{Conn: conn, reader: reader}, nil
					}}
					t.Cleanup(transport.CloseIdleConnections)
					client = &http.Client{Transport: transport, Timeout: 2 * time.Second}
				}
				started := time.Now()
				response, err := client.Get(origin + "/stalled-" + stage)
				if stage == "stream" {
					if err != nil {
						t.Fatal("stream did not deliver its first event before deadline:", err)
					}
					if route == "connect-https-http2" && response.ProtoMajor != 2 {
						t.Fatal("HTTP/2 deadline fixture did not negotiate HTTP/2")
					}
					reader := bufio.NewReader(response.Body)
					line, readErr := reader.ReadString('\n')
					if readErr != nil || line != "data: bounded-first\n" {
						t.Fatalf("stream buffered before deadline: %q %v", line, readErr)
					}
					_, err = io.Copy(io.Discard, reader)
				} else if err == nil {
					_, err = io.Copy(io.Discard, response.Body)
				}
				if response != nil {
					response.Body.Close()
				}
				if err == nil && (response == nil || response.StatusCode == http.StatusOK) {
					t.Fatal("stalled request completed successfully instead of expiring")
				}
				if elapsed := time.Since(started); elapsed > time.Second {
					t.Fatalf("request outlived its total deadline: %v", elapsed)
				}
				select {
				case <-canceled:
				case <-time.After(time.Second):
					t.Fatal("deadline did not cancel the upstream request")
				}
				assertProxySlotsReleased(t, proxy)
			})
		}
	}
}

func TestStrictRequestLifetimeInterruptsStalledClientUpload(t *testing.T) {
	for _, tunnel := range []bool{false, true} {
		t.Run(fmt.Sprintf("connect=%v", tunnel), func(t *testing.T) {
			started, canceled := make(chan struct{}), make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				_, _ = io.Copy(io.Discard, r.Body)
				<-r.Context().Done()
				close(canceled)
			}))
			t.Cleanup(upstream.Close)
			proxy, socket := proxyLifetimeFixture(t, upstream.URL, nil)
			authority := strings.TrimPrefix(upstream.URL, "http://")
			var conn net.Conn
			var reader *bufio.Reader
			if tunnel {
				conn, reader = strictHTTPConnect(t, socket, authority)
			} else {
				var err error
				conn, err = net.Dial("unix", socket)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				reader = bufio.NewReader(conn)
			}
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			target := upstream.URL + "/stalled-upload"
			if tunnel {
				target = "/stalled-upload"
			}
			fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: %s\r\nContent-Length: 100\r\n\r\nx", target, authority)
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("partial upload did not reach the fixture")
			}
			startedAt := time.Now()
			response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodPost})
			if err == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}
			if elapsed := time.Since(startedAt); elapsed > time.Second {
				t.Fatalf("partial client upload outlived the request deadline: %v", elapsed)
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("partial upload retained its upstream request")
			}
			assertProxySlotsReleased(t, proxy)
		})
	}
}

func TestStrictRequestLifetimeInterruptsStalledResponseConsumer(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", h2), func(t *testing.T) {
			canceled := make(chan struct{})
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(canceled)
				w.Header().Set("Content-Type", "application/octet-stream")
				chunk := make([]byte, 64<<10)
				for r.Context().Err() == nil {
					if _, err := w.Write(chunk); err != nil {
						return
					}
					w.(http.Flusher).Flush()
				}
			})
			var origin string
			var roots *x509.CertPool
			if h2 {
				_, origin, roots = strictTLSFixture(t, handler)
			} else {
				upstream := httptest.NewServer(handler)
				t.Cleanup(upstream.Close)
				origin = upstream.URL
			}
			proxy, socket := proxyLifetimeFixture(t, origin, roots)
			client := proxyClient(t, socket)
			if h2 {
				client = strictClient(t, proxy, socket, true)
			}
			response, err := client.Get(origin + "/unconsumed-body")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if h2 && response.ProtoMajor != 2 {
				t.Fatal("stalled consumer fixture did not negotiate HTTP/2")
			}
			// Keep the body open without consuming it. The proxy must interrupt
			// its blocked downstream write, cancel upstream and release slots.
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("stalled response consumer retained the upstream request")
			}
			assertProxySlotsReleased(t, proxy)
		})
	}
}
