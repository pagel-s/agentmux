package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managed"
)

func ownerWebRequest(t *testing.T, state string, client *managed.Client, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	host, token := "127.0.0.1:12345", "private-test-owner-token"
	request := httptest.NewRequest(http.MethodPost, "http://"+host+path, strings.NewReader(string(data)))
	request.Host = host
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	workWebHandler(state, client, token, host).ServeHTTP(result, request)
	return result
}

func TestWebTaskTitleUpdatesKeepIdentityAndCheckVersion(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	title := "Fix <sign-in> & API"
	result := ownerWebRequest(t, state, client, "/api/call", map[string]any{"operation": "agent.label", "args": map[string]any{"id": "peer", "title": title, "version": 0}})
	if result.Code != http.StatusOK {
		t.Fatalf("label update: %d %s", result.Code, result.Body.String())
	}
	registry := inventoryRegistry(t, client)
	if len(registry) != 1 || registry[0].ID != "peer" || registry[0].Title != title || registry[0].TitleVersion != 1 {
		t.Fatalf("label changed principal or was not saved: %+v", registry)
	}
	result = ownerWebRequest(t, state, client, "/api/call", map[string]any{"operation": "agent.label", "args": map[string]any{"id": "peer", "title": "stale edit", "version": 0}})
	if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "conflict") {
		t.Fatalf("stale label write succeeded: %d %s", result.Code, result.Body.String())
	}
	if got := inventoryRegistry(t, client)[0]; got.Title != title || got.ID != "peer" {
		t.Fatalf("stale label changed target: %+v", got)
	}
	result = ownerWebRequest(t, state, client, "/api/call", map[string]any{"operation": "agent.label", "args": map[string]any{"id": "peer", "title": "", "version": 1}})
	if result.Code != http.StatusOK {
		t.Fatalf("label clear: %s", result.Body.String())
	}
	if got := inventoryRegistry(t, client)[0]; got.Title != "" || got.TitleVersion != 2 {
		t.Fatalf("clear lost title version: %+v", got)
	}
	result = ownerWebRequest(t, state, client, "/api/call", map[string]any{"operation": "agent.label", "args": map[string]any{"id": "peer", "title": "bad\nlabel", "version": 2}})
	if result.Code != http.StatusBadRequest {
		t.Fatalf("multiline title accepted: %s", result.Body.String())
	}
}

func TestWebNewSessionCanGenerateIDAndStoreHumanTitle(t *testing.T) {
	state, client, observed := inventoryBroker(t)
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	metadata := `#!/bin/sh
for arg do
 case "$arg" in
 --help) echo '--append-system-prompt --safe-mode --model'; exit 0;;
 --version) echo '2.1.316'; exit 0;;
 status) echo '{"loggedIn":true}'; exit 0;;
 esac
done
exit 91
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(metadata), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	title := "Investigate sign-in issue"
	result := ownerWebRequest(t, state, client, "/api/add", map[string]any{"harness": "claude", "project": observed.Project, "sandbox": "local", "title": title})
	if result.Code != http.StatusOK {
		t.Fatalf("optional ID add: %d %s", result.Code, result.Body.String())
	}
	var reply struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &reply); err != nil || reply.ID == "" {
		t.Fatalf("missing generated stable ID: %s %v", result.Body.String(), err)
	}
	found := false
	for _, agent := range inventoryRegistry(t, client) {
		if agent.ID == reply.ID {
			found = true
			if agent.Title != title || agent.TitleVersion != 1 || agent.Config.Role != "" || agent.Config.Interaction != "interactive" {
				t.Fatalf("title became instructions or was not stored: %+v", agent)
			}
		}
	}
	if !found {
		t.Fatal("HTTP response selected an ID absent from the registry")
	}
}

func TestWebConnectReturnsGeneratedIDAndPreservesExistingTitle(t *testing.T) {
	state, client, observed := inventoryBroker(t)
	result := ownerWebRequest(t, state, client, "/api/connect", map[string]any{"observed_id": observed.ID, "title": "Review API behavior"})
	if result.Code != http.StatusOK {
		t.Fatalf("optional ID connect: %d %s", result.Code, result.Body.String())
	}
	var reply struct {
		ID    string `json:"id"`
		Setup string `json:"setup"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &reply); err != nil || reply.ID == "" || !strings.Contains(reply.Setup, reply.ID) {
		t.Fatalf("bridge returned wrong assigned identity: %s %v", result.Body.String(), err)
	}
	id := reply.ID
	result = ownerWebRequest(t, state, client, "/api/connect", map[string]any{"observed_id": observed.ID, "title": "Do not silently replace existing task title"})
	if result.Code != http.StatusOK {
		t.Fatalf("reconnect: %d %s", result.Code, result.Body.String())
	}
	if err := json.Unmarshal(result.Body.Bytes(), &reply); err != nil || reply.ID != id {
		t.Fatalf("blank-name reconnect changed ID: %s %v", result.Body.String(), err)
	}
	found := false
	for _, agent := range inventoryRegistry(t, client) {
		if agent.ID == id {
			found = true
			if agent.Title != "Review API behavior" || agent.ExternalRef != observed.ID {
				t.Fatalf("reconnect changed label or link: %+v", agent)
			}
		}
	}
	if !found {
		t.Fatal("generated bridge identity missing")
	}
}

func TestWebIdentityClientGuards(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js unavailable; run node --test workweb/app.test.cjs to validate client identity guards")
	}
	command := exec.Command(node, "--test", "workweb/app.test.cjs")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("client identity unit tests: %v\n%s", err, output)
	}
}
