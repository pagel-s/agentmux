package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"agentmux/internal/nativebridge"
)

type pendingDelivery struct {
	delivery          nativebridge.Delivery
	session           string
	queue             string
	turn              string
	terminal          bool
	settled           bool // native completion/deletion proof, unlike uncertainty
	cancelling        bool
	humanIntervened   bool
	humanHoldReported bool
}

// Delivery runs separately from the transparent native transport. A broker
// outage must not freeze terminal rendering, input, or native approval prompts.
func (p *proxy) deliver(ctx context.Context, conn nativebridge.Connection) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	registered := ""
	resultLimit := 48 << 10
	recoverableID := ""
	var pending *pendingDelivery
	var reports []nativebridge.Event
	for {
		select {
		case <-ctx.Done():
			// The child can finish just before terminal exit. Preserve known
			// final results even when the normal event flush was interrupted.
			for _, report := range reports {
				cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = conn.Report(cleanup, report)
				cancel()
			}
			if pending != nil && !pending.terminal {
				cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = conn.Report(cleanup, nativebridge.Event{ID: pending.delivery.ID, Phase: "uncertain", NativeRef: pending.turn, Reason: "native terminal disconnected; submitted work must not be replayed automatically"})
				cancel()
			}
			return
		case event := <-p.events:
			if pending == nil {
				continue
			}
			if report := pending.observe(event); report != nil {
				limitResult(report, resultLimit)
				reports = append(reports, *report)
			}
		case <-ticker.C:
			// Reporting is retried, never queue submission. Terminal reports are
			// retained until acknowledged before leasing any next delivery.
			if len(reports) > 0 {
				reports[0].Reason = truncateUTF8(reports[0].Reason, 2048)
				callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				err := conn.Report(callCtx, reports[0])
				cancel()
				if err != nil {
					// The owner may explicitly resolve this exact attempt while
					// its final report is in flight. A confirmed terminal native
					// turn can then be forgotten; an active turn still needs its
					// own correlated cancellation, never a blind new submission.
					callCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
					status, statusErr := conn.Status(callCtx)
					cancel()
					if statusErr == nil && pending != nil {
						if status.MaxResultBytes > 0 {
							resultLimit = status.MaxResultBytes
						}
						// A broker restart fences the old task instance. Its
						// submitted receipt can no longer be acknowledged, but
						// an exact known native queue/turn must still be stopped.
						if status.Cancel && (status.ActiveID == "" || status.ActiveID == pending.delivery.ID) {
							p.cancelPeer(ctx, pending, &reports)
						}
						resolved := status.ActiveID != pending.delivery.ID && (status.ActiveID != "" || status.State != "uncertain")
						if resolved {
							if pending.terminal {
								if !pending.settled {
									p.disable("resolved peer delivery still awaits native quiescence")
									recoverableID = pending.delivery.ID
								}
								reports = nil
								pending = nil
							} else {
								p.cancelPeer(ctx, pending, &reports)
							}
						}
					}
					continue
				}
				reports = reports[1:]
				if pending != nil && pending.terminal && len(reports) == 0 {
					pending = nil
				}
				continue
			}
			p.mu.Lock()
			session, state, initialized, fault, closed := p.current, p.state, p.initialized, p.fault, p.closed
			p.mu.Unlock()
			if !initialized || session == "" || closed {
				continue
			}
			if pending != nil {
				callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				status, err := conn.Status(callCtx)
				cancel()
				if err != nil {
					continue
				}
				if status.MaxResultBytes > 0 {
					resultLimit = status.MaxResultBytes
				}
				if fault != "" && !pending.terminal {
					pending.terminal = true
					reports = append(reports, nativebridge.Event{ID: pending.delivery.ID, Phase: "uncertain", Reason: fault})
					continue
				}
				if status.Cancel && (status.ActiveID == "" || status.ActiveID == pending.delivery.ID) {
					// Cancellation is addressed to the original correlated turn,
					// independently of which conversation the human now displays.
					p.cancelPeer(ctx, pending, &reports)
				} else if session != registered {
					// A /new or resume switches the UI, not the destination of an
					// already submitted peer request. Cancel queued old work; an
					// active old turn may finish under its original registration.
					if session == registered || pending.turn == "" {
						p.cancelPeer(ctx, pending, &reports)
					}
				}
				continue
			}
			if fault != "" {
				callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				if recoverableID != "" && registered != "" && state == "idle" {
					status, statusErr := conn.Status(callCtx)
					if statusErr == nil && status.ActiveID == "" && status.State != "uncertain" {
						threadRaw, threadErr := p.call(callCtx, "thread/read", map[string]any{"threadId": registered, "includeTurns": false})
						var thread struct {
							Thread struct {
								Status struct {
									Type string `json:"type"`
								} `json:"status"`
							} `json:"thread"`
						}
						quiescent := threadErr == nil && json.Unmarshal(threadRaw, &thread) == nil && (thread.Thread.Status.Type == "idle" || thread.Thread.Status.Type == "notLoaded")
						// Explicit owner resolution releases the broker slot. The
						// old native queue must also be empty before any new claim.
						// Do not resend or start the uncertain request.
						var raw json.RawMessage
						var queueErr error
						if quiescent {
							raw, queueErr = p.call(callCtx, "thread/queue/list", map[string]any{"threadId": registered})
						}
						var queue struct {
							Data []struct {
								ID       string `json:"id"`
								ClientID string `json:"clientUserMessageId"`
							} `json:"data"`
						}
						if quiescent && queueErr == nil && json.Unmarshal(raw, &queue) == nil {
							found := false
							for _, entry := range queue.Data {
								if entry.ClientID == recoverableID {
									found = true
									_, _ = p.call(callCtx, "thread/queue/delete", map[string]any{"threadId": registered, "queuedSubmissionId": entry.ID})
								}
							}
							if !found {
								p.mu.Lock()
								if p.state == "idle" && p.fault == fault {
									p.fault = ""
									recoverableID = ""
								}
								p.mu.Unlock()
							}
						}
					}
				}
				if registered == "" {
					if err := conn.Register(callCtx, nativebridge.Registration{Adapter: "codex", Session: session}); err == nil {
						registered = session
					}
				}
				if registered != "" {
					_, _ = conn.Poll(callCtx, nativebridge.Readiness{State: "offline", Reason: fault})
				}
				cancel()
				continue
			}
			if registered != session {
				if p.ownerContext != "" {
					// Preserve an already active human turn. Frontend turn/start
					// is independently gated, so an idle root is initialized before
					// either its first human turn or any peer queue submission.
					if state != "idle" {
						continue
					}
					if err := p.prepareContext(ctx, session); err != nil {
						continue
					}
				}
				// Probe only a read operation. An incompatible Codex keeps its
				// native UI but never advertises unsupported instruction delivery.
				callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				_, err := p.call(callCtx, "thread/queue/list", map[string]any{"threadId": session})
				cancel()
				if err != nil {
					p.disable("Codex native queue is unavailable: " + err.Error())
					continue
				}
				callCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
				err = conn.Register(callCtx, nativebridge.Registration{Adapter: "codex", Session: session, Capabilities: nativebridge.Capabilities{Instructions: true, Observations: true, Completion: true, Cancellation: true, PreservesDraft: true, NativeQueue: true, CompletionEvidence: "native-events"}})
				cancel()
				if err != nil {
					continue
				}
				registered = session
				callCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
				if status, err := conn.Status(callCtx); err == nil && status.MaxResultBytes > 0 {
					resultLimit = status.MaxResultBytes
				}
				cancel()
			}
			ready := nativebridge.Readiness{State: "busy", Reason: "Codex has an active native turn"}
			switch state {
			case "idle":
				ready = nativebridge.Readiness{State: "idle"}
			case "waitingOnApproval":
				ready = nativebridge.Readiness{State: "approval", Reason: "native approval is waiting for the human"}
			case "waitingOnUserInput":
				ready = nativebridge.Readiness{State: "busy", Reason: "native question is waiting for the human"}
			case "notLoaded", "systemError":
				ready = nativebridge.Readiness{State: "offline", Reason: "Codex thread is unavailable"}
			}
			callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			d, err := conn.Poll(callCtx, ready)
			cancel()
			if err != nil || d == nil {
				continue
			}
			pending = &pendingDelivery{delivery: *d, session: session}
			// A claim is durable. A failed pre-submit report means no native
			// submission occurred, so report failure instead of silently losing it.
			callCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
			err = conn.Report(callCtx, nativebridge.Event{ID: d.ID, Phase: "submitting"})
			cancel()
			if err != nil {
				pending.terminal = true
				pending.settled = true // no native request was sent
				reports = append(reports, nativebridge.Event{ID: d.ID, Phase: "failed", Reason: "broker unavailable before native submission"})
				continue
			}
			// Check current root again after a potentially slow broker call.
			p.mu.Lock()
			same := p.current == session && p.fault == "" && !p.closed
			p.mu.Unlock()
			if !same {
				pending.terminal = true
				pending.settled = true // changed root fenced submission
				reports = append(reports, nativebridge.Event{ID: d.ID, Phase: "cancelled", Reason: "native conversation changed before submission"})
				continue
			}
			callCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
			raw, err := p.call(callCtx, "thread/queue/add", map[string]any{"threadId": session, "clientUserMessageId": d.ID, "input": []map[string]any{{"type": "text", "text": nativebridge.Prompt(*d)}}})
			cancel()
			if err != nil {
				// Even an RPC timeout may have executed. Never resubmit it.
				pending.terminal = true
				recoverableID = d.ID
				p.disable("native queue submission is unconfirmed; automatic delivery paused")
				reports = append(reports, nativebridge.Event{ID: d.ID, Phase: "uncertain", Reason: err.Error()})
				continue
			}
			var receipt struct {
				Submission struct {
					ID       string `json:"id"`
					ClientID string `json:"clientUserMessageId"`
				} `json:"queuedSubmission"`
			}
			if json.Unmarshal(raw, &receipt) != nil || receipt.Submission.ID == "" || receipt.Submission.ClientID != d.ID {
				pending.terminal = true
				recoverableID = d.ID
				p.disable("native queue returned an invalid receipt; delivery paused")
				reports = append(reports, nativebridge.Event{ID: d.ID, Phase: "uncertain", Reason: "invalid native queue receipt"})
				continue
			}
			pending.queue = receipt.Submission.ID
			reports = append(reports, nativebridge.Event{ID: d.ID, Phase: "submitted", NativeRef: pending.queue})
		}
	}
}

func limitResult(e *nativebridge.Event, limit int) {
	if limit <= 0 {
		limit = 48 << 10
	}
	if len(e.Result) <= limit {
		return
	}
	notice := "\n[Agentmux: broker result truncated; full answer remains in the native terminal.]"
	keep := limit - len(notice)
	if keep < 0 {
		keep = 0
		notice = notice[:limit]
	}
	for keep > 0 && !utf8.RuneStart(e.Result[keep]) {
		keep--
	}
	e.Result = e.Result[:keep] + notice
	e.Reason = strings.TrimSpace(e.Reason + " Result truncated to broker storage limit; full answer remains in the native terminal.")
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func (d *pendingDelivery) observe(event notification) *nativebridge.Event {
	if d.terminal {
		return nil
	}
	var e struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Item     struct {
			Type     string `json:"type"`
			ClientID string `json:"clientId"`
		} `json:"item"`
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
			Items []struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Phase string `json:"phase"`
			} `json:"items"`
		} `json:"turn"`
	}
	if json.Unmarshal(event.params, &e) != nil || e.ThreadID != d.session {
		return nil
	}
	if event.method == "agentmux/humanIntervention" && d.turn != "" && e.TurnID == d.turn {
		d.humanIntervened = true
		return nil
	}
	if event.method == "item/started" && e.Item.Type == "userMessage" && e.Item.ClientID == d.delivery.ID {
		if d.turn != "" {
			return nil
		}
		d.turn = e.TurnID
		return &nativebridge.Event{ID: d.delivery.ID, Phase: "running", NativeRef: d.turn}
	}
	if event.method == "thread/closed" {
		d.terminal = true
		return &nativebridge.Event{ID: d.delivery.ID, Phase: "uncertain", NativeRef: d.turn, Reason: "native thread closed before peer request completed"}
	}
	if event.method != "turn/completed" || d.turn == "" || e.Turn.ID != d.turn {
		return nil
	}
	d.terminal = true
	d.settled = true
	result := ""
	for _, item := range e.Turn.Items {
		if item.Type == "agentMessage" && strings.TrimSpace(item.Text) != "" {
			result = item.Text
		}
	}
	phase, reason := "completed", ""
	switch e.Turn.Status {
	case "completed":
	case "interrupted":
		phase = "cancelled"
		reason = "native peer turn was interrupted"
	default:
		phase = "failed"
		reason = fmt.Sprintf("native peer turn finished with status %q", e.Turn.Status)
	}
	if e.Turn.Error != nil {
		reason = e.Turn.Error.Message
	}
	if d.humanIntervened {
		result = "Automatic result withheld: the human interacted with this native peer turn. Review the answer in the native terminal and share it explicitly if appropriate."
		reason = "human intervention; native output and error details were kept private"
	}
	return &nativebridge.Event{ID: d.delivery.ID, Phase: phase, NativeRef: d.turn, Result: result, Reason: reason}
}

func (p *proxy) cancelPeer(ctx context.Context, d *pendingDelivery, reports *[]nativebridge.Event) {
	if d.terminal || d.cancelling {
		return
	}
	if d.turn != "" && d.humanIntervened {
		if !d.humanHoldReported {
			d.humanHoldReported = true
			*reports = append(*reports, nativebridge.Event{ID: d.delivery.ID, Phase: "running", NativeRef: d.turn, Reason: "human-controlled turn continues; future peer delivery is paused; interrupt explicitly in the native terminal to stop this turn"})
		}
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if d.turn != "" {
		// The exact turn must match, so a later human turn cannot be stopped.
		if _, err := p.call(callCtx, "turn/interrupt", map[string]any{"threadId": d.session, "turnId": d.turn}); err == nil {
			d.cancelling = true
		}
		return
	}
	if d.queue == "" {
		return
	}
	raw, err := p.call(callCtx, "thread/queue/delete", map[string]any{"threadId": d.session, "queuedSubmissionId": d.queue})
	if err != nil {
		return
	}
	var result struct {
		Deleted bool `json:"deleted"`
	}
	if json.Unmarshal(raw, &result) == nil && result.Deleted {
		d.terminal = true
		d.settled = true
		*reports = append(*reports, nativebridge.Event{ID: d.delivery.ID, Phase: "cancelled", NativeRef: d.queue, Reason: "queued peer request cancelled before execution"})
	}
}
