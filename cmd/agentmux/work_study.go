package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
	"agentmux/internal/research"
)

type workHandlerBuilder func(string, *managed.Client, string, string) http.Handler

// Study following is explicitly opt-in. Every mutation is fenced by the exact
// attempt displayed when the review began; trial transitions never confer
// authority on an old form, even when agents and policy versions repeat.
func workStudyHandler(state string, client *managed.Client, token, host string, build workHandlerBuilder) http.Handler {
	base := build(state, client, token, host)
	output, problem := research.StudyOutput(state)
	if problem != nil {
		return base
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+host || r.Header.Get("Sec-Fetch-Site") == "cross-site" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			base.ServeHTTP(w, r)
			return
		}
		fail := func(message string, code int) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": "conflict", "error": message})
		}
		if r.URL.Path == "/api/study" && r.Method == http.MethodGet {
			view, err := research.StudyStatus(r.Context(), output)
			if err != nil {
				fail(err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "study": view})
			return
		}
		if r.URL.Path == "/api/research-history" && r.Method == http.MethodGet {
			q := r.URL.Query()
			limit, _ := strconv.Atoi(q.Get("limit"))
			page, err := research.ReadHistory(output, research.HistoryFilter{After: q.Get("after"), Limit: limit, Actor: q.Get("actor"), Type: q.Get("type"), Query: q.Get("query")})
			if err != nil {
				fail(err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "history": page})
			return
		}
		if r.Header.Get("X-Agentmux-Follow-Study") != "1" {
			if r.Method == http.MethodPost {
				fixed, err := research.LiveStatus(r.Context(), state)
				if err != nil || fixed.Research == nil || r.Header.Get("X-Agentmux-Attempt") != research.AttemptIdentity(state, fixed.Research) {
					fail("Displayed study attempt does not match this fixed observer. Refresh before applying controls.", http.StatusConflict)
					return
				}
			}
			base.ServeHTTP(w, r)
			return
		}
		view, err := research.StudyStatus(r.Context(), output)
		if err != nil || view.CurrentState == "" {
			fail("Waiting for the next study attempt; controls are unavailable.", http.StatusConflict)
			return
		}
		if r.Method == http.MethodPost && (view.State == "completed" || view.AttemptID == "" || r.Header.Get("X-Agentmux-Attempt") != view.AttemptID) {
			fail("Study attempt changed. Load and review the current attempt before applying controls.", http.StatusConflict)
			return
		}
		credential, err := managed.LoadCredential(managed.CredentialPath(view.CurrentState, ""))
		if err != nil {
			fail("Current study owner credential is unavailable.", http.StatusConflict)
			return
		}
		build(view.CurrentState, managed.NewClient(credential), token, host).ServeHTTP(w, r)
	})
}

func workStudyAttempt(state string, runtime managedlaunch.Runtime, status *managed.ResearchStatus) string {
	if status != nil {
		return research.AttemptIdentity(state, status)
	}
	return runtime.AttemptID
}
