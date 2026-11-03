package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"agentmux/internal/agentauth"
	"agentmux/internal/managedconfig"
)

// CLIPublication retains only explicitly selected credential values in memory.
// It also observes credentials selected by nested execution through its returned
// context. Callers must retain complete output until its final publication check;
// a later child selection can collide with bytes prepared earlier in the run.
type CLIPublication struct {
	mu          sync.Mutex
	values      map[string]bool
	bytes       int
	unavailable bool
	cancel      context.CancelFunc
}

type cliPublicationKey struct{}

const maxCLIPublicationCredentialBytes = 8 << 20

// PrepareCLIPublication reads selected authentication before CLI metadata or
// parser diagnostics can be published. It starts no process and persists no
// credential. A failed preparation requires silence: even a fixed error could
// equal a selected value that could not be inspected.
func PrepareCLIPublication(ctx context.Context, plan Plan) (context.Context, *CLIPublication, func(), error) {
	if plan.Isolation == nil {
		return ctx, nil, func() {}, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &CLIPublication{values: map[string]bool{}, cancel: cancel}
	ctx = context.WithValue(ctx, cliPublicationKey{}, p)
	ctx, plan, closeRegistry, err := registerIsolationEnvironment(ctx, plan)
	cleanup := func() {
		cancel()
		closeRegistry()
		p.mu.Lock()
		clear(p.values)
		p.bytes = 0
		p.unavailable = true
		p.mu.Unlock()
	}
	fail := func(err error) (context.Context, *CLIPublication, func(), error) {
		p.mu.Lock()
		p.unavailable = true
		p.mu.Unlock()
		return ctx, p, cleanup, err
	}
	if err != nil {
		return fail(err)
	}
	if len(plan.Isolation.Auth) > 16 {
		return fail(errors.New("selected authentication exceeds CLI publication bounds"))
	}
	candidates := map[string]Candidate{}
	for _, candidate := range plan.Candidates {
		if _, duplicate := candidates[candidate.ID]; duplicate {
			return fail(errors.New("selected authentication requires unambiguous candidates"))
		}
		candidates[candidate.ID] = candidate
	}
	registry, _ := ctx.Value(isolationCredentialsKey{}).(*isolationCredentials)
	for id, auth := range plan.Isolation.Auth {
		candidate, exists := candidates[id]
		if !exists {
			return fail(errors.New("selected authentication requires an explicit native candidate"))
		}
		prepared, err := agentauth.PrepareAnonymous(candidate.Harness, managedconfig.NativeAuth{File: auth.File, Provider: auth.Provider})
		if err != nil {
			return fail(err)
		}
		registry.mu.Lock()
		registry.values[id] = append(registry.values[id], prepared.Secrets...)
		registry.mu.Unlock()
		prepared.Close()
	}
	registry.mu.Lock()
	registry.authPending = false
	registry.mu.Unlock()
	selectedIsolationCredentials(ctx) // include every initial explicit selection
	p.mu.Lock()
	unavailable := p.unavailable
	p.mu.Unlock()
	if unavailable {
		return fail(errors.New("selected authentication exceeds CLI publication bounds"))
	}
	return ctx, p, cleanup, nil
}

func observeCLIPublication(ctx context.Context, values []string) {
	p, _ := ctx.Value(cliPublicationKey{}).(*CLIPublication)
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unavailable {
		return
	}
	for _, value := range values {
		if value == "" || p.values[value] {
			continue
		}
		if len(p.values) >= 16384 || len(value) > maxCLIPublicationCredentialBytes-p.bytes {
			p.unavailable = true
			p.cancel()
			return
		}
		p.values[value] = true
		p.bytes += len(value)
	}
}

func (p *CLIPublication) credentialContext() (context.Context, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unavailable {
		return nil, false
	}
	values := make([]string, 0, len(p.values))
	for value := range p.values {
		values = append(values, value)
	}
	// The checking context deliberately has no observer; checking cannot recurse
	// into collection or modify the selection being checked.
	registry := &isolationCredentials{values: map[string][]string{"cli": values}}
	return context.WithValue(context.Background(), isolationCredentialsKey{}, registry), true
}

// CheckOutput checks the complete final bytes, including schema and formatting.
// JSON strings are also checked after decoding their escapes. A collision is a
// refusal, never a rewrite of protocol keys, booleans, numbers or null.
func (p *CLIPublication) CheckOutput(data []byte, structured bool) error {
	ctx, ready := p.credentialContext()
	if !ready || containsIsolationCredential(ctx, data) {
		return errors.New("CLI output could not be published safely")
	}
	if structured && len(data) != 0 {
		if !json.Valid(data) {
			return errors.New("CLI output is not one complete JSON value")
		}
		if _, changed, err := redactIsolationJSON(ctx, data); changed || err != nil {
			return errors.New("CLI output could not be published safely")
		}
	}
	return nil
}

// Diagnostics may lose credential-bearing text. The final byte check also
// covers replacements joining fragments into a selected literal.
func (p *CLIPublication) Diagnostics(data []byte) []byte {
	ctx, ready := p.credentialContext()
	if !ready {
		return nil
	}
	clean := []byte(redactIsolationText(selectedIsolationCredentials(ctx), string(data)))
	if containsIsolationCredential(ctx, clean) {
		return nil
	}
	return clean
}
