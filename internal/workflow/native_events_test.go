package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkflowRetainsNativeEventsForSuccessAndFailure(t *testing.T) {
	for _, status := range []string{"SUCCESS", "MODEL_ERROR"} {
		t.Run(status, func(t *testing.T) {
			repo, root := fixtureRepo(t)
			// A transparent protocol fixture, not an installed provider or model.
			fixture := fmt.Sprintf("import json,sys; sys.stdin.read(); print(json.dumps({'event':'result','result':{'status':%q,'response':'Recorded fixture response','usage':{'input_tokens':1,'output_tokens':2}}}), flush=True)", status)
			fixturePath := filepath.Join(root, "native-fixture.py")
			fixtureWrite(t, fixturePath, []byte(fixture))
			plan := Plan{Version: 1, Execution: "local", Repository: repo, Goal: "Read the fixture", TimeoutSeconds: 10, Candidates: []Candidate{{ID: "fixture", Harness: "antigravity", Command: []string{"python3", fixturePath}}}, Checks: []Check{{Name: "base", Command: []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_text()=='base\\n'"}}}}
			dir := filepath.Join(root, "run")
			report, err := Run(context.Background(), plan, dir, true)
			if (err == nil) != (status == "SUCCESS") {
				t.Fatalf("unexpected outcome: %+v %v", report, err)
			}
			candidate := report.Candidates[0]
			data, readErr := ReadRegularFile(candidate.NativeEventsPath, 32<<20)
			if readErr != nil {
				t.Fatal(readErr)
			}
			digest := sha256.Sum256(data)
			if candidate.NativeEventsSHA256 != hex.EncodeToString(digest[:]) {
				t.Fatal("event digest mismatch")
			}
			var event map[string]any
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(candidate.NativeEventsPath)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("events not private: %v %v", info, err)
			}
			explanation, err := Explain(dir)
			if err != nil {
				t.Fatal(err)
			}
			if explanation.Items[0].NativeEventsPath != candidate.NativeEventsPath {
				t.Fatal("event evidence missing from explanation")
			}
		})
	}
}
