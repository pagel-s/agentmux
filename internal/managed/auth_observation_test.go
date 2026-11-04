package managed

import (
	"context"
	"testing"
)

func TestCancelledAuthenticationIsNotReportedAsInvalidCredential(t *testing.T) {
	s := testStore(t, testConfig())
	owner, err := LoadCredential(CredentialPath(s.dir, ""))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, problem := s.Call(ctx, owner.Token, Request{Operation: "identity"}); problem == nil || problem.Code != "cancelled" {
		t.Fatalf("an expired observation became a credential diagnosis: %+v", problem)
	}
	if _, problem := s.Call(context.Background(), "invalid-credential", Request{Operation: "identity"}); problem == nil || problem.Code != "unauthorized" {
		t.Fatalf("real authentication failure lost its classification: %+v", problem)
	}
}
