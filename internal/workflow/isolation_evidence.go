package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"agentmux/internal/agybridge"
)

// redactIsolationText uses a marker only when it is itself credential-free.
// A replacement can also join surrounding fragments into a selected value;
// the final check covers that case, including arbitrarily short credentials.
func redactIsolationText(values []string, text string) string {
	marker := ""
	for _, candidate := range []string{"[redacted credential]", "[redacted]"} {
		safe := true
		for _, value := range values {
			if strings.Contains(candidate, value) {
				safe = false
				break
			}
		}
		if safe {
			marker = candidate
			break
		}
	}
	for _, value := range values {
		text = strings.ReplaceAll(text, value, marker)
	}
	for _, value := range values {
		if strings.Contains(text, value) {
			return ""
		}
	}
	return text
}

func isolationEvidenceRefusal(ctx context.Context) error {
	return redactEarlyIsolationError(ctx, errors.New("workflow evidence could not be published without selected credential bytes"))
}

// redactIsolationJSON preserves unaffected JSON bytes and exact numeric tokens.
// Matching primitive values are replaced with a neutral JSON value. Object
// keys are schema, so a matching key requires refusal rather than a rename.
func redactIsolationJSON(ctx context.Context, data []byte) ([]byte, bool, error) {
	values := selectedIsolationCredentials(ctx)
	if len(values) == 0 {
		return data, false, nil
	}
	if !json.Valid(data) {
		return nil, false, isolationEvidenceRefusal(ctx)
	}
	var out bytes.Buffer
	changed := false
	for i := 0; i < len(data); {
		start := i
		if data[i] == '"' {
			i++
			for i < len(data) {
				if data[i] == '\\' {
					i += 2
					continue
				}
				if data[i] == '"' {
					i++
					break
				}
				i++
			}
			var value string
			if json.Unmarshal(data[start:i], &value) != nil {
				return nil, false, isolationEvidenceRefusal(ctx)
			}
			next := i
			for next < len(data) && strings.ContainsRune(" \n\r\t", rune(data[next])) {
				next++
			}
			clean := redactIsolationText(values, value)
			if clean != value {
				if next < len(data) && data[next] == ':' {
					return nil, false, isolationEvidenceRefusal(ctx)
				}
				encoded, _ := json.Marshal(clean)
				out.Write(encoded)
				changed = true
			} else {
				out.Write(data[start:i])
			}
			continue
		}
		if strings.ContainsRune("{}[],: \n\r\t", rune(data[i])) {
			out.WriteByte(data[i])
			i++
			continue
		}
		for i < len(data) && !strings.ContainsRune("{}[],: \n\r\t", rune(data[i])) {
			i++
		}
		token := data[start:i]
		if containsIsolationCredential(ctx, token) {
			replacement := []byte("null")
			if containsIsolationCredential(ctx, replacement) {
				replacement = []byte(`""`)
			}
			if containsIsolationCredential(ctx, replacement) {
				return nil, false, isolationEvidenceRefusal(ctx)
			}
			out.Write(replacement)
			changed = true
		} else {
			out.Write(token)
		}
	}
	clean := out.Bytes()
	if !json.Valid(clean) || containsIsolationCredential(ctx, clean) {
		return nil, false, isolationEvidenceRefusal(ctx)
	}
	return clean, changed, nil
}

var rawJSONType = reflect.TypeOf(json.RawMessage{})

// Diagnostics may be redacted; identifiers, protocol enums, verification
// outcomes, counts and schema keys must retain their meaning or be refused.
func isolationDiagnosticField(owner reflect.Type, name string) bool {
	switch owner {
	case reflect.TypeOf(CandidateResult{}):
		return name == "Result" || name == "Error"
	case reflect.TypeOf(CheckResult{}):
		return name == "Output" || name == "Error"
	case reflect.TypeOf(Report{}), reflect.TypeOf(WatchReport{}), reflect.TypeOf(WatchEntry{}), reflect.TypeOf(LabReport{}), reflect.TypeOf(LabEntry{}):
		return name == "Error"
	case reflect.TypeOf(ReviewResult{}):
		return name == "Summary"
	case reflect.TypeOf(ReviewFinding{}):
		return name == "Message"
	case reflect.TypeOf(agybridge.Evidence{}):
		return name == "Input" || name == "Text"
	}
	return false
}

func sanitizeIsolationValue(ctx context.Context, value reflect.Value, diagnostic bool) (bool, error) {
	if value.Type() == rawJSONType {
		if value.Len() == 0 {
			return false, nil
		}
		clean, changed, err := redactIsolationJSON(ctx, value.Bytes())
		if err == nil && changed {
			value.SetBytes(clean)
		}
		return changed, err
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return false, nil
		}
		return sanitizeIsolationValue(ctx, value.Elem(), diagnostic)
	case reflect.String:
		clean := redactIsolationText(selectedIsolationCredentials(ctx), value.String())
		if clean == value.String() {
			return false, nil
		}
		if !diagnostic || !value.CanSet() {
			return false, isolationEvidenceRefusal(ctx)
		}
		value.SetString(clean)
		return true, nil
	case reflect.Struct:
		changed := false
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			if !field.CanSet() {
				continue
			}
			if field.Type() == reflect.TypeOf((*Verification)(nil)) && !field.IsNil() && !verificationHasIdentity(*field.Interface().(*Verification)) {
				field.SetZero()
				continue
			}
			altered, err := sanitizeIsolationValue(ctx, field, isolationDiagnosticField(value.Type(), value.Type().Field(i).Name))
			if err != nil {
				return false, err
			}
			changed = changed || altered
		}
		if changed {
			switch current := value.Addr().Interface().(type) {
			case *CandidateResult:
				current.CredentialsRedacted = true
			case *CheckResult:
				current.CredentialsRedacted = true
			case *agybridge.Evidence:
				current.CredentialsRedacted = true
				digest := sha256.Sum256([]byte(current.Input))
				current.InputSHA256 = hex.EncodeToString(digest[:])
			}
		}
		return changed, nil
	case reflect.Slice, reflect.Array:
		changed := false
		for i := 0; i < value.Len(); i++ {
			altered, err := sanitizeIsolationValue(ctx, value.Index(i), diagnostic)
			if err != nil {
				return false, err
			}
			changed = changed || altered
		}
		return changed, nil
	case reflect.Map:
		// Retained maps contain policy or accounting metadata, never diagnostics.
		encoded, err := json.Marshal(value.Interface())
		if err != nil {
			return false, isolationEvidenceRefusal(ctx)
		}
		if _, changed, err := redactIsolationJSON(ctx, encoded); changed || err != nil {
			return false, isolationEvidenceRefusal(ctx)
		}
	}
	return false, nil
}

// sanitizeIsolationEvidence is a typed publication boundary. Work on a fresh
// copy so JSON null cannot leave a previous numeric value in the destination,
// and refused evidence cannot survive in a returned in-memory report.
func sanitizeIsolationEvidence(ctx context.Context, value any) (changed bool, err error) {
	if len(selectedIsolationCredentials(ctx)) == 0 {
		return false, nil
	}
	destination := reflect.ValueOf(value)
	if destination.Kind() != reflect.Pointer || destination.IsNil() {
		return false, isolationEvidenceRefusal(ctx)
	}
	defer func() {
		if err != nil {
			destination.Elem().SetZero()
		}
	}()
	encoded, err := json.Marshal(value)
	if err != nil {
		return false, isolationEvidenceRefusal(ctx)
	}
	copy := reflect.New(destination.Elem().Type())
	if err := json.Unmarshal(encoded, copy.Interface()); err != nil {
		return false, isolationEvidenceRefusal(ctx)
	}
	changed, err = sanitizeIsolationValue(ctx, copy.Elem(), false)
	if err != nil {
		return false, err
	}
	clean, encodeErr := json.Marshal(copy.Interface())
	if encodeErr != nil || containsIsolationCredential(ctx, clean) {
		return false, isolationEvidenceRefusal(ctx)
	}
	destination.Elem().Set(copy.Elem())
	return changed, nil
}

func saveIsolationJSON(ctx context.Context, path string, value any) error {
	if _, err := sanitizeIsolationEvidence(ctx, value); err != nil {
		return err
	}
	publication := value
	if report, ok := value.(*Report); ok {
		publication = compactReport(*report)
	}
	data, err := json.MarshalIndent(publication, "", "  ")
	if err != nil {
		return isolationEvidenceRefusal(ctx)
	}
	data = append(data, '\n')
	// Check the exact final bytes, including JSON escaping and formatting, before
	// creating even a temporary file. A refusal leaves previous safe evidence.
	_, changed, checkErr := redactIsolationJSON(ctx, data)
	if containsIsolationCredential(ctx, []byte(path)) || changed || checkErr != nil {
		reflect.ValueOf(value).Elem().SetZero()
		return isolationEvidenceRefusal(ctx)
	}
	return redactEarlyIsolationError(ctx, saveJSONBytes(path, data))
}

func writeIsolationNativeEvents(ctx context.Context, path string, events []json.RawMessage) (string, error) {
	var data []byte
	for _, event := range events {
		if _, changed, err := redactIsolationJSON(ctx, event); !json.Valid(event) || changed || err != nil {
			return "", isolationEvidenceRefusal(ctx)
		}
		data = append(data, event...)
		data = append(data, '\n')
	}
	if containsIsolationCredential(ctx, []byte(path)) || containsIsolationCredential(ctx, data) {
		return "", isolationEvidenceRefusal(ctx)
	}
	if err := writeNewPrivate(path, data); err != nil {
		return "", redactEarlyIsolationError(ctx, err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// A failed fingerprint can return no revision at all. Such a failure is not a
// verification record and must not be attached as one to a failed run report.
func verificationHasIdentity(v Verification) bool {
	if v.Repository == "" || !objectID(v.Tree) || !digestID(v.ContentSHA256) {
		return false
	}
	if v.Storage == StoragePrivate && (v.Store == "" || !digestID(v.IgnoreSHA256)) {
		return false
	}
	return true
}
