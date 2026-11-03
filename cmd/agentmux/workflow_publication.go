package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"

	"agentmux/internal/workflow"
)

const maxCLIOutputBytes = 64 << 20

type cliPublicationSessionKey struct{}

// Isolated CLI output is deferred until completion. A child can select a new
// credential after earlier progress was prepared, so even banners must stay in
// bounded memory until the complete output and selection can be checked.
type cliPublicationSession struct {
	mu                   sync.Mutex
	out, diagnostics     io.Writer
	stdout, stderr       bytes.Buffer
	guards               []*workflow.CLIPublication
	cleanups             []func()
	active, blocked      bool
	structured, overflow bool
	count                int
	cancel               context.CancelFunc
}

type cliPublicationWriter struct {
	session    *cliPublicationSession
	diagnostic bool
}

func (w cliPublicationWriter) Write(data []byte) (int, error) {
	s := w.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		if w.diagnostic {
			return s.diagnostics.Write(data)
		}
		return s.out.Write(data)
	}
	if s.overflow || len(data) > maxCLIOutputBytes-s.count {
		s.overflow = true
		s.cancel()
		return 0, errors.New("isolated CLI output exceeds its bounded memory allowance")
	}
	s.count += len(data)
	if w.diagnostic {
		return s.stderr.Write(data)
	}
	return s.stdout.Write(data)
}

func (s *cliPublicationSession) selectPlan(ctx context.Context, plan workflow.Plan) (context.Context, error) {
	if plan.Isolation == nil {
		return ctx, nil
	}
	s.active = true
	prepared, guard, cleanup, err := workflow.PrepareCLIPublication(ctx, plan)
	s.guards = append(s.guards, guard)
	s.cleanups = append(s.cleanups, cleanup)
	if err != nil {
		s.blocked = true
	}
	return prepared, err
}

func runCLIPublicationSession(ctx context.Context, out, diagnostics io.Writer, structured bool, prepare func(context.Context, *cliPublicationSession) (context.Context, error), run func(context.Context, io.Writer, io.Writer) int) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &cliPublicationSession{out: out, diagnostics: diagnostics, structured: structured, cancel: cancel}
	ctx = context.WithValue(ctx, cliPublicationSessionKey{}, s)
	defer func() {
		for i := len(s.cleanups) - 1; i >= 0; i-- {
			s.cleanups[i]()
		}
	}()
	ctx, err := prepare(ctx, s)
	code := exitRuntime
	if err == nil {
		code = run(ctx, cliPublicationWriter{session: s}, cliPublicationWriter{session: s, diagnostic: true})
	}
	if !s.active {
		return code
	}
	if s.blocked {
		return exitRuntime
	}
	stdout, stderr := s.stdout.Bytes(), s.stderr.Bytes()
	if s.overflow {
		stdout = nil
		stderr = []byte("Isolated CLI output exceeded its bounded memory allowance; inspect retained run evidence.\n")
		code = exitRuntime
	}
	for _, guard := range s.guards {
		if err := guard.CheckOutput(stdout, s.structured); err != nil {
			stdout = nil
			stderr = append(stderr, []byte("CLI output was refused because it could publish selected credential bytes.\n")...)
			code = exitRuntime
		}
	}
	// Every guard checks the complete final diagnostics, including any refusal
	// text added above. No static diagnostic fallback bypasses this boundary.
	for _, guard := range s.guards {
		stderr = guard.Diagnostics(stderr)
	}
	for _, guard := range s.guards {
		if guard.CheckOutput(stderr, false) != nil {
			stderr = nil
		}
	}
	if len(stdout) != 0 {
		if n, err := out.Write(stdout); err != nil || n != len(stdout) {
			return exitRuntime
		}
	}
	if len(stderr) != 0 {
		if n, err := diagnostics.Write(stderr); err != nil || n != len(stderr) {
			return exitRuntime
		}
	}
	return code
}

func prepareFlowCLIPublication(ctx context.Context, s *cliPublicationSession, args []string) (context.Context, error) {
	if len(args) < 2 || strings.HasPrefix(args[1], "-") {
		return ctx, nil
	}
	data, err := workflow.ReadRegularFile(args[1], 32<<20)
	if err != nil {
		return ctx, nil // no readable plan selected an authentication capability
	}
	var plan workflow.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		if possibleIsolationSelection(data) {
			s.active, s.blocked = true, true
			return ctx, err
		}
		return ctx, nil
	}
	if possibleIsolationSelection(data) {
		// Duplicate or case-variant fields may hide an earlier explicit grant.
		// The ordinary strict decoder will reject them; publish nothing before
		// an unambiguous selected authentication policy can be inspected.
		if workflow.DecodeJSON(data, &workflow.Plan{}) != nil {
			s.active, s.blocked = true, true
			return ctx, errors.New("ambiguous isolated plan selection")
		}
	}
	return s.selectPlan(ctx, plan)
}

func possibleIsolationSelection(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if name, ok := token.(string); ok && strings.EqualFold(name, "isolation") {
			next, err := decoder.Token()
			if err != nil || next != nil {
				return true
			}
		}
	}
}

func prepareGuidedCLIPublication(ctx context.Context, s *cliPublicationSession, args []string) (context.Context, error) {
	before, _ := splitGuidedCheck(args)
	values := map[string][]string{}
	booleans := map[string]bool{"dry-run": true, "allow-models": true, "json": true}
	known := map[string]bool{}
	for _, name := range strings.Fields("repo output catalog timeout check-timeout attempts containment storage isolation network coder reviewer allow-origin read-only-path auth auth-provider") {
		known[name] = true
	}
	for i := 0; i < len(before); i++ {
		argument := before[i]
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			continue
		}
		name, value, inline := strings.Cut(strings.TrimLeft(argument, "-"), "=")
		if !inline && known[name] && i+1 < len(before) {
			i++
			value = before[i]
		}
		if booleans[name] {
			if name == "json" {
				if !inline {
					s.structured = true
				} else if mode, err := strconv.ParseBool(value); err == nil {
					s.structured = mode
				}
			}
			continue
		}
		values[name] = append(values[name], value)
	}
	if len(values["isolation"])+len(values["auth"])+len(values["auth-provider"]) == 0 {
		return ctx, nil
	}
	s.active = true
	last := func(name, fallback string) string {
		if selected := values[name]; len(selected) > 0 && selected[len(selected)-1] != "" {
			return selected[len(selected)-1]
		}
		return fallback
	}
	options, err := guidedIsolationOptions("bubblewrap", "none", nil, nil, values["auth"], values["auth-provider"])
	if err != nil {
		s.blocked = true
		return ctx, err
	}
	plan := workflow.Plan{Isolation: options, Candidates: []workflow.Candidate{{ID: "coder", Harness: last("coder", "codex")}, {ID: "reviewer", Harness: last("reviewer", "antigravity-bridge")}}}
	return s.selectPlan(ctx, plan)
}

func selectGuidedCLIOptions(ctx context.Context, options workflow.GuidedOptions) (context.Context, error) {
	s, _ := ctx.Value(cliPublicationSessionKey{}).(*cliPublicationSession)
	if s == nil || options.Isolation == nil {
		return ctx, nil
	}
	profile := func(role, name string, override *workflow.Candidate) workflow.Candidate {
		candidate := workflow.Candidate{ID: role, Harness: name}
		if override != nil {
			candidate = *override
			if candidate.ID == "" {
				candidate.ID = role
			}
		}
		return candidate
	}
	coder, reviewer := options.CoderHarness, options.ReviewerHarness
	if coder == "" {
		coder = "codex"
	}
	if reviewer == "" {
		reviewer = "antigravity-bridge"
	}
	return s.selectPlan(ctx, workflow.Plan{Isolation: options.Isolation, Candidates: []workflow.Candidate{profile("coder", coder, options.Coder), profile("reviewer", reviewer, options.Reviewer)}})
}
