package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
)

func observationHeadlessRuntime(t *testing.T) {
	t.Helper()
	previous := describeWorkRuntime
	describeWorkRuntime = func(context.Context, string) (managedlaunch.Runtime, error) {
		return managedlaunch.Runtime{}, os.ErrNotExist
	}
	t.Cleanup(func() { describeWorkRuntime = previous })
}

func observationComponents(diagnostics workObservationDiagnostics) map[string]workObservationComponent {
	return map[string]workObservationComponent{
		"broker": diagnostics.Components.Broker, "runtime": diagnostics.Components.Runtime,
		"native": diagnostics.Components.Native, "storage": diagnostics.Components.Storage,
	}
}

func assertObservationTiming(t *testing.T, diagnostics workObservationDiagnostics) {
	t.Helper()
	if diagnostics.CollectionDurationMS <= 0 {
		t.Fatalf("missing collection timing: %+v", diagnostics)
	}
	for name, component := range observationComponents(diagnostics) {
		if component.State == "not_attempted" {
			if component.DurationMS != nil || component.ErrorCode != nil {
				t.Fatalf("%s unattempted component acquired fabricated evidence: %+v", name, component)
			}
			continue
		}
		if component.DurationMS == nil || *component.DurationMS < 0 || *component.DurationMS > diagnostics.CollectionDurationMS {
			t.Fatalf("%s duration outside collection: %+v, total %f", name, component, diagnostics.CollectionDurationMS)
		}
	}
	broker, native := diagnostics.Components.Broker.DurationMS, diagnostics.Components.Native.DurationMS
	if broker != nil && native != nil && *broker+*native > diagnostics.CollectionDurationMS {
		t.Fatalf("sequential broker/native timings exceed collection (broker includes native time?): %+v", diagnostics)
	}
}

func assertObservationCode(t *testing.T, component workObservationComponent, want string) {
	t.Helper()
	if component.State != "unavailable" || component.ErrorCode == nil || *component.ErrorCode != want {
		t.Fatalf("wrong component classification: %+v, want unavailable/%s", component, want)
	}
}

func TestObserveDiagnosticsSuccessfulBrokerAndMissingCredentials(t *testing.T) {
	state, _, _ := inventoryBroker(t)
	observationHeadlessRuntime(t)
	result := collectWorkObservation(context.Background(), workOptions{state: state}, 25)
	if !result.Complete || result.SchemaVersion != 1 {
		t.Fatalf("diagnostics changed headless completion: %+v", result)
	}
	assertObservationTiming(t, result.Diagnostics)
	for name, component := range observationComponents(result.Diagnostics) {
		wantState := "observed"
		if name == "runtime" {
			wantState = "not_recorded"
		}
		if component.State != wantState || component.ErrorCode != nil {
			t.Fatalf("%s falsely reported failure or health: %+v", name, component)
		}
	}

	missing := filepath.Join(t.TempDir(), "not-created")
	result = collectWorkObservation(context.Background(), workOptions{state: missing}, 25)
	assertObservationTiming(t, result.Diagnostics)
	assertObservationCode(t, result.Diagnostics.Components.Broker, "credential_unavailable")
	if result.Complete || result.Diagnostics.Components.Native.State != "not_attempted" {
		t.Fatalf("missing broker resulted in attempted native observation: %+v", result)
	}
	raw, err := json.Marshal(result.Diagnostics.Components.Native)
	if err != nil || string(raw) != `{"state":"not_attempted","error_code":null,"duration_ms":null}` {
		t.Fatalf("unknown duration is not explicit null: %s, %v", raw, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("observation created missing state: %v", err)
	}
}

// Change only the owner's disposable fixture socket. The actual client still
// serializes authenticated requests, enforces response framing and deadlines,
// and delegates successful reads to the real broker.
func observationDiagnosticProxy(t *testing.T, state string, handler func(http.ResponseWriter, *http.Request, managed.Request)) {
	t.Helper()
	path := managed.CredentialPath(state, "")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := managed.LoadCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(state, "observe.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request managed.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad fixture request", http.StatusBadRequest)
			return
		}
		handler(w, r, request)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		<-done
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Error(err)
		}
	})
	credential.Socket = listener.Addr().String()
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestObserveDiagnosticsFaultsProtocolAndUnattemptedNative(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	observationHeadlessRuntime(t)
	for _, test := range []struct {
		name, operation, fault, result, code string
		malformedWire                        bool
	}{
		{name: "unauthorized", operation: "observation.get", fault: "unauthorized", code: "unauthenticated"},
		{name: "forbidden", operation: "observation.get", fault: "forbidden", code: "forbidden"},
		{name: "old-controller", operation: "observation.get", fault: "unsupported", code: "unsupported"},
		{name: "busy-reader", operation: "observation.get", fault: "unavailable", code: "unavailable"},
		{name: "unknown-code", operation: "observation.get", fault: "private-server-code", code: "unknown"},
		{name: "server-cancellation", operation: "observation.get", fault: "cancelled", code: "canceled"},
		{name: "bad-broker-result", operation: "observation.get", result: `"private malformed result"`, code: "invalid_response"},
		{name: "missing-observation-time", operation: "observation.get", result: `{}`, code: "invalid_response"},
		{name: "wire-protocol", operation: "observation.get", malformedWire: true, code: "transport"},
		{name: "native-unsupported", operation: "native.status", fault: "unsupported", code: "unsupported"},
		{name: "native-invalid", operation: "native.status", result: `{"statuses":null}`, code: "invalid_response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var nativeCalls atomic.Int32
			observationDiagnosticProxy(t, state, func(w http.ResponseWriter, r *http.Request, request managed.Request) {
				if request.Operation == "native.status" {
					nativeCalls.Add(1)
				}
				if request.Operation != test.operation {
					raw, problem := client.Call(r.Context(), request)
					_ = json.NewEncoder(w).Encode(map[string]any{"result": raw, "error": problem})
					return
				}
				if test.malformedWire {
					fmt.Fprint(w, "private invalid wire response")
				} else if test.fault != "" {
					_ = json.NewEncoder(w).Encode(map[string]any{"error": managed.Fault{Code: test.fault, Message: "private problem text: timeout unauthorized"}})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{"result": json.RawMessage(test.result)})
				}
			})
			result := collectWorkObservation(context.Background(), workOptions{state: state}, 25)
			assertObservationTiming(t, result.Diagnostics)
			if result.Complete {
				t.Fatal("partial observation became complete")
			}
			if test.operation == "observation.get" {
				assertObservationCode(t, result.Diagnostics.Components.Broker, test.code)
				if nativeCalls.Load() != 0 || result.Diagnostics.Components.Native.State != "not_attempted" || result.NativeError != "" {
					t.Fatalf("invented native attempt after broker failure: %+v", result)
				}
			} else {
				assertObservationCode(t, result.Diagnostics.Components.Native, test.code)
				if nativeCalls.Load() != 1 || result.Diagnostics.Components.Broker.State != "observed" || result.Snapshot == nil {
					t.Fatalf("native failure erased successful broker observation: %+v", result)
				}
			}
			raw, _ := json.Marshal(result.Diagnostics)
			if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "timeout unauthorized") {
				t.Fatalf("diagnostics copied server payload or guessed from prose: %s", raw)
			}
		})
	}
}

func TestObserveDiagnosticsLocalCancellationAndTimeout(t *testing.T) {
	state, _, _ := inventoryBroker(t)
	observationHeadlessRuntime(t)
	for _, code := range []string{"timeout", "canceled", "call_timeout"} {
		t.Run(code, func(t *testing.T) {
			started := make(chan struct{})
			observationDiagnosticProxy(t, state, func(w http.ResponseWriter, r *http.Request, _ managed.Request) {
				close(started)
				<-r.Context().Done()
			})
			var ctx context.Context
			var cancel context.CancelFunc
			if code == "timeout" {
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			} else if code == "canceled" {
				ctx, cancel = context.WithCancel(context.Background())
				go func() { <-started; cancel() }()
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			result := collectWorkObservation(ctx, workOptions{state: state}, 25)
			assertObservationTiming(t, result.Diagnostics)
			wantCode := code
			if code == "call_timeout" {
				wantCode = "timeout"
				if duration := result.Diagnostics.Components.Broker.DurationMS; duration == nil || *duration < 2800 {
					t.Fatalf("local call deadline was not exercised: %+v", result.Diagnostics)
				}
			}
			assertObservationCode(t, result.Diagnostics.Components.Broker, wantCode)
			if result.Complete || result.Diagnostics.Components.Native.State != "not_attempted" {
				t.Fatalf("canceled observation claimed complete/native evidence: %+v", result)
			}
		})
	}
}

func TestObserveDiagnosticsRejectedCredentialsAndTransport(t *testing.T) {
	state, _, _ := inventoryBroker(t)
	observationHeadlessRuntime(t)
	path := managed.CredentialPath(state, "")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"unauthenticated", "forbidden", "transport"} {
		t.Run(code, func(t *testing.T) {
			credential, err := managed.LoadCredential(path)
			if err != nil {
				t.Fatal(err)
			}
			switch code {
			case "unauthenticated":
				credential.Token = "invalid-owner-token-fixture"
			case "forbidden":
				credential, err = managed.LoadCredential(managed.CredentialPath(state, "peer"))
				if err != nil {
					t.Fatal(err)
				}
			case "transport":
				credential.Socket = filepath.Join(state, "missing.sock")
			}
			raw, err := json.Marshal(credential)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Error(err)
				}
			})
			result := collectWorkObservation(context.Background(), workOptions{state: state}, 25)
			assertObservationTiming(t, result.Diagnostics)
			assertObservationCode(t, result.Diagnostics.Components.Broker, code)
			if result.Complete || result.Snapshot != nil || result.Diagnostics.Components.Native.State != "not_attempted" {
				t.Fatalf("rejected read reported success: %+v", result)
			}
			diagnostics, _ := json.Marshal(result.Diagnostics)
			if strings.Contains(string(diagnostics), credential.Token) || strings.Contains(string(diagnostics), credential.Socket) {
				t.Fatalf("diagnostics contain credentials or socket path: %s", diagnostics)
			}
		})
	}
}

func TestObserveDiagnosticsNativeCancellationRetainsBroker(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	observationHeadlessRuntime(t)
	started := make(chan struct{})
	observationDiagnosticProxy(t, state, func(w http.ResponseWriter, r *http.Request, request managed.Request) {
		if request.Operation == "native.status" {
			close(started)
			<-r.Context().Done()
			return
		}
		raw, problem := client.Call(r.Context(), request)
		_ = json.NewEncoder(w).Encode(map[string]any{"result": raw, "error": problem})
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-started; cancel() }()
	result := collectWorkObservation(ctx, workOptions{state: state}, 25)
	assertObservationTiming(t, result.Diagnostics)
	assertObservationCode(t, result.Diagnostics.Components.Native, "canceled")
	if result.Complete || result.Snapshot == nil || result.Diagnostics.Components.Broker.State != "observed" || result.BrokerError != "" {
		t.Fatalf("native cancellation erased successful broker evidence: %+v", result)
	}
}

func TestObserveDiagnosticsRuntimeAndStorageFailures(t *testing.T) {
	previous := describeWorkRuntime
	describeWorkRuntime = func(context.Context, string) (managedlaunch.Runtime, error) {
		return managedlaunch.Runtime{}, context.DeadlineExceeded
	}
	t.Cleanup(func() { describeWorkRuntime = previous })
	// A directory at the database name is metadata-only and never traversed.
	storageState := t.TempDir()
	if err := os.Mkdir(filepath.Join(storageState, "state.sqlite"), 0700); err != nil {
		t.Fatal(err)
	}
	result := collectWorkObservation(context.Background(), workOptions{state: storageState}, 25)
	assertObservationTiming(t, result.Diagnostics)
	assertObservationCode(t, result.Diagnostics.Components.Runtime, "timeout")
	assertObservationCode(t, result.Diagnostics.Components.Storage, "metadata_unavailable")
	if result.Complete || result.RuntimeError == "" || result.Storage.Files["database"].Bytes != nil {
		t.Fatalf("failed metadata/runtime observation became healthy: %+v", result)
	}
	if got := workObservationErrorCode(context.Background(), errors.New("timeout unauthorized forbidden")); got != "unknown" {
		t.Fatalf("classification parsed unstructured error prose: %s", got)
	}
}
