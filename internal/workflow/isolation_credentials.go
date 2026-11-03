package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"sync"

	"agentmux/internal/agentauth"
	"agentmux/internal/managedconfig"
)

type isolationCredentialsKey struct{}

// Selected credentials are loaded before creating run evidence or importing
// source objects. They never leave memory here. The digest pins a filtered auth
// snapshot, so a later credential-file change cannot introduce an unscanned key.
type isolationCredentials struct {
	mu     sync.Mutex
	values map[string][]string
	auth   map[string]string
	// Until every explicitly selected native auth file has been inspected,
	// even a fixed diagnostic could equal an as-yet unknown short credential.
	authPending bool
}

// registerIsolationEnvironment pins the explicitly selected environment values
// before validation can return a diagnostic containing owner-supplied metadata.
// It does not read authentication files, probe containment, or execute Git.
func registerIsolationEnvironment(ctx context.Context, p Plan) (context.Context, Plan, func(), error) {
	if p.Isolation == nil && isolationFromContext(ctx) == nil {
		return ctx, p, func() {}, nil
	}
	// Registration precedes ValidatePlan, so enforce its credential input bounds
	// independently. Before registration no nonempty diagnostic is safe for
	// every possible selected credential (including a one-character value).
	// Keep the refusal nonnil without publishing unchecked diagnostic bytes.
	if len(p.Candidates) > 16 {
		return ctx, p, func() {}, errors.New("")
	}
	for _, candidate := range p.Candidates {
		if len(candidate.Env) > 256 || len(candidate.APIKeyEnv) > 128 {
			return ctx, p, func() {}, errors.New("")
		}
		for name, value := range candidate.Env {
			if len(name) > 128 || len(value) > 128<<10 {
				return ctx, p, func() {}, errors.New("")
			}
		}
	}
	registry := &isolationCredentials{values: map[string][]string{}, auth: map[string]string{}}
	registry.authPending = p.Isolation != nil && len(p.Isolation.Auth) != 0
	if prior, _ := ctx.Value(isolationCredentialsKey{}).(*isolationCredentials); prior != nil {
		prior.mu.Lock()
		for id, values := range prior.values {
			registry.values[id] = append([]string(nil), values...)
		}
		prior.mu.Unlock()
	}
	cleanup := func() { registry.mu.Lock(); clear(registry.values); clear(registry.auth); registry.mu.Unlock() }
	p.Candidates = append([]Candidate(nil), p.Candidates...)
	ctx = context.WithValue(ctx, isolationCredentialsKey{}, registry)
	// Register every bounded environment selection before availability errors
	// from one candidate can disclose another candidate's selected value.
	for i := range p.Candidates {
		candidate := &p.Candidates[i]
		environment := make(map[string]string, len(candidate.Env)+1)
		for name, value := range candidate.Env {
			environment[name] = value
			if isolationCredentialEnvironment(name) && value != "" {
				registry.values[candidate.ID] = append(registry.values[candidate.ID], value)
			}
		}
		if candidate.APIKeyEnv != "" {
			value, exists := candidate.Env[candidate.APIKeyEnv]
			if !exists {
				value = os.Getenv(candidate.APIKeyEnv)
			}
			environment[candidate.APIKeyEnv] = value
			if value != "" {
				registry.values[candidate.ID] = append(registry.values[candidate.ID], value)
			}
		}
		candidate.Env = environment
	}
	for i := range p.Candidates {
		candidate := &p.Candidates[i]
		if candidate.APIKeyEnv != "" {
			value := candidate.Env[candidate.APIKeyEnv]
			if value == "" {
				return ctx, p, cleanup, redactEarlyIsolationError(ctx, errors.New("selected credential environment is unavailable"))
			}
			if len(value) > 128<<10 {
				return ctx, p, cleanup, redactEarlyIsolationError(ctx, errors.New("selected credential preflight exceeds plan bounds"))
			}
		}
	}
	return ctx, p, cleanup, nil
}

func prepareIsolationCredentials(ctx context.Context, p Plan) (context.Context, Plan, func(), error) {
	ctx, p, cleanup, err := registerIsolationEnvironment(ctx, p)
	if err != nil {
		return ctx, p, cleanup, err
	}
	registry, _ := ctx.Value(isolationCredentialsKey{}).(*isolationCredentials)
	if registry == nil {
		return ctx, p, cleanup, nil
	}
	options := p.Isolation
	if options == nil {
		if inherited := isolationFromContext(ctx); inherited != nil {
			options = &inherited.opts
		}
	}
	if options != nil && len(options.Auth) > 16 {
		return ctx, p, cleanup, errors.New("selected credential preflight exceeds plan bounds")
	}
	for i := range p.Candidates {
		candidate := &p.Candidates[i]
		if options != nil {
			if auth, ok := options.Auth[candidate.ID]; ok {
				prepared, err := agentauth.PrepareAnonymous(candidate.Harness, managedconfig.NativeAuth{File: auth.File, Provider: auth.Provider})
				if err != nil {
					return ctx, p, cleanup, errors.New("selected native authentication preflight failed")
				}
				digest, err := isolationAuthDigest(prepared)
				if err != nil {
					prepared.Close()
					return ctx, p, cleanup, err
				}
				registry.values[candidate.ID] = append(registry.values[candidate.ID], prepared.Secrets...)
				registry.auth[candidate.ID] = digest
				prepared.Close()
			}
		}
	}
	registry.mu.Lock()
	registry.authPending = false
	registry.mu.Unlock()
	// Retained owner metadata must not itself publish selected credentials.
	// Explicit environment values are deliberately omitted from saved plans.
	encoded, err := json.Marshal(redactedPlan(p))
	if err != nil {
		return ctx, p, cleanup, redactEarlyIsolationError(ctx, ErrIsolationCredentialLeak)
	}
	if _, changed, err := redactIsolationJSON(ctx, encoded); changed || err != nil {
		return ctx, p, cleanup, redactEarlyIsolationError(ctx, ErrIsolationCredentialLeak)
	}
	return ctx, p, cleanup, nil
}

func isolationAuthDigest(prepared *agentauth.Prepared) (string, error) {
	hash := sha256.New()
	for _, file := range prepared.Files {
		if file.Descriptor == nil {
			return "", errors.New("selected native authentication requires anonymous immutable data")
		}
		info, err := file.Descriptor.Stat()
		if err != nil || info.Size() < 0 || info.Size() > 1<<20 {
			return "", errors.New("selected native authentication exceeds its bound")
		}
		if _, err := io.Copy(hash, io.NewSectionReader(file.Descriptor, 0, info.Size())); err != nil {
			return "", errors.New("selected native authentication could not be verified")
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validatePinnedIsolationAuth(ctx context.Context, id string, prepared *agentauth.Prepared) error {
	registry, _ := ctx.Value(isolationCredentialsKey{}).(*isolationCredentials)
	if registry == nil {
		return nil
	}
	registry.mu.Lock()
	expected, selected := registry.auth[id]
	registry.mu.Unlock()
	if !selected {
		return errors.New("selected native authentication was not preflighted")
	}
	actual, err := isolationAuthDigest(prepared)
	if err != nil || actual != expected {
		return errors.New("selected native authentication changed after preflight")
	}
	return nil
}

func selectedIsolationCredentials(ctx context.Context) []string {
	seen := map[string]bool{}
	if registry, _ := ctx.Value(isolationCredentialsKey{}).(*isolationCredentials); registry != nil {
		registry.mu.Lock()
		for _, values := range registry.values {
			for _, value := range values {
				if value != "" {
					seen[value] = true
				}
			}
		}
		registry.mu.Unlock()
	}
	if runtime := isolationFromContext(ctx); runtime != nil {
		for _, value := range runtime.secretValues("") {
			if value != "" {
				seen[value] = true
			}
		}
	}
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) != len(values[j]) {
			return len(values[i]) > len(values[j])
		}
		return values[i] < values[j]
	})
	observeCLIPublication(ctx, values)
	return values
}

func containsIsolationCredential(ctx context.Context, data []byte) bool {
	for _, value := range selectedIsolationCredentials(ctx) {
		if bytes.Contains(data, []byte(value)) {
			return true
		}
	}
	return false
}

func redactEarlyIsolationError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if registry, _ := ctx.Value(isolationCredentialsKey{}).(*isolationCredentials); registry != nil {
		registry.mu.Lock()
		pending := registry.authPending
		registry.mu.Unlock()
		if pending {
			return &redactedIsolationError{original: err, message: ""}
		}
	}
	message := redactIsolationText(selectedIsolationCredentials(ctx), err.Error())
	if message == err.Error() {
		return err
	}
	return &redactedIsolationError{original: err, message: message}
}
