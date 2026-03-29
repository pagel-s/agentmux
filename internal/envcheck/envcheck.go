package envcheck

import (
	"os"
	"os/exec"

	"agentmux/internal/config"
)

type Report struct {
	MissingBinaries []string
	MissingEnv      []string
	MissingParams   []string
}

func (r Report) HasMissing() bool {
	return len(r.MissingBinaries) > 0 || len(r.MissingEnv) > 0 || len(r.MissingParams) > 0
}

func Check(reqs config.Requirements, params map[string]string) Report {
	var report Report

	for _, bin := range reqs.Binaries {
		if _, err := exec.LookPath(bin); err != nil {
			report.MissingBinaries = append(report.MissingBinaries, bin)
		}
	}

	for _, env := range reqs.Env {
		if val := os.Getenv(env); val == "" {
			report.MissingEnv = append(report.MissingEnv, env)
		}
	}

	for _, p := range reqs.Params {
		if _, ok := params[p]; !ok {
			report.MissingParams = append(report.MissingParams, p)
		}
	}

	return report
}
