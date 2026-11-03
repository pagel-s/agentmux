package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/egress"
	"agentmux/internal/harness"
)

func TestWorkflowIsolationRealSourceInputsCannotBecomeNativeDependencies(t *testing.T) {
	requireFullWorkflowIsolation(t)
	_, linked, root := linkedSourceFixture(t)
	parentRun := filepath.Join(root, "parent-run")
	seed := filepath.Join(parentRun, "seed")
	childRun := filepath.Join(parentRun, "child")
	workspace := filepath.Join(childRun, "worker")
	sourceDir := filepath.Join(linked, "ignored-dir")
	external := filepath.Join(root, "external-code")
	for _, dir := range []string{parentRun, seed, childRun, workspace, sourceDir, external} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, seed, "init", "-q")
	secret := filepath.Join(linked, "ignored-secret")
	fixtureWrite(t, secret, []byte("original-only-ignored-content"))
	fixtureWrite(t, filepath.Join(sourceDir, "owner-only"), []byte("original-only-subdirectory"))
	fixtureWrite(t, filepath.Join(seed, "seed-only"), []byte("inherited-private-input"))
	fixtureWrite(t, filepath.Join(external, "data"), []byte("external dependency"))
	sourceScript := filepath.Join(sourceDir, "owner-script")
	if err := os.WriteFile(sourceScript, []byte("#!/bin/sh\nprintf original-source-executed\n"), 0700); err != nil {
		t.Fatal(err)
	}
	outsideAlias := filepath.Join(root, "source-alias")
	insideAlias := filepath.Join(linked, "external-alias")
	if err := os.Symlink(sourceDir, outsideAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, insideAlias); err != nil {
		t.Fatal(err)
	}
	parent, closeParent, err := PrepareIsolation(context.Background(), Plan{Repository: linked, Storage: StoragePrivate, Isolation: &IsolationOptions{}}, parentRun)
	if err != nil {
		t.Fatal(err)
	}
	defer closeParent()
	child, closeChild, err := PrepareIsolation(parent, Plan{Repository: seed, Storage: StoragePrivate}, childRun)
	if err != nil {
		t.Fatal(err)
	}
	defer closeChild()
	r := isolationFromContext(child)
	if !r.inputs[linked] || !r.inputs[seed] {
		t.Fatal("nested runtime lost original or inherited input identity")
	}
	if got, err := gitText(child, seed, nil, "rev-parse", "--show-toplevel"); err != nil || got != seed {
		t.Fatalf("native deny policy restricted fixed controller Git: %q %v", got, err)
	}
	runRequest := func(check bool, command []string, env map[string]string) (harness.Result, error) {
		request := harness.Request{Harness: "command", Command: command, Workdir: workspace, Env: env}
		var cleanup func()
		var err error
		if check {
			request, cleanup, err = IsolationCheckRequest(child, workspace, request)
		} else {
			request, cleanup, err = IsolationRequest(child, Candidate{ID: "writer", Harness: "command"}, request, false)
		}
		defer cleanup()
		if err != nil {
			return harness.Result{}, err
		}
		return harness.Run(child, request)
	}
	for _, check := range []bool{false, true} {
		for _, dependency := range []string{linked, sourceDir, secret, filepath.Join(linked, ".git"), seed, filepath.Join(seed, "seed-only"), outsideAlias, insideAlias} {
			r.opts.ReadOnlyPaths = []string{dependency}
			if result, err := runRequest(check, []string{"/usr/bin/cat", secret}, nil); err == nil || !strings.Contains(err.Error(), "original source") || strings.Contains(result.Text, "original-only") {
				t.Fatalf("source/input dependency admitted: check=%v path=%s result=%+v err=%v", check, dependency, result, err)
			}
		}
		r.opts.ReadOnlyPaths = nil
		if result, err := runRequest(check, []string{sourceScript}, nil); err == nil || !strings.Contains(err.Error(), "original source/input paths") || strings.Contains(result.Text, "original-source-executed") {
			t.Fatalf("original source executable admitted: check=%v %+v %v", check, result, err)
		}
	}
	if result, err := runRequest(false, []string{"owner-script"}, map[string]string{"PATH": sourceDir + ":/usr/bin:/bin"}); err == nil || !strings.Contains(err.Error(), "original source/input paths") || strings.Contains(result.Text, "original-source-executed") {
		t.Fatalf("explicit native PATH exposed original executable: %+v %v", result, err)
	}
	r.opts.Network = "allowlist"
	r.opts.Allow = []egress.Endpoint{{Origin: "http://127.0.0.1:65534", IPs: []string{"127.0.0.1"}}}
	r.relay = sourceScript
	if result, err := runRequest(false, []string{"/usr/bin/true"}, nil); err == nil || !strings.Contains(err.Error(), "original source/input paths") || strings.Contains(result.Text, "original-source-executed") {
		t.Fatalf("native relay exposed original executable: %+v %v", result, err)
	}
	r.opts.Network, r.opts.Allow = "none", nil
	// Assigned-workspace execution remains valid even when that workspace is
	// also a recorded private input during standalone verification restoration.
	r.inputs[workspace] = true
	own := filepath.Join(workspace, "own-script")
	code := fmt.Sprintf("#!/usr/bin/python3\nfrom pathlib import Path\nassert not Path(%q).exists()\nassert not Path(%q).exists()\nassert Path(%q).read_text()=='external dependency'\nprint('SOURCE_INPUT_BOUNDARY_OK')\n", linked, seed, filepath.Join(external, "data"))
	if err := os.WriteFile(own, []byte(code), 0700); err != nil {
		t.Fatal(err)
	}
	r.opts.ReadOnlyPaths = []string{external}
	for _, check := range []bool{false, true} {
		result, err := runRequest(check, []string{own}, nil)
		if err != nil || !strings.Contains(result.Text, "SOURCE_INPUT_BOUNDARY_OK") {
			t.Fatalf("own script/external dependency refused: check=%v %+v %v", check, result, err)
		}
	}
}
