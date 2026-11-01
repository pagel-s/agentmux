//go:build !unix

package claude

import (
	"context"
	"errors"
	"os"
	"time"
)

func ownedByCurrentUser(os.FileInfo) bool { return false }
func helperLock(context.Context, string, string, string, time.Duration) (func(), error) {
	return nil, errors.New("Claude native inbox bridge currently requires Unix sockets")
}
