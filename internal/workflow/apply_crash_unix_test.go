//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestApplicationJournalCrashChild(t *testing.T) {
	dir := os.Getenv("AGENTMUX_APPLICATION_CRASH_CHILD")
	if dir == "" {
		t.Skip("child process only")
	}
	_, err := Apply(context.Background(), dir, "fix")
	fmt.Fprintln(os.Stderr, "application unexpectedly returned", err)
	os.Exit(3)
}

func TestApplicationHardCrashBeforeAndAfterSourceMutationIsRecoverable(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprintf("after=%t", after), func(t *testing.T) {
			dir, r := evidenceRun(t)
			root := filepath.Dir(dir)
			shimDir := filepath.Join(root, "crash-shim")
			if err := os.Mkdir(shimDir, 0700); err != nil {
				t.Fatal(err)
			}
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(root, "apply-crash-ready")
			replayed := filepath.Join(root, "unexpected-replay")
			code := "#!/usr/bin/env python3\nimport os,pathlib,subprocess,sys,time\na=sys.argv[1:]\nsource=" + strconv.Quote(r.Repository) + "\nmarker=pathlib.Path(" + strconv.Quote(marker) + ")\n"
			code += "selected='apply' in a and '--check' not in a and '-C' in a and a[a.index('-C')+1]==source\n"
			code += "if selected:\n if marker.exists():\n  pathlib.Path(" + strconv.Quote(replayed) + ").write_text('replayed')\n  sys.exit(86)\n"
			if after {
				code += " subprocess.run([" + strconv.Quote(realGit) + "]+a,check=True)\n"
			}
			code += " marker.write_text(str(os.getpid()))\n time.sleep(60)\nelse:\n os.execv(" + strconv.Quote(realGit) + ",[" + strconv.Quote(realGit) + "]+a)\n"
			shim := filepath.Join(shimDir, "git")
			fixtureWrite(t, shim, []byte(code))
			if err := os.Chmod(shim, 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestApplicationJournalCrashChild$")
			cmd.Env = append(os.Environ(), "AGENTMUX_APPLICATION_CRASH_CHILD="+dir, "PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			var diagnostics strings.Builder
			cmd.Stdout, cmd.Stderr = &diagnostics, &diagnostics
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill() }()
			var childGit int
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if data, err := os.ReadFile(marker); err == nil {
					childGit, _ = strconv.Atoi(string(data))
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if childGit <= 0 {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("source operation did not reach crash point: %s", diagnostics.String())
			}
			defer syscall.Kill(-childGit, syscall.SIGKILL)
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			// The owner is dead while an admitted writer is still alive. Its
			// inherited lock must prevent a recovery from clearing the intent.
			if _, err := RecoverApply(context.Background(), dir, "fix"); err == nil || !strings.Contains(err.Error(), "active") {
				t.Fatalf("surviving source writer lost its integration lock: %v", err)
			}
			_ = syscall.Kill(-childGit, syscall.SIGKILL)
			want := "matches_start"
			if after {
				want = "matches_final"
			}
			inspection, err := InspectApply(context.Background(), dir, "fix")
			if err != nil || inspection.State != "uncertain" || inspection.SourceObservation != want || !inspection.Recoverable {
				t.Fatalf("hard-crashed application lost its intent: %+v %v", inspection, err)
			}
			for deadline := time.Now().Add(2 * time.Second); ; {
				inspection, err = RecoverApply(context.Background(), dir, "fix")
				if err == nil || time.Now().After(deadline) || !strings.Contains(err.Error(), "active") {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			wantState := "not_applied"
			if after {
				wantState = "applied"
			}
			if err != nil || inspection.State != wantState || inspection.Recoverable {
				t.Fatalf("hard-crashed application did not recover: %+v %v", inspection, err)
			}
			if _, err := os.Stat(replayed); !os.IsNotExist(err) {
				t.Fatal("recovery attempted to replay the source operation")
			}
		})
	}
}

func TestApplicationCannotMutateSourceWhenIntentCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-denial regression requires an unprivileged owner")
	}
	dir, r := evidenceRun(t)
	defer os.Chmod(dir, 0700)
	shimDir := filepath.Join(filepath.Dir(dir), "intent-fault-shim")
	if err := os.Mkdir(shimDir, 0700); err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(filepath.Dir(dir), "source-writer-admitted")
	code := "#!/usr/bin/env python3\nimport os,pathlib,sys\na=sys.argv[1:]\nif 'apply' in a and '-C' in a and a[a.index('-C')+1]==" + strconv.Quote(r.Repository) + ":\n if '--check' in a: os.chmod(" + strconv.Quote(dir) + ",0o500)\n else: pathlib.Path(" + strconv.Quote(marker) + ").write_text('admitted')\nos.execv(" + strconv.Quote(realGit) + ",[" + strconv.Quote(realGit) + "]+a)\n"
	shim := filepath.Join(shimDir, "git")
	fixtureWrite(t, shim, []byte(code))
	if err := os.Chmod(shim, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	result, err := Apply(context.Background(), dir, "fix")
	if err == nil || result.Applied || !strings.Contains(err.Error(), "persist application intent") {
		t.Fatalf("failed intent admitted source mutation: %+v %v", result, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("source-writing process was admitted without a durable intent")
	}
	data, _ := os.ReadFile(filepath.Join(r.Repository, "file.txt"))
	if string(data) != "base\n" {
		t.Fatal("source changed before intent was durably created")
	}
}

func TestApplicationReceiptFailurePreservesAppliedAndMarksUncertain(t *testing.T) {
	dir, r := evidenceRun(t)
	shimDir := filepath.Join(filepath.Dir(dir), "receipt-fault-shim")
	if err := os.Mkdir(shimDir, 0700); err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	code := "#!/usr/bin/env python3\nimport os,pathlib,subprocess,sys\na=sys.argv[1:]\nr=subprocess.run([" + strconv.Quote(realGit) + "]+a)\nif r.returncode==0 and 'apply' in a and '--check' not in a and '-C' in a and a[a.index('-C')+1]==" + strconv.Quote(r.Repository) + ":\n for p in pathlib.Path(" + strconv.Quote(dir) + ").glob('applied-*.json'):\n  p.rename(p.with_suffix('.retained-intent'))\n  p.mkdir(mode=0o700)\nsys.exit(r.returncode)\n"
	shim := filepath.Join(shimDir, "git")
	fixtureWrite(t, shim, []byte(code))
	if err := os.Chmod(shim, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	result, err := Apply(context.Background(), dir, "fix")
	if err == nil || !result.Applied || !result.Uncertain || !strings.Contains(result.Note, "Receipt could not be saved") {
		t.Fatalf("failed receipt concealed application effects: %+v %v", result, err)
	}
	data, _ := os.ReadFile(filepath.Join(r.Repository, "file.txt"))
	if string(data) != "fixed\n" {
		t.Fatal("successful source application was lost")
	}
	inspection, err := InspectApply(context.Background(), dir, "fix")
	if err != nil || inspection.State != "unknown" || inspection.Recoverable {
		t.Fatalf("damaged final receipt was treated as certain: %+v %v", inspection, err)
	}
}
