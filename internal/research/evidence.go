package research

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

const decisionProtocol = "agentmux-research-strict-json-v1"

// ExecutionEvidence freezes executable entrypoints, not provider weights, all
// imported dependencies, or a host owner's ability to modify the machine.
type ExecutionEvidence struct {
	Version  int                  `json:"version"`
	Protocol string               `json:"decision_protocol"`
	Runner   ExecutableEvidence   `json:"runner"`
	Worker   ExecutableEvidence   `json:"worker_and_broker"`
	Harness  []ExecutableEvidence `json:"harness_entrypoints"`
}

type ExecutableEvidence struct {
	Actor  string `json:"actor,omitempty"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type executionGuard struct {
	worker   string
	config   managedconfig.Config
	expected ExecutionEvidence
}

func fingerprint(path, actor string) (ExecutableEvidence, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ExecutableEvidence{}, errors.New("research executable entrypoint is unavailable")
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return ExecutableEvidence{}, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return ExecutableEvidence{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ExecutableEvidence{}, errors.New("research executable entrypoint must be a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ExecutableEvidence{}, err
	}
	return ExecutableEvidence{Actor: actor, Path: resolved, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func snapshotExecution(worker string, cfg managedconfig.Config) (ExecutionEvidence, error) {
	result := ExecutionEvidence{Version: 1, Protocol: decisionProtocol}
	runner, err := os.Executable()
	if err != nil {
		return result, err
	}
	// Linux's proc handle names the running inode even if its pathname was
	// rebuilt. The pathname remains useful metadata; the digest names the image.
	result.Runner, err = fingerprint(runner, "")
	if err != nil {
		return result, err
	}
	if _, e := os.Stat("/proc/self/exe"); e == nil {
		image, e := os.Open("/proc/self/exe")
		if e != nil {
			return result, e
		}
		hash := sha256.New()
		_, e = io.Copy(hash, image)
		image.Close()
		if e != nil {
			return result, e
		}
		result.Runner.SHA256 = hex.EncodeToString(hash.Sum(nil))
	}
	result.Worker, err = fingerprint(worker, "")
	if err != nil {
		return result, err
	}
	definitions := map[string]managedconfig.Agent{}
	for id, a := range cfg.Agents {
		definitions[id] = a
	}
	if cfg.Spawn.Enabled {
		for id, a := range cfg.Spawn.Templates {
			definitions["template:"+id] = a
		}
	}
	ids := make([]string, 0, len(definitions))
	for id := range definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := definitions[id]
		binary := a.Harness
		if binary == "antigravity" {
			binary = "agy"
		}
		if len(a.Command) > 0 {
			binary = a.Command[0]
		}
		path, err := researchExecutable(binary, a.Env)
		if err != nil {
			return result, fmt.Errorf("research harness entrypoint unavailable for %s", id)
		}
		entry, err := fingerprint(path, id)
		if err != nil {
			return result, err
		}
		result.Harness = append(result.Harness, entry)
		var arguments []string
		if len(a.Command) > 1 {
			arguments = a.Command[1:]
		}
		for i, argument := range arguments {
			if !filepath.IsAbs(argument) || argument == a.Auth.File {
				continue
			}
			// Native auth can change through refresh. Never fingerprint its
			// contents, including an alias of the configured source file.
			if a.Auth.File != "" {
				auth, e := filepath.EvalSymlinks(a.Auth.File)
				arg, e2 := filepath.EvalSymlinks(argument)
				if e == nil && e2 == nil && auth == arg {
					continue
				}
			}
			info, e := os.Stat(argument)
			if os.IsNotExist(e) || e == nil && !info.Mode().IsRegular() {
				continue
			}
			if e != nil {
				return result, errors.New("research argv entrypoint cannot be inspected")
			}
			entry, err := fingerprint(argument, fmt.Sprintf("%s:argv:%d", id, i+1))
			if err != nil {
				return result, err
			}
			result.Harness = append(result.Harness, entry)
		}
	}
	return result, nil
}

// Match workerExecutable's explicit-PATH lookup. Research replaces Workdir for
// each trial, so relative executable paths cannot name a frozen entrypoint.
func researchExecutable(binary string, env map[string]string) (string, error) {
	if strings.ContainsRune(binary, filepath.Separator) {
		if !filepath.IsAbs(binary) {
			return "", errors.New("research executable paths containing a separator must be absolute")
		}
		return binary, nil
	}
	path := os.Getenv("PATH")
	if value, ok := env["PATH"]; ok {
		path = value
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, binary)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func (g *executionGuard) check() error {
	actual, err := snapshotExecution(g.worker, g.config)
	if err != nil || !reflect.DeepEqual(actual, g.expected) {
		return errors.New("research execution fingerprint changed or is unavailable; no further turns may start in this study")
	}
	return nil
}

func requireNativeEvidence(m Manifest) error {
	definitions := map[string]managedconfig.Agent{}
	for id, a := range m.Config.Agents {
		definitions[id] = a
	}
	if m.Population.Mode == "dynamic" {
		for id, a := range m.Config.Spawn.Templates {
			definitions["template:"+id] = a
		}
	}
	for id, a := range definitions {
		if a.Harness != "command" && !a.Trace.Enabled {
			return fmt.Errorf("research native actor %s requires trace.enabled:true before new turns; completed historical data remains readable", id)
		}
	}
	return nil
}

func verifyNativeTaskTrace(state, actor string, task managed.Task, agent managedconfig.Agent) error {
	if agent.Harness == "command" {
		return nil
	}
	anchor := task.Trace
	if !agent.Trace.Enabled || anchor == nil || !anchor.Complete || anchor.MAC == "" || anchor.Records < 2 || anchor.Bytes < 1 || anchor.Bytes > agent.Trace.Normalized().MaxTurnBytes || !strings.HasPrefix(anchor.ID, task.ID+"-") {
		return errors.New("research native task lacks complete task-bound trace evidence; attempt interrupted without replay")
	}
	verified, err := managed.VerifyTrace(state, actor, anchor.ID, anchor.MAC)
	if err != nil || !reflect.DeepEqual(verified, *anchor) {
		return errors.New("research native task trace does not match its authenticated anchor; attempt interrupted without replay")
	}
	return nil
}

func compactDigest(raw json.RawMessage) (string, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", err
	}
	hash := sha256.Sum256(compact.Bytes())
	return hex.EncodeToString(hash[:]), nil
}
