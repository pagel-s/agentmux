//go:build !unix

package agentauth

import "os"

// Native file provisioning is only used by the Linux Bubblewrap backend.
func ownedByController(info os.FileInfo) bool { return false }
