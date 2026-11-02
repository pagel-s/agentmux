package workflow

import (
	"bytes"
	"encoding/json"
	"testing"
)

// Exercise the public configuration boundary without starting any commands.
// Valid accepted plans must survive canonical serialization without changing
// their meaning; malformed and adversarial data must never panic the decoder.
func FuzzWorkflowPlanRoundTrip(f *testing.F) {
	for _, seed := range []string{
		`{"version":1,"execution":"local","repository":".","goal":"fixture","candidates":[{"id":"first","harness":"command","command":["echo","ok"]}],"checks":[{"name":"fixture","command":["true"]}]}`,
		`{"version":1,"version":2}`,
		`{"execution":"local","EXECUTION":"local"}`,
		`{"goal":"\u001b[2J"}`,
		`{"candidates":[{"id":"../escape"}]}`,
		`{"steps":[{"id":"a","depends_on":["a"]}]}`,
		`null`, `[]`, `{"\u212a":1,"K":2}`, "{\xff}",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		var plan Plan
		if DecodeJSON(data, &plan) != nil {
			return
		}
		valid := ValidatePlan(plan) == nil
		encoded, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip Plan
		if err = DecodeJSON(encoded, &roundTrip); err != nil {
			t.Fatal(err)
		}
		// Empty optional collections and nil are intentionally equivalent in
		// this schema. Canonical bytes and validation must remain stable.
		again, err := json.Marshal(roundTrip)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(encoded, again) || valid != (ValidatePlan(roundTrip) == nil) {
			t.Fatalf("configuration meaning changed after round trip")
		}
	})
}

func TestWorkflowRejectsInvalidUTF8WithoutReplacingInput(t *testing.T) {
	var plan Plan
	if err := DecodeJSON([]byte("{\"goal\":\"bad\xffvalue\"}"), &plan); err == nil {
		t.Fatal("invalid input silently replaced")
	}
}
