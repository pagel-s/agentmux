package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCatalogPreviewHasNoSideEffectsAndScopesProjects(t *testing.T) {
	repo, root := fixtureRepo(t)
	ctx := context.Background()
	catalog := filepath.Join(root, "history")
	output, err := SuggestedOutput(ctx, repo, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output, catalog+string(filepath.Separator)) {
		t.Fatal("output outside selected catalog")
	}
	if _, err := os.Stat(catalog); !os.IsNotExist(err) {
		t.Fatal("preview created history")
	}
	runs, err := ListRuns(ctx, repo, catalog)
	if err != nil || len(runs) != 0 {
		t.Fatalf("empty history: %v %v", runs, err)
	}
	if err := PrepareCatalog(ctx, repo, filepath.Join(repo, "history")); err == nil {
		t.Fatal("catalog admitted inside source")
	}
	if _, err := os.Stat(filepath.Join(repo, "history")); !os.IsNotExist(err) {
		t.Fatal("invalid catalog created inside source")
	}
	if err := PrepareCatalog(ctx, repo, catalog); err != nil {
		t.Fatal(err)
	}
	public := filepath.Join(root, "public-history")
	if err := os.Mkdir(public, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := SuggestedOutput(ctx, repo, public); err == nil {
		t.Fatal("preview accepted a nonprivate managed history root")
	}
	for _, p := range []string{catalog, filepath.Dir(filepath.Dir(output)), filepath.Dir(output)} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("private history %s: %v %v", p, info, err)
		}
	}
}

func TestCatalogRegistersAndResolvesExternalRunWithoutCrossProjectLeak(t *testing.T) {
	dir, r := evidenceRun(t)
	ctx := context.Background()
	catalog := filepath.Join(filepath.Dir(dir), "history")
	if err := PrepareCatalog(ctx, r.Repository, catalog); err != nil {
		t.Fatal(err)
	}
	if err := RegisterRun(ctx, r.Repository, catalog, dir); err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"", "latest", r.ID, dir} {
		got, err := ResolveRun(ctx, r.Repository, catalog, selector)
		if err != nil || got != dir {
			t.Fatalf("resolve %q: %q %v", selector, got, err)
		}
	}
	rows, err := ListRuns(ctx, r.Repository, catalog)
	if err != nil || len(rows) != 1 || rows[0].Goal != r.Goal || rows[0].State != "completed" {
		t.Fatalf("registered rows: %+v %v", rows, err)
	}
	other, _ := fixtureRepo(t)
	rows, err = ListRuns(ctx, other, catalog)
	if err != nil || len(rows) != 0 {
		t.Fatalf("cross-project list: %+v %v", rows, err)
	}
	if _, err = ResolveRun(ctx, other, catalog, dir); err == nil {
		t.Fatal("cross-project explicit run admitted")
	}
	if err = RegisterRun(ctx, other, catalog, dir); err == nil {
		t.Fatal("cross-project registration admitted")
	}
}

func TestCatalogRetainsFailureAndDoesNotSilentlyFallBackFromMissingLatest(t *testing.T) {
	dir, r := evidenceRun(t)
	ctx := context.Background()
	catalog := filepath.Join(filepath.Dir(dir), "history")
	if err := PrepareCatalog(ctx, r.Repository, catalog); err != nil {
		t.Fatal(err)
	}
	r.State = "interrupted"
	r.Error = "Fixture interrupted before review"
	if err := saveJSON(filepath.Join(dir, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	if err := RegisterRun(ctx, r.Repository, catalog, dir); err != nil {
		t.Fatal(err)
	}
	rows, err := ListRuns(ctx, r.Repository, catalog)
	if err != nil || len(rows) != 1 || rows[0].State != "interrupted" {
		t.Fatalf("lost interrupted work: %+v %v", rows, err)
	}
	if err = os.Rename(filepath.Join(dir, "report.json"), filepath.Join(dir, "retained-report.json")); err != nil {
		t.Fatal(err)
	}
	rows, err = ListRuns(ctx, r.Repository, catalog)
	if err != nil || rows[0].State != "unavailable" {
		t.Fatalf("missing run concealed: %+v %v", rows, err)
	}
	if _, err = ResolveRun(ctx, r.Repository, catalog, "latest"); err == nil {
		t.Fatal("missing latest silently resolved")
	}
}

func TestCatalogConcurrentRegistrationAndTerminalOutcome(t *testing.T) {
	dir, r := evidenceRun(t)
	ctx := context.Background()
	catalog := filepath.Join(filepath.Dir(dir), "history")
	if err := PrepareCatalog(ctx, r.Repository, catalog); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- RegisterRun(ctx, r.Repository, catalog, dir) }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ListRuns(ctx, r.Repository, catalog)
	if err != nil || len(rows) != 1 {
		t.Fatalf("registration lost update: %+v %v", rows, err)
	}
	// Older recorded running state cannot replace a recorded terminal outcome.
	r.State = "running"
	r.FinishedAt = ""
	if err = saveJSON(filepath.Join(dir, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	if err = RegisterRun(ctx, r.Repository, catalog, dir); err != nil {
		t.Fatal(err)
	}
	rows, err = ListRuns(ctx, r.Repository, catalog)
	if err != nil || rows[0].State != "completed" {
		t.Fatalf("terminal history regressed: %+v %v", rows, err)
	}
}

func TestCatalogRefusesSymlinkAndTamperedPointers(t *testing.T) {
	dir, r := evidenceRun(t)
	ctx := context.Background()
	parent := filepath.Dir(dir)
	catalog := filepath.Join(parent, "history")
	if err := PrepareCatalog(ctx, r.Repository, catalog); err != nil {
		t.Fatal(err)
	}
	if err := RegisterRun(ctx, r.Repository, catalog, dir); err != nil {
		t.Fatal(err)
	}
	_, _, _, index, _, err := catalogPaths(ctx, r.Repository, catalog)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(index, r.ID+".json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(parent, "pointer.json")
	fixtureWrite(t, external, original)
	if err = os.Rename(path, filepath.Join(parent, "retained-pointer.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}
	if _, err = ListRuns(ctx, r.Repository, catalog); err == nil {
		t.Fatal("symlinked pointer accepted")
	}
	link := filepath.Join(parent, "history-link")
	if err = os.Symlink(catalog, link); err != nil {
		t.Fatal(err)
	}
	if _, err = SuggestedOutput(ctx, r.Repository, link); err == nil {
		t.Fatal("symlink catalog admitted")
	}
}

func TestCatalogRegistrationLockIsBoundedAndCancellable(t *testing.T) {
	dir, r := evidenceRun(t)
	ctx := context.Background()
	catalog := filepath.Join(filepath.Dir(dir), "history")
	if err := PrepareCatalog(ctx, r.Repository, catalog); err != nil {
		t.Fatal(err)
	}
	_, _, _, index, _, err := catalogPaths(ctx, r.Repository, catalog)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := lockApplication(filepath.Join(index, ".register-catalog.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err = RegisterRun(ctx, r.Repository, catalog, dir); err == nil {
		t.Fatal("live registration lock ignored")
	}
	if time.Since(start) > time.Second {
		t.Fatal("registration ignored cancellation")
	}
}

func TestCatalogLatestUsesChronologicalRFC3339Times(t *testing.T) {
	for _, times := range [][2]string{
		{"2026-11-02T18:00:00Z", "2026-11-02T18:00:00.1Z"},
		{"2026-11-02T19:00:00+01:00", "2026-11-02T18:00:01Z"},
	} {
		t.Run(times[1], func(t *testing.T) {
			dir, report := evidenceRun(t)
			ctx := context.Background()
			catalog := filepath.Join(filepath.Dir(dir), "history")
			if err := PrepareCatalog(ctx, report.Repository, catalog); err != nil {
				t.Fatal(err)
			}
			report.StartedAt = times[0]
			if err := saveJSON(filepath.Join(dir, "report.json"), report); err != nil {
				t.Fatal(err)
			}
			if err := RegisterRun(ctx, report.Repository, catalog, dir); err != nil {
				t.Fatal(err)
			}
			newer := report
			newer.ID += "-later"
			newer.Output = filepath.Join(filepath.Dir(dir), "later")
			newer.StartedAt = times[1]
			if err := os.Mkdir(newer.Output, 0700); err != nil {
				t.Fatal(err)
			}
			if err := saveJSON(filepath.Join(newer.Output, "report.json"), newer); err != nil {
				t.Fatal(err)
			}
			if err := RegisterRun(ctx, report.Repository, catalog, newer.Output); err != nil {
				t.Fatal(err)
			}
			got, err := ResolveRun(ctx, report.Repository, catalog, "latest")
			if err != nil || got != newer.Output {
				t.Fatalf("latest selected the older instant: %q want %q, %v", got, newer.Output, err)
			}
		})
	}
}
