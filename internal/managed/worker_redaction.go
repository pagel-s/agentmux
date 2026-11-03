package managed

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"

	"agentmux/internal/harness"
)

var errWorkerEvidenceRedaction = errors.New("native evidence contains known credential values in a field that cannot be safely retained")

func workerSecrets(req harness.Request, token string, nativeSecrets ...string) []string {
	values := append([]string{token}, nativeSecrets...)
	if req.APIKeyEnv != "" {
		value, ok := req.Env[req.APIKeyEnv]
		if !ok {
			value = os.Getenv(req.APIKeyEnv)
		}
		values = append(values, value)
	}
	for key, value := range req.Env {
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.HasSuffix(upper, "_TOKEN") || strings.HasSuffix(upper, "_KEY") {
			values = append(values, value)
		}
	}
	seen := map[string]bool{}
	secrets := []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			secrets = append(secrets, value)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}

func containsWorkerSecret(values []string, text string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

// Known-value redaction is best effort, not protection against arbitrary
// encodings. Never let the replacement marker or joined fragments reintroduce
// a selected value, including unusually short credentials.
func redactWorkerText(values []string, text string) string {
	marker := "[redacted]"
	if containsWorkerSecret(values, marker) {
		marker = ""
	}
	for _, value := range values {
		text = strings.ReplaceAll(text, value, marker)
	}
	if containsWorkerSecret(values, text) {
		return ""
	}
	return text
}

// Inspect decoded JSON strings, not their wire spelling: JSON escapes and HTML
// escapes otherwise hide known values from plain substring replacement. Keep
// unaffected bytes and exact accounting numbers. Keys describe schema, so
// credential-bearing keys are refused rather than renamed or merged.
func workerJSONResult(raw json.RawMessage, req harness.Request, token string, maxBytes int, nativeSecrets ...string) (json.RawMessage, error) {
	if len(raw) > maxBytes || !json.Valid(raw) {
		return nil, errWorkerEvidenceRedaction
	}
	values := workerSecrets(req, token, nativeSecrets...)
	if len(values) == 0 {
		return raw, nil
	}
	var out bytes.Buffer
	for i := 0; i < len(raw); {
		start := i
		if raw[i] == '"' {
			i++
			for i < len(raw) {
				if raw[i] == '\\' {
					i += 2
					continue
				}
				if raw[i] == '"' {
					i++
					break
				}
				i++
			}
			var value string
			if json.Unmarshal(raw[start:i], &value) != nil {
				return nil, errWorkerEvidenceRedaction
			}
			clean := redactWorkerText(values, value)
			if clean != value {
				next := i
				for next < len(raw) && strings.ContainsRune(" \n\r\t", rune(raw[next])) {
					next++
				}
				if next < len(raw) && raw[next] == ':' {
					return nil, errWorkerEvidenceRedaction
				}
				encoded, _ := json.Marshal(clean)
				out.Write(encoded)
			} else {
				out.Write(raw[start:i])
			}
			continue
		}
		if strings.ContainsRune("{}[],: \n\r\t", rune(raw[i])) {
			out.WriteByte(raw[i])
			i++
			continue
		}
		for i < len(raw) && !strings.ContainsRune("{}[],: \n\r\t", rune(raw[i])) {
			i++
		}
		primitive := raw[start:i]
		if containsWorkerSecret(values, string(primitive)) {
			replacement := "null"
			if containsWorkerSecret(values, replacement) {
				replacement = `""`
			}
			if containsWorkerSecret(values, replacement) {
				return nil, errWorkerEvidenceRedaction
			}
			out.WriteString(replacement)
		} else {
			out.Write(primitive)
		}
	}
	clean := out.Bytes()
	if len(clean) > maxBytes || !json.Valid(clean) || containsWorkerSecret(values, string(clean)) {
		return nil, errWorkerEvidenceRedaction
	}
	return clean, nil
}
