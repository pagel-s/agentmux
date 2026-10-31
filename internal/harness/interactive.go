package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
)

// BuildInteractive preserves a native terminal interface and native approval
// prompts. It never converts messages or broker tasks into terminal keystrokes.
func BuildInteractive(req Request) (Spec, error) {
	if req.Conversation != nil {
		return Spec{}, errors.New("interactive sessions use native history, not managed conversation leases")
	}
	spec, err := Build(req)
	if err != nil {
		return spec, err
	}
	if req.InheritEnvironment {
		overrides := map[string]string{}
		for _, entry := range spec.Env {
			key, value, _ := strings.Cut(entry, "=")
			overrides[key] = value
		}
		spec.Env = mergeEnv(os.Environ(), overrides)
	}
	if spec.Name == "command" {
		return spec, nil
	}
	prefix := []string{}
	if len(req.Command) > 0 {
		prefix = append(prefix, req.Command[1:]...)
	}
	spec.Args = prefix
	switch spec.Name {
	case "claude":
		spec.Args = append(spec.Args, "--append-system-prompt", req.Prompt)
	case "codex":
		instructions, _ := json.Marshal(req.Prompt) // JSON strings are valid TOML basic strings.
		spec.Args = append(spec.Args, "--no-daemon", "--no-alt-screen")
		if req.ExternalSandbox {
			spec.Args = append(spec.Args, "--sandbox", "danger-full-access")
		}
		spec.Args = append(spec.Args, "-c", "developer_instructions="+string(instructions))
	case "pi":
		spec.Args = append(spec.Args, "--append-system-prompt", req.Prompt)
		if req.BaseURL != "" {
			spec.Args = append(spec.Args, "--provider", "agentmux")
		}
		if req.ModelMetadata.Tools != nil && !*req.ModelMetadata.Tools {
			spec.Args = append(spec.Args, "--no-tools")
		}
	default:
		return Spec{}, errors.New("native interactive role injection is unsupported for this harness; use Pi")
	}
	if req.Model != "" {
		spec.Args = append(spec.Args, "--model", req.Model)
	}
	return spec, nil
}

// RunInteractive connects the native CLI directly to the tmux PTY. Endpoint
// configuration and sandbox wrapping retain their usual explicit boundaries.
func RunInteractive(ctx context.Context, req Request, in io.Reader, out, diagnostics io.Writer) error {
	spec, err := BuildInteractive(req)
	if err != nil {
		return err
	}
	cleanup, err := preparePiEndpoint(req, &spec)
	if err != nil {
		return err
	}
	defer cleanup()
	if req.Wrap != nil {
		spec, err = req.Wrap(spec)
		if err != nil {
			return err
		}
	}
	cmd := exec.CommandContext(ctx, spec.Binary, spec.Args...)
	cmd.Dir, cmd.Env = spec.Workdir, spec.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, diagnostics
	// Keep the inherited PTY; no pipes, JSON protocol, prompt submission, or
	// process-group changes that detach the native terminal interface.
	return cmd.Run()
}
