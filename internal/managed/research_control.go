package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

// ResearchProgress is owner-only execution metadata. It deliberately excludes
// observations, instructions, decisions, and environment ground truth.
type ResearchProgress struct {
	Trial              string            `json:"trial"`
	Attempt            int               `json:"attempt"`
	Condition          string            `json:"condition"`
	Seed               int64             `json:"seed"`
	StateDir           string            `json:"state_dir"`
	Round              int               `json:"round"`
	Phase              string            `json:"phase"`
	State              string            `json:"state"`
	Deadline           string            `json:"deadline"`
	Population         []string          `json:"population"`
	Tasks              map[string]string `json:"tasks"`
	TaskIDs            map[string]string `json:"task_ids,omitempty"`
	ModelTasksReserved int               `json:"model_tasks_reserved"`
	ModelTasksLimit    int               `json:"model_tasks_limit"`
	AckVersion         int               `json:"ack_version"`
	Revision           int               `json:"revision"`
	UpdatedAt          string            `json:"updated_at"`
}

type ResearchIntervention struct {
	Version int    `json:"version"`
	Action  string `json:"action"`
	Reason  string `json:"reason"`
	At      string `json:"at"`
	Actor   string `json:"actor"`
	Round   int    `json:"round"`
	Phase   string `json:"phase"`
}

type ResearchControl struct {
	Version       int                    `json:"version"`
	Mode          string                 `json:"mode"`
	Interventions []ResearchIntervention `json:"interventions"`
}

type ResearchStatus struct {
	Progress          ResearchProgress `json:"progress"`
	Control           ResearchControl  `json:"control"`
	OwnerMutations    int              `json:"owner_mutations"`
	RunnerHeartbeatAt string           `json:"runner_heartbeat_at,omitempty"`
}

func loadResearch(ctx context.Context, q queryer) (*ResearchStatus, error) {
	var raw json.RawMessage
	err := get(ctx, q, "research", "research_live", &raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if e := checkStateRecoveryDigest(ctx, q, "research", "live", raw); e != nil {
		return nil, e
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	var status ResearchStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, err
	}
	if status.Progress.Revision < 1 || status.Control.Version < 1 || status.Progress.AckVersion > status.Control.Version {
		return nil, errors.New("invalid research lifecycle state")
	}
	switch status.Control.Mode {
	case "running", "paused", "step", "stopped":
	default:
		return nil, errors.New("invalid research control mode")
	}
	status.RunnerHeartbeatAt = ""
	return &status, nil
}

func (s *Store) researchControl(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	if !a.owner() {
		return nil, forbidden()
	}
	status, err := loadResearch(ctx, q)
	if err != nil {
		return nil, internal(err)
	}
	// Heartbeats are ephemeral liveness observations, not durable control
	// mutations. Keeping them out of receipt storage avoids spending the task
	// budget while a study is paused. A broker restart resets this to unknown.
	if r.Operation == "research.heartbeat" {
		if !a.researchRunner {
			return nil, forbidden()
		}
		var in struct{}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		if !validKey(r.RequestID) {
			return nil, fault("invalid", "heartbeat requires a request_id")
		}
		if status == nil {
			return nil, fault("not_found", "no research runner is registered")
		}
		s.researchHeartbeatAt = now()
		return map[string]any{"runner_heartbeat_at": s.researchHeartbeatAt}, nil
	}
	if r.Operation == "research.status" {
		var in struct{}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		if status != nil {
			if err := refreshResearchTasks(ctx, q, status); err != nil {
				return nil, internal(err)
			}
		}
		if status != nil {
			status.RunnerHeartbeatAt = s.researchHeartbeatAt
		}
		return map[string]any{"research": status}, nil
	}
	if r.Operation == "research.publish" {
		if !a.researchRunner {
			return nil, forbidden()
		}
		var p ResearchProgress
		if err := strictJSON(r.Args, &p); err != nil {
			return nil, invalid(err)
		}
		if p.Trial == "" || p.Attempt < 1 || len(p.Population) > 16 || len(p.Tasks) > 16 || len(p.TaskIDs) > 16 || p.Round < 0 || p.Revision < 1 || p.AckVersion < 1 || p.ModelTasksReserved < 0 || p.ModelTasksLimit < 0 {
			return nil, fault("invalid", "invalid bounded research progress")
		}
		switch p.State {
		case "starting", "barrier", "paused", "running", "completed", "interrupted", "stopped":
		default:
			return nil, fault("invalid", "invalid research state")
		}
		if status == nil {
			if p.Revision != 1 || p.AckVersion != 1 {
				return nil, fault("conflict", "research must initialize at revision one")
			}
			status = &ResearchStatus{Control: ResearchControl{Version: 1, Mode: "running", Interventions: []ResearchIntervention{}}}
		} else if p.Trial != status.Progress.Trial || p.Attempt != status.Progress.Attempt || p.Revision != status.Progress.Revision+1 || p.AckVersion > status.Control.Version || p.AckVersion < status.Progress.AckVersion {
			return nil, fault("conflict", "research progress changed; reload before publishing")
		}
		if p.State == "running" && status.Progress.State != "running" {
			if p.AckVersion != status.Control.Version || (status.Control.Mode != "running" && status.Control.Mode != "step") {
				return nil, fault("conflict", "research phase admission requires the current running or step control version")
			}
		}
		if status.Progress.Revision > 0 {
			switch status.Progress.State {
			case "completed", "interrupted", "stopped":
				if p.State != status.Progress.State {
					return nil, fault("conflict", "terminal research progress cannot resume")
				}
			}
			if p.Round < status.Progress.Round || p.ModelTasksLimit != status.Progress.ModelTasksLimit || p.ModelTasksReserved < status.Progress.ModelTasksReserved {
				return nil, fault("conflict", "research progress cannot reset rounds or budgets")
			}
		}
		p.UpdatedAt = now()
		status.Progress = p
	} else {
		var in struct {
			Version int    `json:"version"`
			Action  string `json:"action"`
			Reason  string `json:"reason"`
		}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		if status == nil {
			return nil, fault("not_found", "no live research runner is registered")
		}
		if in.Version != status.Control.Version {
			return nil, fault("conflict", "research control changed; reload before intervening")
		}
		if status.Control.Mode == "stopped" || status.Progress.State == "completed" || status.Progress.State == "interrupted" || status.Progress.State == "stopped" {
			return nil, fault("conflict", "research attempt is terminal")
		}
		if strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 512 || strings.IndexFunc(in.Reason, unicode.IsControl) >= 0 {
			return nil, fault("invalid", "interventions require a printable reason of 1–512 bytes")
		}
		mode := map[string]string{"pause": "paused", "resume": "running", "step": "step", "stop": "stopped"}[in.Action]
		if mode == "" {
			return nil, fault("invalid", "research action must be pause, resume, step, or stop")
		}
		if len(status.Control.Interventions) >= 128 && in.Action != "stop" {
			return nil, fault("limit", "research intervention limit reached; stop the runner with its process context")
		}
		status.Control.Version++
		status.Control.Mode = mode
		intervention := ResearchIntervention{Version: status.Control.Version, Action: in.Action, Reason: in.Reason, At: now(), Actor: a.ID, Round: status.Progress.Round, Phase: status.Progress.Phase}
		status.Control.Interventions = append(status.Control.Interventions, intervention)
		if err := event(ctx, q, a, "research.intervention", status.Progress.Trial, map[string]any{"intervention": intervention, "effective_at": "next phase barrier"}); err != nil {
			return nil, internal(err)
		}
	}
	if err := refreshResearchTasks(ctx, q, status); err != nil {
		return nil, internal(err)
	}
	if err := save(ctx, q, "research", "research_live", ownerID, "", "owner", status); err != nil {
		return nil, internal(err)
	}
	if err := saveStateRecoveryDigest(ctx, q, "research", "live", status); err != nil {
		return nil, internal(err)
	}
	status.RunnerHeartbeatAt = s.researchHeartbeatAt
	return map[string]any{"research": status}, nil
}

// InitResearchRunner mints a distinct scheduling capability before the broker
// starts. It supplements the owner credential; agents cannot use it, and only a
// hash is persisted. It is never a participant role or proof of a hostile owner.
func (s *Store) InitResearchRunner() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	proof := randomID("runner")
	_, err := s.db.Exec("INSERT INTO metadata(key,value) VALUES('research_runner_hash',?)", hash(proof))
	if err != nil {
		return "", err
	}
	return proof, nil
}
func (s *Store) authenticateResearchRunner(ctx context.Context, q queryer, a *actor, proof string) *Fault {
	if proof == "" {
		return nil
	}
	if !a.owner() || len(proof) > 256 {
		return forbidden()
	}
	var expected string
	if err := q.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='research_runner_hash'").Scan(&expected); err != nil || expected != hash(proof) {
		return forbidden()
	}
	a.researchRunner = true
	return nil
}
func recordResearchOwnerMutation(ctx context.Context, q queryer, a actor, r Request) error {
	status, err := loadResearch(ctx, q)
	if err != nil || status == nil {
		return err
	}
	switch status.Progress.State {
	case "completed", "interrupted", "stopped":
		return nil
	}
	status.OwnerMutations++
	if err := save(ctx, q, "research", "research_live", ownerID, "", "owner", status); err != nil {
		return err
	}
	if err := saveStateRecoveryDigest(ctx, q, "research", "live", status); err != nil {
		return err
	}
	return event(ctx, q, a, "research.owner_mutation", status.Progress.Trial, map[string]any{"operation": r.Operation, "request_id": r.RequestID, "round": status.Progress.Round, "phase": status.Progress.Phase})
}

func refreshResearchTasks(ctx context.Context, q queryer, status *ResearchStatus) error {
	if status.Progress.Tasks == nil {
		status.Progress.Tasks = map[string]string{}
	}
	for id, taskID := range status.Progress.TaskIDs {
		task, err := loadTask(ctx, q, taskID)
		if err != nil || task.To != id {
			return errors.New("research task progress is unavailable")
		}
		status.Progress.Tasks[id] = task.State + "/" + task.Delivery
	}
	return nil
}

// ResearchSnapshot is an in-process owner evidence read, like Export. It does
// not advance controls, issue task claims, or renew any budget.
func (s *Store) ResearchSnapshot(ctx context.Context) (*ResearchStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("store is closed")
	}
	return loadResearch(ctx, s.db)
}
