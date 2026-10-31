package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

const (
	maxPromptBytes = 1024 * 1024
	maxEventBytes  = 2 * 1024 * 1024
	maxOutputBytes = 16 * 1024 * 1024
	maxEventCount  = 10000
	maxStderrBytes = 64 * 1024
	shutdownGrace  = 2 * time.Second
	defaultTimeout = 10 * time.Minute
)

func run(ctx context.Context, req Request, spec Spec) (Result, error) {
	// A forgotten caller deadline must not leave an unattended model process
	// running forever. A shorter caller deadline always takes precedence.
	runCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, spec.Binary, spec.Args...)
	cmd.Dir, cmd.Env = spec.Workdir, spec.Env
	cmd.WaitDelay = shutdownGrace
	configureProcess(cmd)
	var stderr diagnosticBuffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	var stdin io.WriteCloser
	if spec.Name == "command" {
		cmd.Stdin = strings.NewReader(req.Prompt)
	} else {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return Result{}, err
		}
	}
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("start %s: %w", spec.Name, err)
	}
	if stdin != nil {
		defer stdin.Close()
		// Read stdout while delivering stdin so an initial event cannot deadlock
		// a prompt larger than the OS pipe buffer.
		go func() {
			_, writeErr := stdin.Write(promptInput(spec.Name, req.Prompt))
			if spec.Name != "pi" || writeErr != nil {
				_ = stdin.Close()
			}
		}()
	}
	state := streamState{name: spec.Name}
	var readErr error
	var shutdownTimer *time.Timer
	var forcedShutdown atomic.Bool
	if spec.Name == "command" {
		var output bytes.Buffer
		_, readErr = io.Copy(&output, io.LimitReader(stdout, maxOutputBytes+1))
		if output.Len() > maxOutputBytes {
			readErr = ErrOutputLimit
			output.Truncate(maxOutputBytes)
		}
		state.result.Text = output.String()
		state.complete = true
	} else {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), maxEventBytes)
		total := 0
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			total += len(scanner.Bytes()) + 1
			if total > maxOutputBytes || len(state.result.Events) >= maxEventCount {
				readErr = ErrOutputLimit
				break
			}
			if len(line) == 0 {
				continue
			}
			if err := state.consume(line); err != nil {
				readErr = err
				break
			}
			if spec.Name == "pi" && state.complete && shutdownTimer == nil {
				// RPC remains alive until stdin closes; agent_end alone is not
				// completion, because automatic retries may follow it.
				_ = stdin.Close()
				shutdownTimer = time.AfterFunc(shutdownGrace, func() {
					forcedShutdown.Store(true)
					cancel()
				})
			}
		}
		if err := scanner.Err(); err != nil && readErr == nil {
			readErr = fmt.Errorf("read %s JSON stream (event limit %d bytes): %w", spec.Name, maxEventBytes, err)
		}
	}
	if readErr != nil {
		cancel()
		if stdin != nil {
			_ = stdin.Close()
		}
	}
	waitErr := cmd.Wait()
	if shutdownTimer != nil {
		shutdownTimer.Stop()
	}
	if ctx.Err() != nil {
		return state.result, ctx.Err()
	}
	if readErr != nil {
		return state.result, readErr
	}
	if runCtx.Err() != nil && !forcedShutdown.Load() {
		return state.result, runCtx.Err()
	}
	if state.err != nil {
		return state.result, state.err
	}
	if state.denied || (spec.Name == "antigravity" && stderr.denied) {
		return state.result, fmt.Errorf("%w: %s (partial response retained)", ErrToolDenied, spec.Name)
	}
	if waitErr != nil {
		return state.result, fmt.Errorf("%s process failed: %w%s", spec.Name, waitErr, stderr.suffix())
	}
	if !state.complete || (spec.Name == "pi" && !state.accepted) {
		return state.result, fmt.Errorf("%w: %s%s", ErrIncomplete, spec.Name, stderr.suffix())
	}
	if forcedShutdown.Load() {
		return state.result, fmt.Errorf("%s did not shut down after its terminal result", spec.Name)
	}
	return state.result, nil
}

func promptInput(name, prompt string) []byte {
	var record any
	switch name {
	case "claude":
		record = map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}}
	case "antigravity":
		record = map[string]any{"event": "user", "message": map[string]any{"content": prompt}}
	case "pi":
		record = map[string]any{"id": "agentmux-prompt", "type": "prompt", "message": prompt}
	default:
		return []byte(prompt)
	}
	data, _ := json.Marshal(record)
	if name == "pi" {
		return append([]byte("{\"id\":\"agentmux-state\",\"type\":\"get_state\"}\n"), append(data, '\n')...)
	}
	return append(data, '\n')
}

// Diagnostics are bounded independently of stdout. Detection is performed as
// bytes arrive, so a later flood cannot hide a soft denial by truncating it.
type diagnosticBuffer struct {
	data   []byte
	tail   string
	denied bool
}

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	combined := b.tail + strings.ToLower(string(p))
	if permissionDenial(combined) {
		b.denied = true
	}
	if len(combined) > 128 {
		b.tail = combined[len(combined)-128:]
	} else {
		b.tail = combined
	}
	b.data = append(b.data, p...)
	if len(b.data) > maxStderrBytes {
		b.data = append([]byte(nil), b.data[len(b.data)-maxStderrBytes:]...)
	}
	return len(p), nil
}

func (b *diagnosticBuffer) suffix() string {
	message := strings.TrimSpace(string(b.data))
	if message == "" {
		return ""
	}
	return ": " + message
}

func permissionDenial(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "soft-denied") || strings.Contains(text, "soft denied") || strings.Contains(text, "permission denied") || strings.Contains(text, "permission_denied") || strings.Contains(text, "permissiondenied") || strings.Contains(text, "requires approval") || strings.Contains(text, "approval required")
}

var _ io.Writer = (*diagnosticBuffer)(nil)
