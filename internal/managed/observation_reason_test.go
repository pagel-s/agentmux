package managed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type operationalReasonTransport func(*http.Request) (*http.Response, error)

func (f operationalReasonTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOperationalReasonClientAndLegacyWireCompatibility(t *testing.T) {
	s := testStore(t, testConfig())
	owner := credential(t, s, "")
	_, _, slot, _, problem := s.operationalReader()
	if problem != nil {
		t.Fatal(problem)
	}
	slot <- struct{}{}
	defer func() { <-slot }()
	for _, header := range []string{"", "1", "unknown"} {
		r := httptest.NewRequest("POST", "http://agentmux/v1/call", strings.NewReader(`{"operation":"observation.get","args":{}}`))
		r.Header.Set("Authorization", "Bearer "+owner.Token)
		r.Header.Set(operationalDiagnosticsHeader, header)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if header == "1" {
			var response wireResponse
			if err := strictJSON(w.Body.Bytes(), &response); err != nil || response.Error == nil || response.Error.ReasonCode != "operational_read_busy" {
				t.Fatalf("opted-in observer lost typed cause: %s %v", w.Body, err)
			}
			continue
		}
		// Use the exact old strict Fault shape, so this catches accidental
		// wire additions that an old client would turn into transport failure.
		var legacy struct {
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := strictJSON(w.Body.Bytes(), &legacy); err != nil || legacy.Error == nil || legacy.Error.Code != "unavailable" {
			t.Fatalf("legacy fault contract changed: %s %v", w.Body, err)
		}
	}
	client := NewClient(owner)
	client.http.Transport = operationalReasonTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get(operationalDiagnosticsHeader) != "1" {
			t.Error("new operational client did not opt into additive diagnostics")
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Result(), nil
	})
	if _, problem := client.Call(context.Background(), Request{Operation: "observation.get"}); problem == nil || problem.Code != "unavailable" || problem.ReasonCode != "operational_read_busy" || problem.Error() != "unavailable: an operational observation is already running; refresh after it finishes" || problem.ErrorCode() != "unavailable" {
		t.Fatalf("typed transport changed original fault behavior: %+v", problem)
	}
}

func TestOperationalReasonClientBoundsUntrustedFields(t *testing.T) {
	for _, fixture := range []struct{ code, reason, want string }{
		{"unavailable", "operational_read_busy", "operational_read_busy"},
		{"cancelled", "query_budget_exhausted", "query_budget_exhausted"},
		{"cancelled", "shutdown", "shutdown"},
		{"unavailable", "shutdown", "shutdown"},
		{"cancelled", "", ""}, // Older controllers do not record subcauses.
		{"unavailable", "private-server-reason", ""},
		{"forbidden", "query_budget_exhausted", ""},
		{"cancelled", "operational_read_busy", ""},
		{"unavailable", "query_budget_exhausted", ""},
	} {
		t.Run(fixture.code+"/"+fixture.reason, func(t *testing.T) {
			client := NewClient(Credential{})
			client.http.Transport = operationalReasonTransport(func(*http.Request) (*http.Response, error) {
				raw, _ := json.Marshal(wireResponse{Error: &Fault{Code: fixture.code, Message: "unchanged public message", ReasonCode: fixture.reason}})
				return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}}, nil
			})
			_, problem := client.Call(context.Background(), Request{Operation: "observation.get"})
			if problem == nil || problem.Code != fixture.code || problem.ReasonCode != fixture.want || problem.Error() != fixture.code+": unchanged public message" {
				t.Fatalf("untrusted reason was echoed or old fault reclassified: %+v", problem)
			}
		})
	}
}

func TestOperationalReasonCallerDeadlineIsNotServerQueryBudget(t *testing.T) {
	for _, request := range []Request{{Operation: "observation.get"}, {Operation: "native.status", Args: json.RawMessage(`{"summary":true}`)}} {
		t.Run(request.Operation, func(t *testing.T) {
			s := testStore(t, testConfig())
			owner := credential(t, s, "")
			_, reader, _, _, problem := s.operationalReader()
			if problem != nil {
				t.Fatal(problem)
			}
			held, err := reader.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if _, problem := s.Call(ctx, owner.Token, request); problem == nil || problem.Code != "cancelled" || problem.ReasonCode != "" {
				t.Fatalf("caller deadline was mislabeled as the 250ms server budget: %+v", problem)
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if _, problem := s.Call(context.Background(), owner.Token, request); problem == nil || problem.Code != "unavailable" || problem.ReasonCode != "shutdown" {
				t.Fatalf("closed store lost shutdown reason: %+v", problem)
			}
		})
	}
}

func TestOperationalReasonPublicationCancellationClassification(t *testing.T) {
	s := testStore(t, testConfig())
	owner := credential(t, s, "")
	prior, err := authenticateWith(context.Background(), s.db, owner.Token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeoutCause(context.Background(), time.Millisecond, errOperationalQueryBudget)
	defer cancel()
	<-ctx.Done()
	if problem := currentOperationalCredential(ctx, s.db, owner.Token, prior); problem == nil || problem.Code != "cancelled" {
		t.Fatalf("publication recheck did not exercise cancellation: %+v", problem)
	}
	if problem := operationalReadFault(ctx, context.Background(), ctx.Err()); problem.Code != "cancelled" || problem.ReasonCode != "query_budget_exhausted" {
		t.Fatalf("publication-stage budget reason was lost: %+v", problem)
	}
	lifetime, closeStore := context.WithCancel(context.Background())
	closeStore()
	if problem := operationalReadFault(ctx, lifetime, ctx.Err()); problem.Code != "cancelled" || problem.ReasonCode != "shutdown" {
		t.Fatalf("publication-stage store shutdown was lost: %+v", problem)
	}
}
