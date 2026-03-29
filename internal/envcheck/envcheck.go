package envcheck

import (
	"fmt"
	"os"
	"os/exec"

	"agentmux/internal/config"
)

func Check(reqs config.Requirements) []string {
	var missing []string

	for _, bin := range reqs.Binaries {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, fmt.Sprintf("binary %q not found in PATH", bin))
		}
	}

	for _, env := range reqs.Env {
		if val := os.Getenv(env); val == "" {
			missing = append(missing, fmt.Sprintf("environment variable %q is not set", env))
		}
	}

	return missing
}
