//go:build !linux

package sandbox

import (
	"context"
	"errors"
)

func probeWorkflowCredentialFD(ctx context.Context) error {
	return errors.New("anonymous credential namespace requires Linux")
}
