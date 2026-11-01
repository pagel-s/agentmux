package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentmux/internal/managed"
)

var ErrResearchStopped = errors.New("research stopped by owner at a phase barrier; evidence and budget retained")

type LiveView struct {
	Live     bool                    `json:"live"`
	Research *managed.ResearchStatus `json:"research"`
}

type liveRunner struct {
	call        rpc
	progress    managed.ResearchProgress
	path        string
	budget      *modelBudget
	stepVersion int
}

func (l *liveRunner) publish(ctx context.Context) (*managed.ResearchStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := l.progress
	p.Revision++
	p.ModelTasksLimit, p.ModelTasksReserved = l.budget.Limit, len(l.budget.Reserved)
	raw, err := l.call(ctx, "research.publish", p)
	if err != nil {
		return nil, liveContextError(ctx, err)
	}
	var response struct {
		Research *managed.ResearchStatus `json:"research"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Research == nil {
		return nil, errors.New("invalid research progress response")
	}
	l.progress = response.Research.Progress
	if err := writeJSON(l.path, LiveView{Live: false, Research: response.Research}); err != nil {
		return nil, err
	}
	return response.Research, nil
}

// barrier never extends the trial deadline or reserves work while paused.
// A step grants exactly one phase, regardless of the number of status reads.
func (l *liveRunner) barrier(ctx context.Context, round int, phase string, population []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.progress.Round, l.progress.Phase, l.progress.Population = round, phase, population
	l.progress.Tasks, l.progress.TaskIDs = map[string]string{}, nil
	l.progress.State = "barrier"
	if _, err := l.publish(ctx); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := l.call(ctx, "research.status", map[string]any{})
		if err != nil {
			return liveContextError(ctx, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var response struct {
			Research *managed.ResearchStatus `json:"research"`
		}
		if json.Unmarshal(raw, &response) != nil || response.Research == nil {
			return errors.New("research control state missing")
		}
		c := response.Research.Control
		l.progress.AckVersion = c.Version
		switch c.Mode {
		case "stopped":
			l.progress.State = "stopped"
			_, err := l.publish(ctx)
			if err != nil {
				return err
			}
			return ErrResearchStopped
		case "running":
			l.progress.State = "running"
			_, err := l.publish(ctx)
			if err == nil {
				// Publication may have committed as the deadline expired. Keep
				// its revision for cleanup, but never release new phase work.
				err = ctx.Err()
			}
			if researchAdmissionConflict(err) {
				l.progress.State = "barrier"
				continue
			}
			return err
		case "step":
			if l.stepVersion != c.Version {
				l.progress.State = "running"
				_, err := l.publish(ctx)
				if err == nil {
					err = ctx.Err()
				}
				if researchAdmissionConflict(err) {
					l.progress.State = "barrier"
					continue
				}
				if err == nil {
					l.stepVersion = c.Version
				}
				return err
			}
		}
		if l.progress.State != "paused" || response.Research.Progress.AckVersion != c.Version {
			l.progress.State = "paused"
			if _, err := l.publish(ctx); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// complete freezes terminal progress and the next attempt's admission intent
// in one broker transaction. There is no extra phase to release here: a used
// step carries a pause, while an unused step still grants exactly one phase.
// A final study trial can finish without waiting at a nonexistent barrier.
func (l *liveRunner) complete(ctx context.Context) (*managed.ResearchStatus, string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		raw, err := l.call(ctx, "research.status", map[string]any{})
		if err != nil {
			return nil, "", liveContextError(ctx, err)
		}
		var response struct {
			Research *managed.ResearchStatus `json:"research"`
		}
		if json.Unmarshal(raw, &response) != nil || response.Research == nil {
			return nil, "", errors.New("research control state missing at completion")
		}
		control := response.Research.Control
		next := control.Mode
		if next == "step" && l.stepVersion == control.Version {
			next = "paused"
		}
		l.progress.AckVersion = control.Version
		l.progress.State = "completed"
		if control.Mode == "stopped" {
			l.progress.State = "stopped"
		}
		status, err := l.publish(ctx)
		if researchAdmissionConflict(err) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if control.Mode == "stopped" {
			return status, "", ErrResearchStopped
		}
		return status, next, nil
	}
}

// Broker cancellation faults lose Go's context error identity across RPC. Only
// an actually ended caller context takes precedence over a transport fault;
// live-context broker errors still retain their code and are never retried here.
func liveContextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// ResolveLiveState accepts an explicit broker state or a study output directory.
// A pointer is metadata, never an instruction to read credentials outside its
// own private run/attempt tree.
func ResolveLiveState(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(managed.CredentialPath(abs, "")); err == nil {
		return abs, nil
	}
	var view LiveView
	if err := privateJSON(filepath.Join(abs, "live.json"), &view); err != nil {
		return "", err
	}
	if view.Research == nil {
		return "", errors.New("no research attempt has published progress")
	}
	state := filepath.Clean(view.Research.Progress.StateDir)
	rel, err := filepath.Rel(abs, state)
	parts := strings.Split(rel, string(filepath.Separator))
	if err != nil || !filepath.IsAbs(state) || len(parts) != 3 || !strings.HasPrefix(parts[0], "run-") || !strings.HasPrefix(parts[1], "attempt-") || parts[2] != "state" {
		return "", errors.New("research live pointer escapes its output")
	}
	current := abs
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return "", errors.New("research live state must remain in private directories without symlinks")
		}
	}
	return state, nil
}

func LiveStatus(ctx context.Context, path string) (LiveView, error) {
	state, err := ResolveLiveState(path)
	if err != nil {
		return LiveView{}, err
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		return LiveView{}, err
	}
	client := managed.NewClient(credential)
	raw, problem := client.Call(ctx, managed.Request{Operation: "research.status", Args: json.RawMessage(`{}`)})
	if problem == nil {
		var view LiveView
		if err := json.Unmarshal(raw, &view); err != nil {
			return view, err
		}
		view.Live = true
		return view, nil
	}
	// STATE and OUTPUT must reopen the same authenticated checkpoint. The
	// pointer selects an attempt; its cached metadata is not authoritative.
	status, problem := client.LocalResearchStatus(ctx, state)
	if problem != nil {
		return LiveView{}, problem
	}
	return LiveView{Live: false, Research: status}, nil
}

func ControlLive(ctx context.Context, path, action, reason string, version int) (LiveView, error) {
	state, err := ResolveLiveState(path)
	if err != nil {
		return LiveView{}, err
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		return LiveView{}, err
	}
	args, _ := json.Marshal(map[string]any{"version": version, "action": action, "reason": reason})
	raw, problem := managed.NewClient(credential).Call(ctx, managed.Request{Operation: "research.control", RequestID: fmt.Sprintf("intervene-%d", time.Now().UnixNano()), Args: args})
	if problem != nil {
		return LiveView{}, problem
	}
	var view LiveView
	if err := json.Unmarshal(raw, &view); err != nil {
		return view, err
	}
	view.Live = true
	return view, nil
}

// Only a definitive rejected transaction may be re-evaluated. Transport loss
// may have admitted the phase and must interrupt rather than replay a mutation.
func researchAdmissionConflict(err error) bool {
	var problem *managed.Fault
	return errors.As(err, &problem) && problem.Code == "conflict"
}
