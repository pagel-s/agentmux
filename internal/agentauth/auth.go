// Package agentauth prepares explicitly selected, private native-login snapshots.
// It never loads native configuration, discovers credentials implicitly, changes
// a host login, or puts credential values in configuration or error messages.
package agentauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agentmux/internal/managedconfig"
)

const maxFileBytes = 1 << 20

type File struct {
	Source     string
	Target     string
	Descriptor *os.File
}

type Prepared struct {
	Files   []File
	Secrets []string
	dir     string
}

func (p *Prepared) Close() {
	if p != nil {
		for _, file := range p.Files {
			if file.Descriptor != nil {
				_ = file.Descriptor.Close()
			}
		}
	}
	if p != nil && p.dir != "" {
		_ = os.RemoveAll(p.dir)
	}
}

// Prepare filters an owner-selected credential file and creates a private
// read-only-mountable snapshot. The worker may read its own credentials; this is
// isolation between workers, not a credential-hiding proxy. Native refreshes
// cannot update the read-only snapshot or the owner's original cache.
func Prepare(name string, auth managedconfig.NativeAuth) (*Prepared, error) {
	return prepare(name, auth, false)
}

// PrepareAnonymous keeps filtered credentials only in a sealed anonymous Linux
// file. Controller death closes its descriptor; there is no pathname-backed
// credential snapshot to strand after SIGKILL. The original login is untouched.
func PrepareAnonymous(name string, auth managedconfig.NativeAuth) (*Prepared, error) {
	return prepare(name, auth, true)
}

func prepare(name string, auth managedconfig.NativeAuth, anonymous bool) (*Prepared, error) {
	p := &Prepared{}
	if auth.File == "" && auth.Provider == "" {
		return p, nil
	}
	if !filepath.IsAbs(auth.File) {
		return nil, errors.New("native auth requires an absolute credential-file path")
	}
	canonical, err := filepath.EvalSymlinks(auth.File)
	if err != nil || canonical != filepath.Clean(auth.File) {
		return nil, errors.New("native auth source must be accessible without symlink components")
	}
	info, err := os.Lstat(auth.File)
	if err != nil || !info.Mode().IsRegular() || (info.Mode().Perm() != 0600 && info.Mode().Perm() != 0400) || !ownedByController(info) {
		return nil, errors.New("native auth source must be an owned private regular file (mode 0600 or 0400), without hardlinks or symlinks")
	}
	f, err := openCredentialFile(auth.File)
	if err != nil {
		return nil, errors.New("native auth source could not be opened")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || (opened.Mode().Perm() != 0600 && opened.Mode().Perm() != 0400) || !ownedByController(opened) {
		return nil, errors.New("native auth source changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil || len(data) > maxFileBytes {
		return nil, errors.New("native auth source exceeds the bounded credential-file size or could not be read")
	}
	ended, statErr := f.Stat()
	pathEnd, pathErr := os.Lstat(auth.File)
	if statErr != nil || pathErr != nil || !os.SameFile(info, ended) || !os.SameFile(info, pathEnd) || ended.Size() != info.Size() || int64(len(data)) != info.Size() || ended.Mode() != info.Mode() || pathEnd.Mode() != info.Mode() || !ended.ModTime().Equal(info.ModTime()) || !pathEnd.ModTime().Equal(info.ModTime()) {
		return nil, errors.New("native auth source changed while reading")
	}
	var source map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	if dec.Decode(&source) != nil || source == nil {
		return nil, errors.New("native auth source must be a JSON credential object")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, errors.New("native auth source must contain exactly one JSON object")
	}
	var selected map[string]json.RawMessage
	target := ""
	switch name {
	case "claude":
		oauth, ok := source["claudeAiOauth"]
		if !ok || !nonemptyField(oauth, "accessToken") {
			return nil, errors.New("Claude auth source has no supported OAuth credential")
		}
		selected = map[string]json.RawMessage{"claudeAiOauth": oauth}
		target = "/home/agent/.claude/.credentials.json"
	case "codex":
		if !nonemptyField(source["tokens"], "access_token") && !nonemptyString(source["OPENAI_API_KEY"]) {
			return nil, errors.New("Codex auth source has no supported file credential")
		}
		selected = pick(source, "auth_mode", "OPENAI_API_KEY", "tokens", "last_refresh")
		target = "/home/agent/.codex/auth.json"
	case "pi":
		credential, ok := source[auth.Provider]
		if auth.Provider == "" || !ok || (!nonemptyField(credential, "key") && !nonemptyField(credential, "access")) {
			return nil, errors.New("Pi auth source has no credential for the explicitly selected provider")
		}
		var entry map[string]json.RawMessage
		_ = json.Unmarshal(credential, &entry)
		var kind, key string
		_ = json.Unmarshal(entry["type"], &kind)
		_ = json.Unmarshal(entry["key"], &key)
		if kind == "api_key" && nonemptyString(entry["key"]) && !strings.HasPrefix(strings.TrimSpace(key), "!") {
			credential, _ = json.Marshal(pick(entry, "type", "key"))
		} else if kind == "oauth" && nonemptyString(entry["access"]) {
			credential, _ = json.Marshal(pick(entry, "type", "access", "refresh", "expires", "accountId"))
		} else {
			return nil, errors.New("Pi auth must be an OAuth credential or literal API key; command-backed credentials are unsupported")
		}
		selected = map[string]json.RawMessage{auth.Provider: credential}
		target = "/home/agent/.pi/agent/auth.json"
	default:
		return nil, errors.New("native auth is unsupported for this harness")
	}
	filtered, err := json.Marshal(selected)
	if err != nil {
		return nil, errors.New("native auth credential object could not be prepared")
	}
	if anonymous {
		descriptor, source, err := anonymousCredential(filtered)
		if err != nil {
			return nil, err
		}
		p.Files = []File{{Source: source, Target: target, Descriptor: descriptor}}
		var values any
		_ = json.Unmarshal(filtered, &values)
		p.Secrets = secretStrings(values)
		return p, nil
	}
	p.dir, err = os.MkdirTemp("", "agentmux-auth-*")
	if err != nil {
		return nil, errors.New("private native auth snapshot directory could not be created")
	}
	file := filepath.Join(p.dir, "credential.json")
	if err := os.WriteFile(file, filtered, 0600); err != nil {
		p.Close()
		return nil, errors.New("private native auth snapshot could not be written")
	}
	p.Files = []File{{Source: file, Target: target}}
	// Exact known values are redacted from worker-published results. This does
	// not attempt to prevent deliberate exfiltration or arbitrary encodings.
	var values any
	_ = json.Unmarshal(filtered, &values)
	p.Secrets = secretStrings(values)
	return p, nil
}

func pick(source map[string]json.RawMessage, keys ...string) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage)
	for _, key := range keys {
		if value, ok := source[key]; ok {
			result[key] = value
		}
	}
	return result
}

func nonemptyField(raw json.RawMessage, key string) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && nonemptyString(object[key])
}

func nonemptyString(raw json.RawMessage) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != ""
}

func secretStrings(value any) []string {
	var result []string
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			name := strings.ToLower(key)
			if text, ok := item.(string); ok && text != "" && (strings.Contains(name, "token") || strings.HasSuffix(name, "_key") || name == "key" || name == "access" || name == "refresh" || name == "account_id" || name == "accountid") {
				result = append(result, text)
			} else {
				result = append(result, secretStrings(item)...)
			}
		}
	case []any:
		for _, item := range v {
			result = append(result, secretStrings(item)...)
		}
	}
	return result
}
