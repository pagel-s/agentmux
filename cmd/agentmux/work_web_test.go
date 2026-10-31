package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebOwnerCapabilityHostAndOriginAreRequired(t *testing.T) {
	host, token := "127.0.0.1:12345", "private-random-owner-capability"
	handler := workWebHandler("/nonexistent", nil, token, host)
	for _, test := range []struct {
		name, path, host, origin, capability string
		code                                 int
	}{
		{"public shell", "/", host, "", "", 200},
		{"no owner token", "/api/snapshot", host, "", "", 401},
		{"wrong token", "/api/snapshot", host, "", "wrong", 401},
		{"host rebinding", "/api/snapshot", "evil.example", "", token, 403},
		{"cross origin", "/api/snapshot", host, "https://evil.example", token, 403},
		{"cross origin assets", "/app.js", host, "https://evil.example", "", 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://"+test.host+test.path, nil)
			r.Host = test.host
			if test.origin != "" {
				r.Header.Set("Origin", test.origin)
			}
			if test.capability != "" {
				r.Header.Set("Authorization", "Bearer "+test.capability)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != test.code {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), token) {
				t.Fatal("capability was embedded into public HTML or an error response")
			}
			if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Fatal("missing framing protection")
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("owner API enabled CORS")
			}
		})
	}
}

func TestWebRejectsUnexposedOperationsMalformedBodiesAndCrossSiteRequests(t *testing.T) {
	host, token := "127.0.0.1:12345", "private-random-owner-capability"
	handler := workWebHandler("/nonexistent", nil, token, host)
	for _, test := range []struct {
		body, contentType, site string
		code                    int
	}{
		{`{"operation":"agent.spawn","args":{}}`, "application/json", "", 403},
		{`{"operation":"control.get"} {}`, "application/json", "", 400},
		{`{"operation":"control.get","unexpected":true}`, "application/json", "", 400},
		{`{}`, "text/plain", "", 415},
		{`{}`, "application/json", "cross-site", 403},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/call", strings.NewReader(test.body))
		r.Host = host
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", test.contentType)
		r.Header.Set("Sec-Fetch-Site", test.site)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != test.code {
			t.Fatalf("body %q: %d %s", test.body, w.Code, w.Body.String())
		}
	}
}
