package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateMetadataDirectoryEnumerationStopsAtConfiguredEntryBudget(t *testing.T) {
	path := t.TempDir()
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3*privateMetadataDirectoryBatch; i++ {
		fixtureWrite(t, filepath.Join(path, fmt.Sprintf("entry-%04d", i)), nil)
	}
	root, err := openPrivateMetadataRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	info, err := root.Lstat(".")
	if err != nil {
		t.Fatal(err)
	}
	budget := privateMetadataBudget{limits: privateMetadataLimits{entries: 8, pathBytes: 1 << 20, bytes: 1 << 20}}
	if err := budget.discover("."); err != nil {
		t.Fatal(err)
	}
	if names, err := privateMetadataDirectoryNames(context.Background(), root, ".", info, &budget); err == nil || names != nil {
		t.Fatalf("oversized directory enumeration was retained: %d %v", len(names), err)
	}
	if budget.discovered != budget.limits.entries {
		t.Fatalf("directory discovery exceeded its configured budget: %+v", budget)
	}
	if entries, err := listPrivateMetadata(context.Background(), root, budget.limits); err == nil || len(entries) > 1 {
		t.Fatalf("oversized names were visited before bounded discovery: %d %v", len(entries), err)
	}
}

func TestPrivateMetadataEnumerationBoundsAggregateNestedPathNames(t *testing.T) {
	path := t.TempDir()
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "parent"), 0700); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, filepath.Join(path, "parent", "file-one"), nil)
	fixtureWrite(t, filepath.Join(path, "parent", "file-two"), nil)
	root, err := openPrivateMetadataRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	limits := privateMetadataLimits{entries: 100, pathBytes: len(".") + len("parent") + len("parent/file-one"), bytes: 1 << 20}
	if _, err := listPrivateMetadata(context.Background(), root, limits); err == nil || !strings.Contains(err.Error(), "path-name bound") {
		t.Fatalf("aggregate path-name budget was not enforced: %v", err)
	}
}

func TestPrivateMetadataHashesAreIndependentOfDirectoryEntryOrder(t *testing.T) {
	var digests []string
	for _, names := range [][]string{{"z", "a", "m"}, {"m", "z", "a"}} {
		path := t.TempDir()
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if err := os.Mkdir(filepath.Join(path, name), 0700); err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, filepath.Join(path, name, "body"), []byte(name+"\n"))
		}
		root, err := openPrivateMetadataRoot(path)
		if err != nil {
			t.Fatal(err)
		}
		_, digest, err := walkPrivateMetadata(context.Background(), root, "")
		root.Close()
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, digest)
	}
	if digests[0] != digests[1] {
		t.Fatalf("metadata hashes depend on directory creation order: %v", digests)
	}
}

func TestPrivateMetadataHashesFrameFileBoundaries(t *testing.T) {
	var digests []string
	for _, folded := range []bool{false, true} {
		path := t.TempDir()
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
		body := []byte("first")
		if folded {
			body = append(body, []byte(fmt.Sprintf("z\x00%d\x00second", os.FileMode(0600)))...)
		} else {
			fixtureWrite(t, filepath.Join(path, "z"), []byte("second"))
		}
		fixtureWrite(t, filepath.Join(path, "a"), body)
		root, err := openPrivateMetadataRoot(path)
		if err != nil {
			t.Fatal(err)
		}
		_, digest, err := walkPrivateMetadata(context.Background(), root, "")
		root.Close()
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, digest)
	}
	if digests[0] == digests[1] {
		t.Fatal("file bytes impersonated a separate metadata entry")
	}
}
