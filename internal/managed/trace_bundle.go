package managed

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const MaxTraceBundleBytes = 128 << 20

// TraceBundle is an owner-signed portable attestation of a locally verified
// HMAC trace. It contains no HMAC key, signing key, credentials or state dump.
// The signature authenticates the pinned exporter, not a provider or task truth.
type TraceBundle struct {
	Version   int                `json:"version"`
	Payload   TraceBundlePayload `json:"payload"`
	PublicKey string             `json:"public_key"`
	Signature string             `json:"signature"`
}
type TraceBundlePayload struct {
	Session    string       `json:"session"`
	Agent      string       `json:"agent"`
	ExportedAt string       `json:"exported_at"`
	Summary    TraceSummary `json:"summary"`
	SHA256     string       `json:"sha256"`
	JSONL      string       `json:"jsonl"`
}
type TraceBundleVerification struct {
	Fingerprint string       `json:"fingerprint"`
	Session     string       `json:"session"`
	Agent       string       `json:"agent"`
	Summary     TraceSummary `json:"summary"`
	Trust       string       `json:"trust"`
}

func traceSigningKey(dir string) (ed25519.PrivateKey, error) {
	path := filepath.Join(dir, "portable-signing.key")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		if err := writeTracePrivate(path, private.Seed()); os.IsExist(err) {
			return traceSigningKey(dir)
		} else if err != nil {
			return nil, err
		}
		return private, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, errors.New("portable trace signing key must be a private regular file")
	}
	seed, err := os.ReadFile(path)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("portable trace signing key invalid")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func writeTracePrivate(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

// ExportTraceBundle authenticates the exact captured bytes before signing.
// The returned fingerprint must be shared through an independently trusted
// channel; accepting a key merely because it is embedded is not authentication.
func ExportTraceBundle(state, agent, id, expectedMAC, output string) (TraceBundleVerification, error) {
	var result TraceBundleVerification
	if _, err := VerifyTrace(state, agent, id, expectedMAC); err != nil {
		return result, err
	}
	path := filepath.Join(state, "traces", agent, id+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return result, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxTraceBundleBytes/2+1))
	f.Close()
	if err != nil || len(raw) > MaxTraceBundleBytes/2 {
		return result, errors.New("trace exceeds portable bundle size allocation")
	}
	key, err := os.ReadFile(filepath.Join(state, "traces", "trace.key"))
	if err != nil {
		return result, err
	}
	summary, err := verifyTraceReader(bytes.NewReader(raw), key, agent, id, expectedMAC)
	if err != nil {
		return result, err
	}
	store, err := Open(state)
	if err != nil {
		return result, err
	}
	session := store.Config().Session
	store.Close()
	private, err := traceSigningKey(filepath.Join(state, "traces"))
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(raw)
	payload := TraceBundlePayload{Session: session, Agent: agent, ExportedAt: time.Now().UTC().Format(time.RFC3339Nano), Summary: summary, SHA256: hex.EncodeToString(sum[:]), JSONL: string(raw)}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return result, err
	}
	public := private.Public().(ed25519.PublicKey)
	bundle := TraceBundle{Version: 1, Payload: payload, PublicKey: base64.StdEncoding.EncodeToString(public), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, canonical))}
	encoded, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil || len(encoded) > MaxTraceBundleBytes {
		return result, errors.New("portable trace bundle exceeds size allocation")
	}
	if err := writeTracePrivate(output, append(encoded, '\n')); err != nil {
		return result, err
	}
	return traceBundleResult(bundle, public), nil
}

func traceBundleResult(b TraceBundle, public []byte) TraceBundleVerification {
	sum := sha256.Sum256(public)
	return TraceBundleVerification{Fingerprint: hex.EncodeToString(sum[:]), Session: b.Payload.Session, Agent: b.Payload.Agent, Summary: b.Payload.Summary, Trust: "signature verified against explicitly pinned owner-exporter key; not provider attestation"}
}

func VerifyTraceBundle(path, trustedFingerprint string) (TraceBundleVerification, error) {
	var result TraceBundleVerification
	if len(trustedFingerprint) != 64 || strings.Trim(trustedFingerprint, "0123456789abcdef") != "" {
		return result, errors.New("--trust requires the owner's independently obtained public-key SHA256 fingerprint")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxTraceBundleBytes {
		return result, errors.New("trace bundle must be a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer f.Close()
	var bundle TraceBundle
	d := json.NewDecoder(io.LimitReader(f, MaxTraceBundleBytes+1))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&bundle) != nil || d.Decode(&extra) != io.EOF || bundle.Version != 1 {
		return result, errors.New("invalid trace bundle framing or version")
	}
	public, err := base64.StdEncoding.DecodeString(bundle.PublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return result, errors.New("invalid trace bundle public key")
	}
	result = traceBundleResult(bundle, public)
	if result.Fingerprint != trustedFingerprint {
		return TraceBundleVerification{}, errors.New("trace bundle signer differs from pinned owner key")
	}
	signature, err := base64.StdEncoding.DecodeString(bundle.Signature)
	canonical, _ := json.Marshal(bundle.Payload)
	if err != nil || !ed25519.Verify(public, canonical, signature) {
		return TraceBundleVerification{}, errors.New("trace bundle signature authentication failed")
	}
	sum := sha256.Sum256([]byte(bundle.Payload.JSONL))
	if hex.EncodeToString(sum[:]) != bundle.Payload.SHA256 || !bundle.Payload.Summary.Complete || bundle.Payload.Summary.Bytes != int64(len(bundle.Payload.JSONL)) {
		return TraceBundleVerification{}, errors.New("trace bundle digest or completion summary invalid")
	}
	return result, nil
}
