package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"path/filepath"

	"agentmux/internal/managedconfig"
)

var errOperationalQueryBudget = errors.New("operational observation query budget exhausted")

func operationalQueryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, operationalObservationReadBudget, errOperationalQueryBudget)
}

func operationalReason(problem *Fault, reason string) *Fault {
	problem.ReasonCode = reason
	return problem
}

// Only the body-free operational projections may run outside the authority
// lock. Decode routing with the same encoding/json field selection as native;
// the normal handler still authenticates and strictly validates every argument.
func operationalRead(r Request) bool {
	if r.Operation == "observation.get" {
		return true
	}
	if r.Operation != "native.status" {
		return false
	}
	var in struct {
		Summary bool `json:"summary"`
	}
	return json.Unmarshal(r.Args, &in) == nil && in.Summary
}

// SQL cancellation can take longer than its context budget, particularly under
// race instrumentation. A separate read-only connection, not a short timeout
// on the authority connection, keeps that cleanup away from lease monitors.
// One slot bounds both in-flight projections and SQLite connections.
func (s *Store) operationalReader() (*Store, *sql.DB, chan struct{}, context.Context, *Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, nil, nil, operationalReason(fault("unavailable", "store is closed"), "shutdown")
	}
	if s.observationDB == nil {
		u := url.URL{Scheme: "file", Path: filepath.Join(s.dir, "state.sqlite")}
		query := u.Query()
		query.Set("mode", "ro")
		query.Add("_pragma", "query_only(1)")
		query.Add("_pragma", "busy_timeout(50)")
		u.RawQuery = query.Encode()
		db, err := sql.Open("sqlite", u.String())
		if err != nil {
			return nil, nil, nil, nil, internal(err)
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		s.observationDB, s.observationSlot = db, make(chan struct{}, 1)
		s.observationContext, s.observationCancel = context.WithCancel(context.Background())
	}
	// Copy only operational routing and immutable value metadata. The shadow
	// may prune its progress copy; it never changes the live Store's maps.
	view := &Store{cfg: managedconfig.Config{Limits: s.cfg.Limits, Agents: map[string]managedconfig.Agent{}},
		taskProgress: maps.Clone(s.taskProgress), nativePresence: maps.Clone(s.nativePresence)}
	for id, agent := range s.cfg.Agents {
		view.cfg.Agents[id] = managedconfig.Agent{Harness: agent.Harness, Interaction: agent.Interaction, NativeOnly: agent.NativeOnly}
	}
	return view, s.observationDB, s.observationSlot, s.observationContext, nil
}

func (s *Store) callOperationalRead(ctx context.Context, token string, r Request) (json.RawMessage, *Fault) {
	view, db, slot, lifetime, problem := s.operationalReader()
	if problem != nil {
		return nil, problem
	}
	select {
	case slot <- struct{}{}:
		defer func() { <-slot }()
	default:
		return nil, operationalReason(fault("unavailable", "an operational observation is already running; refresh after it finishes"), "operational_read_busy")
	}
	ctx, cancel := operationalQueryContext(ctx)
	defer cancel()
	stop := context.AfterFunc(lifetime, cancel)
	defer stop()
	connection, err := db.Conn(ctx)
	if err != nil {
		return nil, operationalReadFault(ctx, lifetime, err)
	}
	defer connection.Close()
	tx, err := connection.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, operationalReadFault(ctx, lifetime, err)
	}
	defer tx.Rollback()
	a, err := authenticateWith(ctx, tx, token)
	if err != nil {
		if ctx.Err() != nil || lifetime.Err() != nil {
			return nil, operationalReadFault(ctx, lifetime, err)
		}
		return nil, fault("unauthorized", "invalid credential")
	}
	if len(r.Args) == 0 {
		r.Args = json.RawMessage(`{}`)
	}
	if len(r.Args) > 2*view.cfg.Limits.MaxMessageBytes+32768 {
		return nil, fault("invalid", "request arguments exceed limit")
	}
	if problem := view.authenticateResearchRunner(ctx, tx, &a, r.ResearchRunnerProof); problem != nil {
		if ctx.Err() != nil || lifetime.Err() != nil {
			return nil, operationalReadFault(ctx, lifetime, ctx.Err())
		}
		return nil, problem
	}
	result, problem := view.dispatch(ctx, tx, a, r)
	if ctx.Err() != nil || lifetime.Err() != nil {
		return nil, operationalReadFault(ctx, lifetime, ctx.Err())
	}
	if problem != nil {
		return nil, problem
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, internal(err)
	}
	// End the consistent read snapshot before checking current authorization.
	// An owner token rotated while this read ran cannot publish its old sample.
	if err := tx.Rollback(); err != nil {
		return nil, operationalReadFault(ctx, lifetime, err)
	}
	if problem := currentOperationalCredential(ctx, connection, token, a); problem != nil {
		if ctx.Err() != nil || lifetime.Err() != nil {
			return nil, operationalReadFault(ctx, lifetime, ctx.Err())
		}
		return nil, problem
	}
	if ctx.Err() != nil || lifetime.Err() != nil {
		return nil, operationalReadFault(ctx, lifetime, ctx.Err())
	}
	return data, nil
}

func operationalReadFault(ctx, lifetime context.Context, err error) *Fault {
	if ctx.Err() != nil {
		problem := operationalQueryFault(ctx, ctx.Err())
		if lifetime.Err() != nil {
			return operationalReason(problem, "shutdown")
		}
		return problem
	}
	if lifetime.Err() != nil {
		return operationalReason(internal(lifetime.Err()), "shutdown")
	}
	return internal(err)
}

func operationalQueryFault(ctx context.Context, err error) *Fault {
	problem := internal(err)
	if problem.Code == "cancelled" && errors.Is(context.Cause(ctx), errOperationalQueryBudget) {
		problem.ReasonCode = "query_budget_exhausted"
	}
	return problem
}

func currentOperationalCredential(ctx context.Context, q queryer, token string, prior actor) *Fault {
	current, err := authenticateWith(ctx, q, token)
	if ctx.Err() != nil {
		return internal(ctx.Err())
	}
	if err != nil || current.ID != prior.ID || current.Instance != prior.Instance || current.Generation != prior.Generation {
		return fault("unauthorized", "credential changed during observation")
	}
	return nil
}
