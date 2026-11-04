package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
)

type workPairReview struct {
	request  managed.TaskPairRequest
	statuses []managed.NativeStatus
	labels   []string
	body     string
}

func (r workPairReview) args() map[string]any {
	return map[string]any{"version": r.request.Version, "mode": r.request.Mode, "agents": append([]managed.TaskPairAgent(nil), r.request.Agents...)}
}

// Mode changes only reformat the same frozen review; never resolve identities,
// refresh a policy or substitute a different queued request in an open modal.
func (r workPairReview) withMode(mode string) workPairReview {
	if mode != "review" && mode != "auto" {
		return r
	}
	r.request.Mode = mode
	var body strings.Builder
	fmt.Fprintf(&body, "Task pair: %s ↔ %s\nPermission revision: %d\n\nGrant Commands in BOTH directions: %s → %s and %s → %s.\nEnable %s delivery and returned results on BOTH native receivers.\n", r.labels[0], r.labels[1], r.request.Version, r.request.Agents[0].ID, r.request.Agents[1].ID, r.request.Agents[1].ID, r.request.Agents[0].ID, mode)
	if mode == "auto" {
		body.WriteString("\nAUTO CAN START EXISTING ELIGIBLE QUEUED ITEMS, including boundary notices.\n")
	} else {
		body.WriteString("\nReview keeps each eligible item queued until you approve its exact text.\n")
	}
	body.WriteString("Receiver modes apply to ALL their already-authorized senders and subscriptions, not only this pair. Queue counts below use CURRENT grants and subscriptions. Pair grants and returned results can make existing queued instructions/results newly eligible; the count can grow without a new arrival. Pairing also changes boundary context.\n\n")
	for i, status := range r.statuses {
		pending := "unknown"
		if nativePendingKnown(status) {
			pending = fmt.Sprint(max(status.PendingCount, len(status.Pending)))
			if status.PendingMore {
				pending += "+"
			}
		}
		fmt.Fprintf(&body, "%s [%s] · policy v%d · native session %s\n  Delivery %s → %s · returned results %s → on\n  Native turns %d/%d · max hops %d · cooldown %ds · known eligible queue %s\n  Adapter %s · connected %t · readiness %s\n  %s\n", r.labels[i], r.request.Agents[i].ID, status.Policy.Version, consoleContent(status.Session, 160), status.Policy.Mode, mode, nativeEnabled(status.Policy.Results), status.Turns, status.Policy.MaxTurns, status.Policy.MaxHops, status.Policy.CooldownSeconds, pending, consoleContent(status.Adapter, 80), status.Connected, consoleContent(status.State, 80), consoleContent(status.Reason, 500))
		if !status.Capabilities.PreservesDraft {
			body.WriteString("  Draft preservation is unverified; check this native terminal before enabling delivery.\n")
		}
		if !status.Capabilities.Cancellation {
			body.WriteString("  Native cancellation is unverified; queued turns may not be retractable.\n")
		}
		if status.Capabilities.CompletionEvidence == "receiver-ack" {
			body.WriteString("  Completion relies on the receiver's acknowledgment, not independently observed native completion.\n")
		}
		for j, item := range status.Pending {
			if j == 8 {
				body.WriteString("  More pending items follow; use delivery ID --json to inspect full text.\n")
				break
			}
			fmt.Fprintf(&body, "  Queued: %s from %s\n", consoleContent(item.Kind, 40), consoleContent(item.From, 100))
		}
		body.WriteByte('\n')
	}
	body.WriteString("Turn/hop limits, spent turns, cooldowns and other subscriptions stay saved.\nNotes, pings, shared boards and memory permissions are unchanged.\nDispatch still waits for a supported ready adapter; pairing is not delivery or verified completion.\nOne atomic change applies the exact reviewed permissions and both policies, or none.\n")
	r.body = body.String()
	return r
}

func prepareWorkPairReview(ctx context.Context, client *managed.Client, from, to, mode string) (workPairReview, error) {
	r := workPairReview{}
	if from == to || mode != "review" && mode != "auto" {
		return r, errors.New("pair requires two distinct native session IDs and review or auto delivery")
	}
	raw, err := workCall(ctx, client, "control.get", map[string]any{})
	if err != nil {
		return r, err
	}
	var control managed.ControlState
	if err = json.Unmarshal(raw, &control); err != nil {
		return r, err
	}
	agents := make([]managed.AgentInfo, 2)
	for i, id := range []string{from, to} {
		agents[i], err = workAgentForLabel(ctx, client, id)
		if err != nil {
			return r, err
		}
	}
	if err = managed.ValidateTaskPairAgents(agents, control); err != nil {
		return r, err
	}
	r.request = managed.TaskPairRequest{Version: control.Version, Mode: mode}
	for _, agent := range agents {
		raw, err = workCall(ctx, client, "native.status", map[string]any{"agent": agent.ID})
		if err != nil {
			return r, err
		}
		var response struct {
			Status managed.NativeStatus `json:"status"`
		}
		if err = json.Unmarshal(raw, &response); err != nil {
			return r, err
		}
		status := response.Status
		if status.Agent != agent.ID || status.Policy.Version < 1 {
			return r, errors.New("current native delivery policy is unavailable; refresh before pairing")
		}
		if status.Adapter != "" && (!status.Capabilities.Instructions || !status.Capabilities.Observations || !status.Capabilities.Completion) {
			return r, errors.New("the current native adapter does not support tasks and returned results")
		}
		r.request.Agents = append(r.request.Agents, managed.TaskPairAgent{ID: agent.ID, PolicyVersion: status.Policy.Version, NativeSession: status.Session})
		r.statuses = append(r.statuses, status)
		r.labels = append(r.labels, consoleContent(sessionTitle(agent), 150))
	}
	return r.withMode(mode), nil
}

func runWorkPair(args []string, out, diagnostics io.Writer) int {
	if workHelpRequested(args) {
		fmt.Fprintln(out, `Usage: amx pair A B [--delivery review|auto] [--apply]
                [--session NAME | --state DIR]

Preview task delegation between two owned native sessions. Default: review.
--apply prints the concrete review and atomically grants Commands both ways,
sets both receiver modes and enables their own returned results.
Auto can start EXISTING eligible queues from ALL already-authorized senders.
Limits and other subscriptions stay saved. Notes, pings, boards and memory
permissions are unchanged. Manual-only, external and isolated sessions are rejected.
Pairing does not confirm native delivery or task verification.`)
		return exitSuccess
	}
	fail := func(err error) int { fmt.Fprintln(diagnostics, "error:", err); return exitValidation }
	if len(args) < 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		return fail(errors.New("pair requires two native session IDs; see pair --help"))
	}
	seen := map[string]bool{}
	for _, arg := range args[2:] {
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "--") {
			name, _, _ := strings.Cut(arg, "=")
			if seen[name] {
				return fail(fmt.Errorf("duplicate %s; no action was taken", name))
			}
			seen[name] = true
		}
	}
	flags := flag.NewFlagSet("pair", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	mode := flags.String("delivery", "review", "native delivery mode for both receivers")
	apply := flags.Bool("apply", false, "apply the exact reviewed pair atomically")
	session := flags.String("session", "work", "workspace name")
	state := flags.String("state", "", "workspace state")
	if err := flags.Parse(args[2:]); err != nil {
		return exitValidation
	}
	if flags.NArg() != 0 || *mode != "review" && *mode != "auto" {
		return fail(errors.New("choose exactly two IDs and --delivery review or auto"))
	}
	selectors := []string{}
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "session" {
			selectors = append(selectors, "--session", *session)
		} else if f.Name == "state" {
			selectors = append(selectors, "--state", *state)
		}
	})
	o, err := parseWorkOptions(selectors)
	if err != nil {
		return fail(err)
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(o.state, ""))
	if err != nil {
		return fail(errors.New("workspace not found; start a workspace first"))
	}
	client := managed.NewClient(credential)
	review, err := prepareWorkPairReview(context.Background(), client, args[0], args[1], *mode)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintln(out, "Workspace:", consoleContent(o.state, 300))
	fmt.Fprintln(out, review.body)
	if !*apply {
		command := []string{"amx", "pair", args[0], args[1], "--delivery", *mode, "--apply", "--state", o.state}
		fmt.Fprintln(out, "Preview only; no settings changed. Apply after reviewing:", managedlaunch.ShellCommand(command))
		return exitSuccess
	}
	if _, err = workCall(context.Background(), client, "control.pair", review.args()); err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		fmt.Fprintln(diagnostics, "Pairing was not confirmed. Inspect current permissions and delivery before retrying; no automatic retry or rollback was attempted. An older workspace controller may require an explicit upgrade.")
		return exitRuntime
	}
	fmt.Fprintf(out, "Task pair saved: %s ↔ %s · both receivers %s · returned results on.\n", consoleContent(args[0], 100), consoleContent(args[1], 100), *mode)
	fmt.Fprintln(out, "Inspect:", managedlaunch.ShellCommand([]string{"amx", "delivery", args[1], "--state", o.state}))
	return exitSuccess
}
