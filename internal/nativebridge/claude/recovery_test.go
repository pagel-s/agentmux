//go:build unix

package claude

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentmux/internal/nativebridge"
)

type codedFailure struct{ code string }

func (e codedFailure) Error() string     { return "fixture " + e.code }
func (e codedFailure) ErrorCode() string { return e.code }

type recoveringFixture struct {
	base                                   *fixtureConn
	mu                                     sync.Mutex
	statusFailures                         int
	pollConflicts                          int
	lostClaim, lostReport, committedReport bool
	fenced                                 bool
	attempts                               []nativebridge.Event
	unbounded                              bool
}

func (f *recoveringFixture) bounded(ctx context.Context) {
	if _, ok := ctx.Deadline(); !ok {
		f.mu.Lock()
		f.unbounded = true
		f.mu.Unlock()
	}
}
func (f *recoveringFixture) Register(ctx context.Context, r nativebridge.Registration) error {
	f.bounded(ctx)
	return f.base.Register(ctx, r)
}
func (f *recoveringFixture) Status(ctx context.Context) (nativebridge.Status, error) {
	f.bounded(ctx)
	f.mu.Lock()
	if f.statusFailures > 0 {
		f.statusFailures--
		f.mu.Unlock()
		return nativebridge.Status{}, codedFailure{"transport"}
	}
	f.mu.Unlock()
	return f.base.Status(ctx)
}
func (f *recoveringFixture) Poll(ctx context.Context, r nativebridge.Readiness) (*nativebridge.Delivery, error) {
	f.bounded(ctx)
	f.mu.Lock()
	if f.pollConflicts > 0 {
		f.pollConflicts--
		f.mu.Unlock()
		return nil, codedFailure{"conflict"}
	}
	f.mu.Unlock()
	d, err := f.base.Poll(ctx, r)
	f.mu.Lock()
	lost := f.lostClaim && d != nil
	if lost {
		f.lostClaim = false
	}
	f.mu.Unlock()
	if lost {
		return nil, codedFailure{"transport"}
	}
	return d, err
}
func (f *recoveringFixture) Report(ctx context.Context, e nativebridge.Event) error {
	f.bounded(ctx)
	f.mu.Lock()
	f.attempts = append(f.attempts, e)
	lost := f.lostReport && e.Phase == "submitted"
	if lost {
		f.lostReport = false
	}
	committed := f.committedReport
	fenced := f.fenced && e.Phase == "uncertain"
	f.mu.Unlock()
	if fenced {
		return codedFailure{"conflict"}
	}
	if lost {
		if committed {
			_ = f.base.Report(ctx, e)
		}
		return codedFailure{"transport"}
	}
	return f.base.Report(ctx, e)
}
func startRecoveringHook(t *testing.T, ctx context.Context, env hookEnvironment, conn nativebridge.Connection) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- runHook(ctx, strings.NewReader(`{"session_id":"native-session","hook_event_name":"SessionStart"}`), conn, env, 5*time.Millisecond)
	}()
	return done
}
func drainNative(t *testing.T, listener net.Listener) *atomic.Int32 {
	t.Helper()
	count := &atomic.Int32{}
	go func() {
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, socket)
			_ = socket.Close()
			count.Add(1)
		}
	}()
	return count
}

func TestBrokerStatusOutageRecoversWithoutRelaunch(t *testing.T) {
	env, listener := hookFixture(t)
	count := drainNative(t, listener)
	base := &fixtureConn{delivery: &nativebridge.Delivery{ID: "after-outage"}, status: nativebridge.Status{Mode: "auto"}}
	conn := &recoveringFixture{base: base, statusFailures: 3}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRecoveringHook(t, ctx, env, conn)
	await(t, func() bool { _, events := base.snapshot(); return len(events) > 0 })
	if count.Load() != 1 {
		t.Fatal("recovery did not submit exactly one native turn")
	}
	r, _ := base.snapshot()
	if len(r) != 1 {
		t.Fatal("network recovery repeatedly registered a conversation")
	}
	conn.mu.Lock()
	unbounded := conn.unbounded
	conn.mu.Unlock()
	if unbounded {
		t.Fatal("broker RPC lacked a deadline")
	}
	cancel()
	finish(t, done)
}

func TestLostNativeClaimResponseIsFencedAndNeverSubmitted(t *testing.T) {
	env, listener := hookFixture(t)
	count := drainNative(t, listener)
	base := &fixtureConn{delivery: &nativebridge.Delivery{ID: "lost-claim"}, status: nativebridge.Status{Mode: "auto"}}
	conn := &recoveringFixture{base: base, lostClaim: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRecoveringHook(t, ctx, env, conn)
	await(t, func() bool { _, events := base.snapshot(); return len(events) > 0 })
	_, events := base.snapshot()
	if events[0].ID != "lost-claim" || events[0].Phase != "uncertain" || count.Load() != 0 {
		t.Fatalf("ambiguous claim replayed: %+v", events)
	}
	// Only explicit owner resolution allows another, distinct request.
	base.mu.Lock()
	base.status.ActiveID = ""
	base.status.State = "ready"
	base.claimed = false
	base.delivery = &nativebridge.Delivery{ID: "fresh-after-owner-resolution"}
	base.mu.Unlock()
	await(t, func() bool { _, events := base.snapshot(); return len(events) > 1 })
	_, events = base.snapshot()
	if events[1].ID != "fresh-after-owner-resolution" || events[1].Phase != "submitted" {
		t.Fatalf("did not recover after owner resolution: %+v", events)
	}
	await(t, func() bool { return count.Load() == 1 })
	cancel()
	finish(t, done)
}

func TestLostNativeReceiptRetriesExactEvidenceWithoutReplayingInbox(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "request-lost", true: "response-lost"}[committed], func(t *testing.T) {
			env, listener := hookFixture(t)
			count := drainNative(t, listener)
			base := &fixtureConn{delivery: &nativebridge.Delivery{ID: "one-native-write"}, status: nativebridge.Status{Mode: "auto"}}
			conn := &recoveringFixture{base: base, lostReport: true, committedReport: committed}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := startRecoveringHook(t, ctx, env, conn)
			await(t, func() bool { conn.mu.Lock(); defer conn.mu.Unlock(); return len(conn.attempts) >= 2 })
			conn.mu.Lock()
			attempts := append([]nativebridge.Event{}, conn.attempts...)
			conn.mu.Unlock()
			if !reflect.DeepEqual(attempts[0], attempts[1]) || attempts[0].Phase != "submitted" {
				t.Fatalf("retried a changed receipt: %+v", attempts)
			}
			await(t, func() bool { return count.Load() == 1 })
			time.Sleep(20 * time.Millisecond)
			if count.Load() != 1 {
				t.Fatal("receipt retry replayed native request")
			}
			cancel()
			finish(t, done)
		})
	}
}

func TestRecoveredInstanceConflictWaitsForOwnerAndKeepsHelperAlive(t *testing.T) {
	env, listener := hookFixture(t)
	count := drainNative(t, listener)
	base := &fixtureConn{delivery: &nativebridge.Delivery{ID: "old-instance"}, status: nativebridge.Status{Mode: "auto"}}
	conn := &recoveringFixture{base: base}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRecoveringHook(t, ctx, env, conn)
	await(t, func() bool { _, events := base.snapshot(); return len(events) > 0 })
	conn.mu.Lock()
	conn.fenced = true
	conn.mu.Unlock()
	base.mu.Lock()
	base.status.State = "uncertain"
	base.status.Cancel = true
	base.mu.Unlock()
	await(t, func() bool { conn.mu.Lock(); defer conn.mu.Unlock(); return len(conn.attempts) > 1 })
	time.Sleep(25 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("transient restart killed native helper: %v", err)
	default:
	}
	conn.mu.Lock()
	n := len(conn.attempts)
	conn.fenced = false
	conn.mu.Unlock()
	if n != 2 {
		t.Fatalf("definitive receipt conflict was hammered: %d attempts", n)
	}
	base.mu.Lock()
	base.status.Cancel = false
	base.status.ActiveID = ""
	base.status.State = "ready"
	base.claimed = false
	base.delivery = &nativebridge.Delivery{ID: "new-authorized-request"}
	base.mu.Unlock()
	await(t, func() bool { return count.Load() == 2 })
	cancel()
	finish(t, done)
}

func TestReconnectBackoffStopsWithItsNativeInvocation(t *testing.T) {
	env, _ := hookFixture(t)
	conn := &recoveringFixture{base: &fixtureConn{}, statusFailures: 100}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRecoveringHook(t, ctx, env, conn)
	await(t, func() bool { conn.mu.Lock(); defer conn.mu.Unlock(); return conn.statusFailures < 100 })
	_ = os.Remove(filepath.Join(env.dir, "alive"))
	finish(t, done)
}

func TestFaultClassificationDoesNotTreatTransportAsPermanent(t *testing.T) {
	if definitive(codedFailure{"transport"}) || definitive(errors.New("network disconnected")) || !definitive(codedFailure{"conflict"}) {
		t.Fatal("incorrect reconnect error classification")
	}
}

func TestOwnerQueueChangeDoesNotKillNativeHelper(t *testing.T) {
	env, listener := hookFixture(t)
	count := drainNative(t, listener)
	base := &fixtureConn{delivery: &nativebridge.Delivery{ID: "eligible-after-conflict"}, status: nativebridge.Status{Mode: "auto"}}
	conn := &recoveringFixture{base: base, pollConflicts: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRecoveringHook(t, ctx, env, conn)
	await(t, func() bool { return count.Load() == 1 })
	cancel()
	finish(t, done)
}
