//go:build !linux

package sandbox

import "errors"

func workflowCertificateSource(string, string, string) (string, error) {
	return "", errors.New("inspected workflow TLS requires Linux filesystem guarantees")
}
