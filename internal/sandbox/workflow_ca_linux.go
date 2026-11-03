//go:build linux

package sandbox

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func workflowCertificateSource(path, workspace, state string) (string, error) {
	source, err := realPath(path, "workflow public egress CA", false)
	if err != nil || source != path || containsPath(workspace, source) || containsPath(state, source) {
		return "", errors.New("inspected egress requires one exact external public CA file")
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("public egress CA must be a private regular file")
	}
	fd, err := unix.Open(source, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", errors.New("public egress CA could not be opened safely")
	}
	file := os.NewFile(uintptr(fd), "agentmux-public-egress-ca")
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return "", errors.New("public egress CA changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return "", errors.New("public egress CA exceeds its certificate bound")
	}
	ended, err := file.Stat()
	pathEnd, pathErr := os.Lstat(source)
	if err != nil || pathErr != nil || !os.SameFile(info, pathEnd) || ended.Size() != info.Size() || !ended.ModTime().Equal(info.ModTime()) || pathEnd.Mode() != info.Mode() {
		return "", errors.New("public egress CA changed while reading")
	}
	data = bytes.TrimSpace(data)
	block, remaining := pem.Decode(data)
	if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) || block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(remaining)) != 0 {
		return "", errors.New("egress trust file must contain only one public CA certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 || cert.CheckSignatureFrom(cert) != nil {
		return "", errors.New("egress trust file is not a self-signed public CA")
	}
	return source, nil
}
