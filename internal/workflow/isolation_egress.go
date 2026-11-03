package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"agentmux/internal/egress"
)

// Full workflows always inspect the HTTP authority inside HTTPS; legacy
// opaque CONNECT/SNI mode is never a fallback. Only the ephemeral public CA is
// written for a read-only worker mount. Its private signing key stays in memory.
func startIsolatedEgress(ctx context.Context, socket string, allow []egress.Endpoint) (*egress.Server, string, error) {
	server, err := egress.StartStrict(ctx, socket, allow, egress.StrictOptions{})
	if err != nil {
		return nil, "", err
	}
	certificate := filepath.Join(filepath.Dir(socket), "public-ca.pem")
	file, err := os.OpenFile(certificate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		server.Close()
		return nil, "", errors.New("isolated egress public CA could not be retained")
	}
	data := server.PublicCertificate()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if len(data) == 0 || writeErr != nil || closeErr != nil {
		server.Close()
		os.Remove(certificate)
		return nil, "", errors.New("isolated egress public CA could not be prepared")
	}
	return server, certificate, nil
}
