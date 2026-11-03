package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func installPrivatePolicyGitShim(t *testing.T, root, script string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "policy-git-shim")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRIVATE_POLICY_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestPrivateQuarantineRejectsLateMetadataPolicyChangesBeforeGit(t *testing.T) {
	for _, attack := range []string{"alternates", "http-alternates", "commondir", "worktree-config", "config-include", "raw-attributes", "self-consistent-ignore"} {
		t.Run(attack, func(t *testing.T) {
			ctx, repo, root, baseline := preparePrivateFixture(t)
			worker := filepath.Join(root, "worker")
			if err := CreateWorktree(ctx, baseline, worker); err != nil {
				t.Fatal(err)
			}
			store := filepath.Join(worker, ".git")
			path, data := "objects/info/alternates", []byte(filepath.Join(repo, ".git", "objects")+"\n")
			markerPayload := ""
			switch attack {
			case "http-alternates":
				path = "objects/info/http-alternates"
			case "commondir":
				path, data = "commondir", []byte(filepath.Join(repo, ".git")+"\n")
			case "worktree-config":
				path, data = "config.worktree", []byte("[core]\n bare = false\n")
			case "config-include":
				config, err := os.ReadFile(filepath.Join(store, "config"))
				if err != nil {
					t.Fatal(err)
				}
				path, data = "config", append(config, []byte("\n[include]\n path = "+filepath.Join(repo, ".git", "config")+"\n")...)
			case "raw-attributes":
				path, data = "info/attributes", []byte("* text eol=lf\n")
			case "self-consistent-ignore":
				path, data = "info/exclude", []byte("new-worker-file\n")
				var marker privateStoreMarker
				if err := DecodeFile(filepath.Join(store, privateStoreMarkerName), &marker); err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(data)
				marker.IgnoreSHA256 = hex.EncodeToString(digest[:])
				encoded, err := json.Marshal(marker)
				if err != nil {
					t.Fatal(err)
				}
				markerPayload = filepath.Join(root, "marker-payload")
				fixtureWrite(t, markerPayload, encoded)
			}
			payload := filepath.Join(root, "metadata-payload")
			fixtureWrite(t, payload, data)
			started := filepath.Join(root, "fast-import-started")
			t.Setenv("PRIVATE_POLICY_STORE", store)
			t.Setenv("PRIVATE_POLICY_PAYLOAD", payload)
			t.Setenv("PRIVATE_POLICY_TARGET", path)
			t.Setenv("PRIVATE_POLICY_MARKER_PAYLOAD", markerPayload)
			t.Setenv("PRIVATE_POLICY_ONCE", filepath.Join(root, "injected"))
			t.Setenv("PRIVATE_POLICY_STARTED", started)
			installPrivatePolicyGitShim(t, root, `selected=0
for arg in "$@"; do
 [ "$arg" != --others ] || selected=1
 [ "$arg" != fast-import ] || : > "$PRIVATE_POLICY_STARTED"
done
"$PRIVATE_POLICY_REAL_GIT" "$@"
status=$?
if [ "$status" = 0 ] && [ "$selected" = 1 ] && [ ! -e "$PRIVATE_POLICY_ONCE" ]; then
 : > "$PRIVATE_POLICY_ONCE"
 cp "$PRIVATE_POLICY_PAYLOAD" "$PRIVATE_POLICY_STORE/$PRIVATE_POLICY_TARGET" || exit 90
 if [ -n "$PRIVATE_POLICY_MARKER_PAYLOAD" ]; then
  cp "$PRIVATE_POLICY_MARKER_PAYLOAD" "$PRIVATE_POLICY_STORE/agentmux-private-store.json" || exit 91
 fi
fi
exit "$status"
`)
			fixtureWrite(t, filepath.Join(worker, "new-worker-file"), []byte("candidate bytes absent from source objects\n"))
			before := privateSourceMetadata(t, repo)
			if _, _, err := contentState(ctx, worker); err == nil {
				t.Fatal("late metadata policy change was accepted")
			}
			if _, err := os.Stat(started); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("Git mutation started before rejecting unsafe policy: %v", err)
			}
			if privateSourceMetadata(t, repo) != before {
				t.Fatal("rejected metadata policy changed source Git")
			}
		})
	}
}

func TestPrivateQuarantineRejectsUnsafePolicyBeforePublication(t *testing.T) {
	ctx, repo, root, baseline := preparePrivateFixture(t)
	worker := filepath.Join(root, "worker")
	if err := CreateWorktree(ctx, baseline, worker); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRIVATE_POLICY_SOURCE_OBJECTS", filepath.Join(repo, ".git", "objects"))
	installPrivatePolicyGitShim(t, root, `selected=0
store=
for arg in "$@"; do
 [ "$arg" != fast-import ] || selected=1
 case "$arg" in --git-dir=*) store=${arg#--git-dir=} ;; esac
done
"$PRIVATE_POLICY_REAL_GIT" "$@"
status=$?
if [ "$status" = 0 ] && [ "$selected" = 1 ]; then
 printf '%s\n' "$PRIVATE_POLICY_SOURCE_OBJECTS" > "$store/objects/info/alternates" || exit 90
fi
exit "$status"
`)
	fixtureWrite(t, filepath.Join(worker, "new-worker-file"), []byte("candidate bytes must remain quarantined\n"))
	beforeSource, beforeWorker := privateSourceMetadata(t, repo), privateSourceMetadata(t, worker)
	if _, _, err := contentState(ctx, worker); err == nil {
		t.Fatal("unsafe quarantine policy was published")
	}
	if privateSourceMetadata(t, repo) != beforeSource || privateSourceMetadata(t, worker) != beforeWorker {
		t.Fatal("rejected quarantine changed original metadata")
	}
}

func TestPrivateInitializationRejectsRedirectedBootstrapMetadata(t *testing.T) {
	repo, root := fixtureRepo(t)
	t.Setenv("PRIVATE_POLICY_SOURCE_OBJECTS", filepath.Join(repo, ".git", "objects"))
	installPrivatePolicyGitShim(t, root, `selected=0
store=
for arg in "$@"; do
 [ "$arg" != init ] || selected=1
 case "$arg" in --git-dir=*) store=${arg#--git-dir=} ;; esac
done
"$PRIVATE_POLICY_REAL_GIT" "$@"
status=$?
if [ "$status" = 0 ] && [ "$selected" = 1 ]; then
 printf '%s\n' "$PRIVATE_POLICY_SOURCE_OBJECTS" > "$store/objects/info/alternates" || exit 90
fi
exit "$status"
`)
	store := filepath.Join(root, "private.git")
	before := privateSourceMetadata(t, repo)
	if _, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, store); err == nil {
		t.Fatal("unsafe initialization metadata was published")
	}
	entries, err := os.ReadDir(store)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected initialization published private metadata: %v %v", entries, err)
	}
	if privateSourceMetadata(t, repo) != before {
		t.Fatal("rejected initialization changed source Git")
	}
}
