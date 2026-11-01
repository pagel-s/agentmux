package main

import (
	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func teamCLIBroker(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "amx-team-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	state := filepath.Join(root, "s")
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "teamcli", Teams: managedconfig.Teams{Enabled: true, Creators: []string{"alice"}}, Agents: map[string]managedconfig.Agent{"alice": {Harness: "command", Command: []string{"/bin/true"}}, "bob": {Harness: "command", Command: []string{"/bin/true"}}}}
	store, err := managed.Init(state, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- managed.Serve(ctx, store) }()
	t.Cleanup(func() { cancel(); <-done; store.Close() })
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		t.Fatal(err)
	}
	client := managed.NewClient(credential)
	until := time.Now().Add(3 * time.Second)
	for {
		if _, err = workCall(ctx, client, "identity", map[string]any{}); err == nil {
			break
		}
		select {
		case e := <-done:
			done <- e
			t.Fatal(e)
		default:
		}
		if time.Now().After(until) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond * 10)
	}
	return state
}
func teamCLI(t *testing.T, state string, args ...string) managed.Team {
	t.Helper()
	var out, diag bytes.Buffer
	args = append(args, "--state", state)
	if code := runWorkTeam(args, &out, &diag); code != exitSuccess {
		t.Fatalf("team%v exit%d: %s", args, code, &diag)
	}
	var response struct {
		Team managed.Team `json:"team"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Team
}
func TestTeamCLIUsesExplicitAgentConsentAndCAS(t *testing.T) {
	state := teamCLIBroker(t)
	team := teamCLI(t, state, "create", "Temporary investigation", "--description", "Two repos")
	team = teamCLI(t, state, "invite", team.ID, "bob")
	var out, diag bytes.Buffer
	if code := runWorkTeam([]string{"join", team.ID, "--state", state}, &out, &diag); code == exitSuccess || !strings.Contains(diag.String(), "require --credential-file") {
		t.Fatal("join silently used owner authority")
	}
	team = teamCLI(t, state, "join", team.ID, "--credential-file", managed.CredentialPath(state, "bob"))
	team = teamCLI(t, state, "transfer", team.ID, "bob")
	if team.PendingOwner != "bob" || team.Owner != "$owner" {
		t.Fatal("proposal performed transfer")
	}
	team = teamCLI(t, state, "accept-owner", team.ID, "--credential-file", managed.CredentialPath(state, "bob"))
	if team.Owner != "bob" {
		t.Fatal("consented transfer missing")
	}
	version := team.Version
	team = teamCLI(t, state, "edit", team.ID, "--name", "Reviewed team", "--description", "")
	if team.Name != "Reviewed team" || team.Description != "" {
		t.Fatal("metadata editor lost explicit empty description")
	}
	diag.Reset()
	if code := runWorkTeam([]string{"edit", team.ID, "--name", "Stale", "--version", jsonNumber(version), "--state", state}, &out, &diag); code == exitSuccess || !strings.Contains(diag.String(), "conflict") {
		t.Fatal("CLI retried stale reviewedversion")
	}
}
func jsonNumber(n int) string { data, _ := json.Marshal(n); return string(data) }
func TestTeamCLIHelpAndWorkspaceSafetyBeforeIO(t *testing.T) {
	var out, diag bytes.Buffer
	if runWorkTeam([]string{"--help"}, &out, &diag) != exitSuccess || !strings.Contains(out.String(), "team move") {
		t.Fatal("missing team help")
	}
	for _, args := range [][]string{{"list", "--state", "/tmp/no-state", "--session", "other"}, {"list", "--state", ""}, {"edit", "some", "--name", "a", "--name", "b"}, {"create", "name", "--to-version", "2"}} {
		out.Reset()
		diag.Reset()
		if runWorkTeam(args, &out, &diag) == exitSuccess || diag.Len() == 0 {
			t.Fatalf("invalid flags accepted%v", args)
		}
	}
}

func TestCollaborationLegacyPolicyParserAndWireChoice(t *testing.T) {
	for _, policy := range []string{"preserve", "group"} {
		o, err := parseWorkOptions([]string{"alice", "bob", "--legacy-policy", policy})
		if err != nil || o.legacyPolicy != policy {
			t.Fatalf("legacychoiceparse%q %v", policy, err)
		}
	}
	if _, err := parseWorkOptions([]string{"--legacy-policy", "guess"}); err == nil {
		t.Fatal("invalidmigrationpolicyaccepted")
	}
	state := teamCLIBroker(t)
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		t.Fatal(err)
	}
	client := managed.NewClient(credential)
	raw, err := workCollaboration(context.Background(), client, []string{"alice", "bob"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var c managed.ControlState
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Group == nil || c.Group.ContributionsVersion != 1 {
		t.Fatal("newCLIgroupnotversioned")
	}
	_, err = workCall(context.Background(), client, "control.update", map[string]any{"version": c.Version, "kind": "edge", "from": "alice", "to": "bob", "action": "message.send", "allow": true})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(state, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("UPDATE records SET data=json_remove(data,'$.contributions_version') WHERE kind='workspace_group'"); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := runWorkSession("collaborate", []string{"alice", "bob", "--state", state}, strings.NewReader(""), &out, &diag); code == exitSuccess || !strings.Contains(diag.String(), "explicit owner review") {
		t.Fatal("legacyCLIusedimplicitpolicy")
	}
	out.Reset()
	diag.Reset()
	if code := runWorkSession("collaborate", []string{"alice", "bob", "--state", state, "--legacy-policy", "group"}, strings.NewReader(""), &out, &diag); code != exitSuccess {
		t.Fatalf("explicitlegacyCLIchoicefailed %s", &diag)
	}
	raw, err = workCall(context.Background(), client, "control.get", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	c = managed.ControlState{}
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Edges) != 0 || len(c.Groups) != 1 || c.Groups[0].ContributionsVersion != 1 {
		t.Fatal("legacyCLIpolicywasnotapplied")
	}
}
