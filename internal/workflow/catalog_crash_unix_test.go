//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCatalogRegistrationCrashChild(t *testing.T) {
	lock := os.Getenv("AGENTMUX_CATALOG_CRASH_LOCK")
	if lock == "" {
		t.Skip("child process only")
	}
	lease, err := lockApplication(lock)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := os.WriteFile(os.Getenv("AGENTMUX_CATALOG_CRASH_READY"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
}

func TestCatalogRegistrationAutomaticallyRecoversItsLockAfterOwnerIsKilled(t *testing.T) {
	dir, r := evidenceRun(t)
	catalog := filepath.Join(filepath.Dir(dir), "history")
	if err := PrepareCatalog(context.Background(), r.Repository, catalog); err != nil {
		t.Fatal(err)
	}
	_, _, _, index, _, err := catalogPaths(context.Background(), r.Repository, catalog)
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(index, ".register-catalog.lock")
	ready := filepath.Join(filepath.Dir(dir), "catalog-lock-ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCatalogRegistrationCrashChild$")
	cmd.Env = append(os.Environ(), "AGENTMUX_CATALOG_CRASH_LOCK="+lock, "AGENTMUX_CATALOG_CRASH_READY="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("registration child did not hold its lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("coordination inode should remain after interruption")
	}
	if err := RegisterRun(context.Background(), r.Repository, catalog, dir); err != nil {
		t.Fatalf("dead registrar left a stale exclusive lock: %v", err)
	}
}

func TestCatalogRecordCapacityExcludesCoordinationAndTemporaryEntries(t *testing.T) {
	dir, r := evidenceRun(t)
	catalog := filepath.Join(filepath.Dir(dir), "history")
	if err := PrepareCatalog(context.Background(), r.Repository, catalog); err != nil {
		t.Fatal(err)
	}
	_, _, _, index, _, err := catalogPaths(context.Background(), r.Repository, catalog)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := lockApplication(filepath.Join(index, ".register-catalog.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	fixtureWrite(t, filepath.Join(index, ".register-previous.lock"), []byte("old coordination inode"))
	fixtureWrite(t, filepath.Join(index, ".catalog-interrupted"), []byte("temporary record"))
	for i := 0; i < maxCatalogEntries; i++ {
		id := fmt.Sprintf("capacity-%04d", i)
		record := catalogRecord{Version: 1, ID: id, Repository: r.Repository, Directory: dir, StartedAt: r.StartedAt, RegisteredAt: r.StartedAt, State: r.State, FinishedAt: r.FinishedAt, Goal: r.Goal}
		if err := saveJSON(filepath.Join(index, id+".json"), record); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := ListRuns(context.Background(), r.Repository, catalog)
	if err != nil || len(runs) != maxCatalogEntries {
		t.Fatalf("coordination entries consumed record capacity: %d %v", len(runs), err)
	}
	fixtureWrite(t, filepath.Join(index, "unknown-artifact"), []byte("unexpected"))
	if _, err := ListRuns(context.Background(), r.Repository, catalog); err == nil {
		t.Fatal("unexpected index entry silently ignored")
	}
}
