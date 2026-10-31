package memoryio

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/managed"
)

type noCalls struct{ calls int }

func (c *noCalls) Call(context.Context, managed.Request) (json.RawMessage, *managed.Fault) {
	c.calls++
	return nil, &managed.Fault{Code: "unexpected", Message: "invalid archive reached destination"}
}

func TestArchiveDigestCoversIdentityProvenanceAndTimestamp(t *testing.T) {
	c := fixture(t, "source")
	a, err := Export(context.Background(), c, "private")
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Archive){"session": func(a *Archive) { a.SourceSession = "different" }, "agent": func(a *Archive) { a.SourceAgent = "bob" }, "namespace": func(a *Archive) { a.Namespace = "other" }, "timestamp": func(a *Archive) { a.ExportedAt = "2020-01-01T00:00:00Z" }} {
		t.Run(name, func(t *testing.T) {
			changed := a
			mutate(&changed)
			path := filepath.Join(t.TempDir(), "tampered.json")
			raw, _ := json.Marshal(changed)
			os.WriteFile(path, raw, 0600)
			if _, err := Read(path); err == nil {
				t.Fatal("provenance tampering escaped checksum")
			}
			dest := &noCalls{}
			if _, err := Import(context.Background(), dest, changed, "private", false); err == nil || dest.calls != 0 {
				t.Fatal("invalid library import reached destination")
			}
		})
	}
}

func TestImportDirectAPIRejectsDuplicatesBeforeAnyDestinationCalls(t *testing.T) {
	c := fixture(t, "source")
	a, err := Export(context.Background(), c, "private")
	if err != nil {
		t.Fatal(err)
	}
	a.Entries = []managed.Memory{{Key: "same", Value: "first"}, {Key: "same", Value: "second"}}
	a.SHA256 = archiveDigest(a)
	dest := &noCalls{}
	if _, err := Import(context.Background(), dest, a, "private", false); err == nil || dest.calls != 0 {
		t.Fatal("direct import accepted duplicates or began destination work")
	}
}
