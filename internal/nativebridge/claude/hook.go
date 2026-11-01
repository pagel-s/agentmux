package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agentmux/internal/nativebridge"
)

const pollInterval = 500 * time.Millisecond

var sessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

type hookInput struct {
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
}

type hookEnvironment struct {
	dir, nonce, socket, token string
}

// RunHook is the silent asynchronous SessionStart helper launched by Run. The
// CLI must construct conn from the normal scoped agent credential; it must not
// print the usual managed JSON envelope on successful hook exit. Native tokens
// are read only from the child environment, never sent to the broker or stored.
func RunHook(ctx context.Context, in io.Reader, conn nativebridge.Connection) error {
	env := hookEnvironment{os.Getenv(bridgeDirEnv), os.Getenv(bridgeNonceEnv), os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET"), os.Getenv("CLAUDE_CODE_MESSAGING_TOKEN")}
	return runHook(ctx, in, conn, env, pollInterval)
}

func runHook(ctx context.Context, in io.Reader, conn nativebridge.Connection, env hookEnvironment, interval time.Duration) error {
	var input hookInput
	data, err := io.ReadAll(io.LimitReader(in, 1<<20+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &input) != nil || input.Event != "SessionStart" || !sessionPattern.MatchString(input.SessionID) {
		return errors.New("invalid Claude SessionStart bridge event")
	}
	if conn == nil {
		return errors.New("Claude native bridge requires a scoped broker connection")
	}
	if !alive(env) {
		return errors.New("Claude native bridge invocation is no longer active")
	}
	if err := validateSocket(env.socket); err != nil {
		return err
	}
	if env.token == "" || len(env.token) > 4096 || strings.ContainsAny(env.token, "\x00\r\n") {
		return errors.New("Claude did not export its native inbox token; native collaboration is unavailable")
	}
	// SessionStart can run again after /clear or resume. Publish the new native
	// ID before taking the exclusive helper lock: the old helper notices the
	// change and exits, while duplicate hooks for the same ID simply return.
	marker := filepath.Join(env.dir, "session")
	if err := writeSession(marker, input.SessionID); err != nil {
		return err
	}
	unlock, err := helperLock(ctx, filepath.Join(env.dir, "helper.lock"), marker, input.SessionID, interval)
	if err != nil {
		return err
	}
	if unlock == nil {
		return nil
	}
	defer unlock()
	if !currentSession(env, input.SessionID) {
		return nil
	}
	registration := nativebridge.Registration{Adapter: "claude", Session: input.SessionID, Capabilities: nativebridge.Capabilities{
		Instructions: true, Observations: true, Completion: true, NativeQueue: true, CompletionEvidence: "receiver-ack",
		// Installed 2.1.316 passes draft/approval tests; retain a conservative
		// capability for unverified native versions and configurations.
		PreservesDraft: false, Cancellation: false,
	}}
	backoff := interval * 2
	pause := func() error {
		err := guardedWait(ctx, env, input.SessionID, backoff)
		backoff = min(backoff*2, interval*20)
		return err
	}
	for {
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := conn.Register(callCtx, registration)
		cancel()
		if err == nil {
			break
		}
		if definitive(err) {
			return err
		}
		if err := pause(); err != nil {
			return hookExit(err)
		}
	}
	var submitted, fenced string
	var pending []nativebridge.Event
	defer func() {
		if submitted != "" {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = conn.Report(cleanup, nativebridge.Event{ID: submitted, Phase: "uncertain", Reason: "Claude native bridge stopped before correlated completion was confirmed"})
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !currentSession(env, input.SessionID) {
			return nil
		}
		if err := validateSocket(env.socket); err != nil {
			return nil
		}
		// Retain exact evidence across an ambiguous RPC response. The broker
		// accepts an identical retry and ignores late transport progress after a
		// model ACK; no message is written to the native inbox again.
		if len(pending) > 0 {
			callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := conn.Report(callCtx, pending[0])
			cancel()
			if err != nil {
				if !definitive(err) {
					if err := pause(); err != nil {
						return hookExit(err)
					}
					continue
				}
				// Restart recovery may fence the task instance, or the owner may
				// have resolved it. Drop stale evidence and wait for activeID to
				// clear; neither outcome authorizes replay of the native turn.
				fenced = pending[0].ID
				if submitted == fenced {
					submitted = ""
				}
			}
			pending = pending[1:]
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		status, err := conn.Status(callCtx)
		cancel()
		if err != nil {
			if definitive(err) {
				return err
			}
			if err := pause(); err != nil {
				return hookExit(err)
			}
			continue
		}
		if submitted != "" && status.ActiveID != submitted {
			submitted = ""
		}
		if fenced != "" && status.ActiveID != fenced {
			fenced = ""
		}
		if status.ActiveID != "" && submitted == "" && fenced != status.ActiveID {
			// A lost claim response can reserve a turn whose payload we never
			// received. The broker's stable ID lets us fence it without guessing
			// whether another process delivered it. Owner resolution is required.
			fenced = status.ActiveID
			if status.State != "uncertain" {
				pending = append(pending, nativebridge.Event{ID: fenced, Phase: "uncertain", Reason: "Native claim or prior submission is unconfirmed after a broker communication failure; inspect the terminal and resolve the task before another turn"})
				continue
			}
		}
		if status.Cancel && submitted != "" {
			pending = append(pending, nativebridge.Event{ID: submitted, Phase: "uncertain", Reason: "Cancellation cannot retract a message already queued in Claude's native inbox; interrupt the native terminal if required"})
			fenced, submitted = submitted, ""
			continue
		}
		callCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
		delivery, err := conn.Poll(callCtx, nativebridge.Readiness{State: "ready", Reason: "Native inbox supports a later queue boundary; terminal draft and approval preservation are not verified"})
		cancel()
		if err != nil {
			// A queue can change between peek and claim when the owner cancels
			// or edits policy. This is a normal admission race, not a dead adapter.
			if definitive(err) && errorCode(err) != "conflict" {
				return err
			}
			// Do not retry Poll here: its claim may already have committed.
			// The next iteration checks status before requesting another claim.
			if err := pause(); err != nil {
				return hookExit(err)
			}
			continue
		}
		if delivery != nil {
			if !currentSession(env, input.SessionID) {
				callCtx, cancel := context.WithTimeout(ctx, time.Second)
				_ = conn.Report(callCtx, nativebridge.Event{ID: delivery.ID, Phase: "failed", Reason: "Claude session changed before native submission"})
				cancel()
				return nil
			}
			callCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
			status, err := conn.Status(callCtx)
			cancel()
			if err != nil {
				// Even a pre-write status failure must not lose a leased ID.
				fenced = delivery.ID
				pending = append(pending, nativebridge.Event{ID: delivery.ID, Phase: "uncertain", Reason: "Broker state could not be checked after native claim; payload was not submitted"})
				continue
			}
			if status.Cancel || status.Mode == "manual" || status.Mode == "paused" {
				pending = append(pending, nativebridge.Event{ID: delivery.ID, Phase: "cancelled", Reason: "Delivery cancelled or paused before native submission"})
			} else {
				written, err := post(ctx, env, input.SessionID, *delivery)
				if err != nil {
					phase := "failed"
					if written {
						phase = "uncertain"
						fenced = delivery.ID
					}
					pending = append(pending, nativebridge.Event{ID: delivery.ID, Phase: phase, Reason: "Claude native inbox submission failed; no automatic replay"})
				} else {
					submitted = delivery.ID
					pending = append(pending, nativebridge.Event{ID: delivery.ID, Phase: "submitted", NativeRef: input.SessionID + ":" + nativeMessageID(input.SessionID, delivery.ID), Reason: "Written to Claude native inbox; awaiting the recipient's correlated acknowledgement"})
				}
			}
		}
		backoff = interval * 2
		if err := guardedWait(ctx, env, input.SessionID, interval); err != nil {
			return hookExit(err)
		}
	}
}

// Definitive broker faults must not be hammered indefinitely. Transport, busy
// admission and ordinary network errors remain recoverable with backoff.
func definitive(err error) bool {
	switch errorCode(err) {
	case "conflict", "forbidden", "not_found", "expired", "invalid", "unsupported", "unauthorized":
		return true
	default:
		return false
	}
}

func errorCode(err error) string {
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return ""
}

var errInvocationEnded = errors.New("native invocation ended")

func hookExit(err error) error {
	if errors.Is(err, errInvocationEnded) {
		return nil
	}
	return err
}
func guardedWait(ctx context.Context, env hookEnvironment, session string, duration time.Duration) error {
	deadline := time.Now().Add(duration)
	for {
		if !currentSession(env, session) {
			return errInvocationEnded
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil
		}
		if err := wait(ctx, min(remaining, 100*time.Millisecond)); err != nil {
			return err
		}
	}
}

func alive(env hookEnvironment) bool {
	if !filepath.IsAbs(env.dir) || len(env.nonce) != 48 {
		return false
	}
	st, err := os.Lstat(env.dir)
	if err != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return false
	}
	data, err := os.ReadFile(filepath.Join(env.dir, "alive"))
	return err == nil && string(data) == env.nonce
}

func currentSession(env hookEnvironment, session string) bool {
	if !alive(env) {
		return false
	}
	data, err := os.ReadFile(filepath.Join(env.dir, "session"))
	return err == nil && string(data) == session
}

func validateSocket(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("Claude did not export an absolute native inbox socket")
	}
	st, err := os.Lstat(path)
	if err != nil || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm()&0077 != 0 || !ownedByCurrentUser(st) {
		return errors.New("Claude native inbox socket is unavailable or is not private to the current user")
	}
	return nil
}

func writeSession(path, session string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.WriteString(session); err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func post(ctx context.Context, env hookEnvironment, session string, delivery nativebridge.Delivery) (bool, error) {
	if err := validateSocket(env.socket); err != nil {
		return false, err
	}
	dial := net.Dialer{Timeout: time.Second}
	socket, err := dial.DialContext(ctx, "unix", env.socket)
	if err != nil {
		return false, err
	}
	defer socket.Close()
	_ = socket.SetWriteDeadline(time.Now().Add(time.Second))
	auth, _ := json.Marshal(map[string]string{"type": "auth", "token": env.token})
	frame, err := json.Marshal(map[string]any{
		"type": "user", "session_id": session, "uuid": nativeMessageID(session, delivery.ID), "priority": "later",
		"message": map[string]string{"role": "user", "content": deliveryPrompt(session, delivery)},
	})
	if err != nil {
		return false, err
	}
	if len(frame) > 900000 {
		return false, errors.New("native delivery exceeds Claude inbox message limit")
	}
	// Separate auth and delivery writes make a pre-payload failure distinguishable
	// from an ambiguous partially written delivery. Neither is a model ACK.
	if _, err := socket.Write(append(auth, '\n')); err != nil {
		return false, err
	}
	n, err := socket.Write(append(frame, '\n'))
	if err == nil && n != len(frame)+1 {
		err = io.ErrShortWrite
	}
	return n > 0, err
}

// Claude normally assigns UUIDs to conversation rows. Keep that native format
// while deriving a stable ID for this one broker delivery and native session.
func nativeMessageID(session, delivery string) string {
	sum := sha256.Sum256([]byte(session + "\x00" + delivery))
	sum[6] = sum[6]&0x0f | 0x50
	sum[8] = sum[8]&0x3f | 0x80
	h := hex.EncodeToString(sum[:16])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func deliveryPrompt(session string, d nativebridge.Delivery) string {
	privacy := "\n\nReturn only output attributable to this delivery. Never include intervening human input or answers to it in the broker result. If attribution becomes ambiguous, complete with an explicit result-withheld notice. Read native.status for max_result_bytes; use a labeled summary within that UTF-8 byte limit when needed. The full answer stays in the native terminal."
	args, _ := json.Marshal(map[string]string{"session": session, "id": d.ID, "phase": "running"})
	end, _ := json.Marshal(map[string]string{"session": session, "id": d.ID, "phase": "completed", "result": "<your response or result>"})
	return nativebridge.Prompt(d) + fmt.Sprintf("\n\nAgentmux delivery protocol: before acting, acknowledge this exact delivery with:\n\"$AGENTMUX_CLI\" managed call native.event --json %s\nProceed only if that broker call succeeds. If denied or cancelled, do not execute this request. After handling it, report a correlated result with:\n\"$AGENTMUX_CLI\" managed call native.event --json %s\nReplace the result placeholder with your actual response, preserving valid JSON. Use phase failed with a reason if you cannot complete it. These are broker lifecycle calls, not consent to change permissions or bypass a human approval prompt.\n", shellQuote(string(args)), shellQuote(string(end))) + privacy
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func wait(ctx context.Context, duration time.Duration) error {
	t := time.NewTimer(duration)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
