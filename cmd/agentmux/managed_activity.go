package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
)

type managedActivitySnapshot struct {
	Messages        []managed.Message `json:"messages"`
	Tasks           []managed.Task    `json:"tasks"`
	Events          []managed.Event   `json:"events"`
	RuntimeDeadline string            `json:"runtime_deadline"`
}

// Every content field is quoted before terminal rendering. This escapes C0/C1
// controls, ESC/OSC sequences, newlines, and Unicode direction controls; output
// is bounded independently of the broker's JSON transport limits.
func activityText(value string, limit int) string {
	text := []rune(value)
	if len(text) > limit {
		value = string(text[:limit]) + "..."
	}
	return strconv.QuoteToASCII(value)
}

func formatManagedActivity(snapshot managedActivitySnapshot) string {
	var out strings.Builder
	deadline := activityText(snapshot.RuntimeDeadline, 80)
	if snapshot.RuntimeDeadline == "" {
		deadline = "unknown"
	} else if parsed, err := time.Parse(time.RFC3339Nano, snapshot.RuntimeDeadline); err == nil && parsed.IsZero() {
		deadline = "none (no automatic deadline)"
	}
	fmt.Fprintf(&out, "Owner activity (recent items, newest first)\nRun deadline: %s\n", deadline)
	out.WriteString("\nMESSAGES\n")
	for i, message := range snapshot.Messages {
		if i == 20 {
			break
		}
		fmt.Fprintf(&out, "%s %s from=%s to=%s board=%s %s\n", activityText(message.CreatedAt, 40), activityText(message.Kind, 30), activityText(message.From, 64), activityText(strings.Join(message.To, ","), 200), activityText(message.Board, 80), activityText(message.Text, 600))
	}
	out.WriteString("\nTASKS / RESULTS / FAILURES\n")
	for i, task := range snapshot.Tasks {
		if i == 20 {
			break
		}
		fmt.Fprintf(&out, "%s to=%s state=%s delivery=%s\n  instruction=%s\n", activityText(task.ID, 80), activityText(task.To, 64), activityText(task.State, 40), activityText(task.Delivery, 40), activityText(task.Text, 400))
		if task.Result != "" {
			fmt.Fprintf(&out, "  result=%s\n", activityText(task.Result, 1000))
		}
		if len(task.Usage) > 0 {
			fmt.Fprintf(&out, "  observed_usage=%s\n", activityText(string(task.Usage), 600))
		}
	}
	out.WriteString("\nEVENTS\n")
	for i, event := range snapshot.Events {
		if i == 20 {
			break
		}
		fmt.Fprintf(&out, "%s %s actor=%s entity=%s\n", activityText(event.At, 40), activityText(event.Type, 60), activityText(event.Actor, 64), activityText(event.EntityID, 80))
	}
	out.WriteString("\nOwner-only content view. Usage is provider-reported, not a spending guarantee.\n")
	return out.String()
}

func watchManagedActivity(state string, out io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	for {
		var snapshot managedActivitySnapshot
		var counts map[string]any
		var problem *managed.Fault
		credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
		if err != nil {
			return managedCLIError(out, "activity", "", exitRuntime, err)
		}
		client := managed.NewClient(credential)
		queryCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		raw, problem := client.Call(queryCtx, managed.Request{Operation: "activity", Args: json.RawMessage(`{"limit":20}`)})
		cancel()
		if problem == nil {
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				return managedCLIError(out, "activity", "", exitRuntime, fmt.Errorf("invalid activity response"))
			}
			queryCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
			status, failure := client.Call(queryCtx, managed.Request{Operation: "status", Args: json.RawMessage(`{}`)})
			cancel()
			if failure == nil {
				_ = json.Unmarshal(status, &counts)
			}
		}
		var display strings.Builder
		if runtime, err := managedlaunch.Snapshot(state); err == nil {
			display.WriteString(managedlaunch.FormatStatus(runtime, counts))
		}
		if problem != nil {
			fmt.Fprintf(&display, "\nOwner activity unavailable: %s\n", activityText(problem.Code, 40))
		} else {
			display.WriteString("\n")
			display.WriteString(formatManagedActivity(snapshot))
		}
		if _, err := fmt.Fprint(out, "\x1b[H\x1b[2J", display.String()); err != nil {
			return exitRuntime
		}
		select {
		case <-ctx.Done():
			return exitSuccess
		case <-time.After(time.Second):
		}
	}
}
