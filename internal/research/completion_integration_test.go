package research

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real finite command workers exercise the trial loop as well as the broker
// barrier. No provider credentials or model turns are used by this fixture.
func TestResearchTerminalControlsAcrossTrialsIntegration(t *testing.T) {
	if os.Getenv("AGENTMUX_RESEARCH_INTEGRATION") != "1" {
		t.Skip("opt in to model-free local research with AGENTMUX_TEST_BINARY")
	}
	binary := os.Getenv("AGENTMUX_TEST_BINARY")
	if binary == "" {
		t.Fatal("AGENTMUX_TEST_BINARY required")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"stop", "pause", "unused-step", "used-step"} {
		t.Run(mode, func(t *testing.T) {
			root, err := os.MkdirTemp("/tmp", "amx-terminal-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(root)
			t.Setenv("XDG_STATE_HOME", filepath.Join(root, "xdg"))
			script := filepath.Join(root, "agent.py")
			if err := os.WriteFile(script, []byte("import sys,time\np=sys.stdin.read()\nif 'Action phase:' in p:\n time.sleep(0.6)\n print('{\"project\":null,\"contribute\":0}')\nelse: print('{\"ready\":true}')\n"), 0600); err != nil {
				t.Fatal(err)
			}
			m, err := Load(filepath.Join("..", "..", "examples", "research", "public-goods.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			m.Conditions = m.Conditions[:1]
			m.Seeds = []int64{11, 22}
			m.Game.Rounds = 1
			m.BrokerSandbox = "local"
			for id, a := range m.Config.Agents {
				a.Command = []string{python, script}
				a.Sandbox, a.Network = "local", "host"
				a.ReadOnlyPaths = nil
				m.Config.Agents[id] = a
			}
			output := filepath.Join(root, "out")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			type outcome struct {
				report Report
				err    error
			}
			done := make(chan outcome, 1)
			callbackErrors := make(chan error, 1)
			go func() {
				report, err := Run(ctx, m, RunOptions{OutputDir: output, Executable: binary, OnLive: func(state string) {
					if mode == "used-step" && strings.Contains(state, "run-00001") {
						_, err := ControlLive(ctx, state, "pause", "prepare final single phase", 1)
						if err != nil {
							callbackErrors <- err
						}
					}
				}})
				done <- outcome{report, err}
			}()
			wait := func(trial, phase, state string, allRunning bool) LiveView {
				t.Helper()
				for ctx.Err() == nil {
					select {
					case err := <-callbackErrors:
						t.Fatal(err)
					case ended := <-done:
						t.Fatalf("study ended before %s %s %s: %v", trial, phase, state, ended.err)
					default:
					}
					view, err := LiveStatus(ctx, output)
					if err == nil && view.Live && view.Research != nil {
						p := view.Research.Progress
						ready := p.Trial == trial && p.Phase == phase && p.State == state
						if allRunning {
							ready = ready && len(p.Tasks) == len(m.Config.Agents)
							for _, task := range p.Tasks {
								ready = ready && strings.HasPrefix(task, "running/")
							}
						}
						if ready {
							return view
						}
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal(ctx.Err())
				return LiveView{}
			}
			control := func(view LiveView, action string) {
				t.Helper()
				if _, err := ControlLive(ctx, view.Research.Progress.StateDir, action, "terminal admission regression", view.Research.Control.Version); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "used-step" {
				control(wait("run-00001", "coordinate", "paused", false), "step")
				control(wait("run-00001", "act", "paused", false), "step")
			}
			final := wait("run-00001", "act", "running", true)
			if mode != "used-step" {
				action := mode
				if mode == "unused-step" {
					action = "step"
				}
				control(final, action)
			}
			if mode != "stop" {
				phase := "coordinate"
				if mode == "unused-step" {
					phase = "act"
				}
				next := wait("run-00002", phase, "paused", false)
				if len(next.Research.Progress.TaskIDs) != 0 || next.Research.Progress.ModelTasksReserved != 0 {
					t.Fatalf("carried control dispatched or reserved the paused phase: %+v", next)
				}
				control(next, "stop")
			}
			ended := <-done
			if !errors.Is(ended.err, ErrResearchStopped) {
				t.Fatalf("stop did not stop study: %v", ended.err)
			}
			wantTrials := 2
			if mode == "stop" {
				wantTrials = 1
			}
			if len(ended.report.Results) != wantTrials {
				t.Fatalf("stop allowed another trial: got %d", len(ended.report.Results))
			}
			first := ended.report.Results[0]
			if len(first.Rounds) != 1 || len(first.Interventions) == 0 {
				t.Fatalf("terminal boundary discarded outcome/intervention evidence: %+v", first)
			}
			if mode != "stop" {
				if first.Status != "completed" {
					t.Fatalf("pause/step prevented completed trial checkpoint: %s", first.Status)
				}
				next := ended.report.Results[1]
				dispatched := 0
				for _, phase := range next.Phases {
					dispatched += len(phase.Tasks)
				}
				wantTasks := 0
				if mode == "unused-step" {
					wantTasks = len(m.Config.Agents)
				}
				if dispatched != wantTasks || len(next.Interventions) != 2 {
					t.Fatalf("carried intent released wrong work or lost audit: tasks=%d interventions=%v", dispatched, next.Interventions)
				}
			}
		})
	}
}
