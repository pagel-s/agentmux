package managed

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestPortableTraceBundlePinsSignerAndNeverExportsKeys(t *testing.T) {
	s := testStore(t, testConfig())
	trace, err := beginTrace(s.dir, "alice", "task_portable", managedconfig.Trace{MaxTurnBytes: 8192, MaxTotalBytes: 16384}, map[string]string{"input": "retained context"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := trace.finish("complete")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "portable.json")
	exported, err := ExportTraceBundle(s.dir, "alice", summary.ID, summary.MAC, path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("bundle permissions not private")
	}
	for _, keyPath := range []string{filepath.Join(s.dir, "traces", "trace.key"), filepath.Join(s.dir, "traces", "portable-signing.key")} {
		key, err := os.ReadFile(keyPath)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, key) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(key))) {
			t.Fatal("bundle disclosed a secret key")
		}
	}
	if bytes.Contains(raw, []byte("secret-value")) || bytes.Contains(raw, []byte(credential(t, s, "").Token)) {
		t.Fatal("bundle dumped configuration or credentials")
	}
	verified, err := VerifyTraceBundle(path, exported.Fingerprint)
	if err != nil || verified != exported || verified.Summary != summary {
		t.Fatalf("portable verification: %+v %v", verified, err)
	}
	if _, err := VerifyTraceBundle(path, ""); err == nil {
		t.Fatal("self-selected embedded key accepted as trusted")
	}
	if _, err := VerifyTraceBundle(path, strings.Repeat("0", 64)); err == nil {
		t.Fatal("different trusted signer accepted")
	}
	if _, err := ExportTraceBundle(s.dir, "alice", summary.ID, summary.MAC, path); err == nil {
		t.Fatal("existing artifact overwritten")
	}
	var bundle TraceBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	bundle.Payload.JSONL = strings.Replace(bundle.Payload.JSONL, "retained context", "tampered context", 1)
	changed, _ := json.Marshal(bundle)
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTraceBundle(path, exported.Fingerprint); err == nil {
		t.Fatal("tampered signed content accepted")
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Verification is genuinely portable: it needs neither the HMAC key nor
	// any local broker state or signing private key.
	if err := os.Rename(s.dir, s.dir+"-offline"); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTraceBundle(path, exported.Fingerprint); err != nil {
		t.Fatal(err)
	}
}
