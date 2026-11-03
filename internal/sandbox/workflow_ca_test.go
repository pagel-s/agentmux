package sandbox

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWorkflowPublicTrustRejectsNoncertificateDataAndOverridesAmbientTrust(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux workflow certificate mount")
	}
	root, workspace, options := workflowBoundaryFixture(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	path := filepath.Join(root, "public-ca.pem")
	if err := os.WriteFile(path, certificate, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := workflowCertificateSource(path, workspace, options.StateDir); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{
		append([]byte("unrelated-private-prefix\n"), certificate...),
		append(append([]byte(nil), certificate...), []byte("-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----\n")...),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"private-metadata": "must-not-mount"}, Bytes: der}),
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := workflowCertificateSource(path, workspace, options.StateDir); err == nil {
			t.Fatal("non-public trust bytes were accepted for a worker mount")
		}
	}
	env := workflowCertificateEnvironment([]string{"PATH=/usr/bin", "SSL_CERT_FILE=/owner/private-path", "CODEX_CA_CERTIFICATE=/owner/pinned-ca", "NODE_USE_ENV_PROXY=0"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "/owner/") || !strings.Contains(joined, "CODEX_CA_CERTIFICATE="+workflowCertificateTarget) || !strings.Contains(joined, "NODE_EXTRA_CA_CERTS="+workflowCertificateTarget) {
		t.Fatal("worker TLS trust inherited an unrelated host source")
	}
	if strings.Count(joined, "NODE_USE_ENV_PROXY=") != 1 || !strings.Contains(joined, "NODE_USE_ENV_PROXY=1") {
		t.Fatal("Node fetch could bypass the selected proxy configuration")
	}
}
