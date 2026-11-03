package workflow

import (
	"context"
	"errors"
	"os/exec"
	"runtime"

	"agentmux/internal/harness"
	"agentmux/internal/sandbox"
)

const (
	ProcessGroups = "process-group"
	PIDNamespace  = "pid-namespace"
)

func normalizedContainment(policy string) string {
	if policy == "" {
		return ProcessGroups
	}
	return policy
}

// ValidateProcessContainment is read-only: preview validates the chosen policy,
// while execution separately probes the actual requested kernel boundary.
func ValidateProcessContainment(policy string) error {
	switch normalizedContainment(policy) {
	case ProcessGroups:
		return nil
	case PIDNamespace:
		if runtime.GOOS != "linux" {
			return errors.New("pid-namespace containment requires Linux and Bubblewrap")
		}
		return nil
	default:
		return errors.New("process containment must be process-group or pid-namespace")
	}
}

type processContainmentContextKey struct{}

// PrepareProcessContainment probes a requested boundary before any work starts
// and returns a context carrying that verified policy through Git fingerprints,
// provider turns and acceptance checks. An already prepared context avoids
// repeated probes; a nested operation cannot weaken its parent's policy.
func PrepareProcessContainment(ctx context.Context, policy string) (context.Context, error) {
	if err := ValidateProcessContainment(policy); err != nil {
		return ctx, err
	}
	policy = normalizedContainment(policy)
	if prepared, _ := ctx.Value(processContainmentContextKey{}).(string); prepared == PIDNamespace || prepared == policy {
		return ctx, nil
	}
	if policy == PIDNamespace {
		if err := sandbox.ProbeProcessTree(ctx); err != nil {
			return ctx, err
		}
	}
	return context.WithValue(ctx, processContainmentContextKey{}, policy), nil
}

func contextProcessContainment(ctx context.Context) string {
	policy, _ := ctx.Value(processContainmentContextKey{}).(string)
	return normalizedContainment(policy)
}

// Both controller Git and child tools may execute owner-configured helpers.
// Apply the same requested lifetime boundary to those Git processes too.
func wrapGitProcess(ctx context.Context, cmd *exec.Cmd) error {
	if IsolationMode(ctx) == "bubblewrap" {
		return WrapIsolatedGit(ctx, cmd)
	}
	wrap := processContainmentWrapper(contextProcessContainment(ctx))
	if wrap == nil {
		return nil
	}
	if len(cmd.ExtraFiles) > 1 {
		return errors.New("PID-contained Git supports one explicit application lifetime descriptor")
	}
	spec, err := wrap(harness.Spec{Name: "command", Binary: cmd.Path, Args: append([]string(nil), cmd.Args[1:]...), Env: cmd.Env, Workdir: cmd.Dir})
	if err != nil {
		return err
	}
	if len(cmd.ExtraFiles) == 1 {
		// Bubblewrap closes unrelated inherited descriptors. Its monitor must
		// retain the admitted writer's flock lease until the PID namespace has
		// finished; otherwise owner death could admit recovery too early.
		spec.Args = append([]string{"--sync-fd", "3"}, spec.Args...)
	}
	cmd.Path, cmd.Args, cmd.Env, cmd.Dir = spec.Binary, append([]string{spec.Binary}, spec.Args...), spec.Env, spec.Workdir
	return nil
}

func processContainmentWrapper(policy string) func(harness.Spec) (harness.Spec, error) {
	if normalizedContainment(policy) == PIDNamespace {
		return sandbox.WrapProcessTree
	}
	return nil
}
