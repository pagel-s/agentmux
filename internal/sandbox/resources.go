package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"agentmux/internal/harness"
)

// Limits are optional Linux bounds; zero leaves the resource unchanged.
// MemoryBytes, CPUPercent and Processes use a cgroup for the whole turn tree
// (Processes counts kernel tasks/threads). CPUSeconds, AddressSpaceBytes and
// OpenFiles are inherited per-process rlimits, not aggregate tree budgets.
// AddressSpaceBytes is intentionally opt-in: V8/Go reserve large virtual ranges
// even when actual RAM use is small. CPUPercent=100 permits one CPU's time.
type Limits struct {
	MemoryBytes       int64
	CPUPercent        int
	Processes         int
	OpenFiles         int
	CPUSeconds        int
	AddressSpaceBytes int64
}

func (l Limits) Enabled() bool { return l != (Limits{}) }
func (l Limits) Empty() bool   { return !l.Enabled() }
func (l Limits) cgroup() bool  { return l.MemoryBytes != 0 || l.CPUPercent != 0 || l.Processes != 0 }
func (l Limits) Validate() error {
	if l.MemoryBytes < 0 || (l.MemoryBytes != 0 && l.MemoryBytes < 64<<20) || l.MemoryBytes > 1<<40 || l.AddressSpaceBytes < 0 || (l.AddressSpaceBytes != 0 && l.AddressSpaceBytes < 64<<20) || l.AddressSpaceBytes > 8<<40 || l.CPUPercent < 0 || l.CPUPercent > 6400 || l.Processes < 0 || l.Processes == 1 || l.Processes > 65536 || l.OpenFiles < 0 || (l.OpenFiles != 0 && l.OpenFiles < 16) || l.OpenFiles > 1048576 || l.CPUSeconds < 0 || l.CPUSeconds > 86400 {
		return fmt.Errorf("resource limits must be nonnegative and within supported finite bounds")
	}
	return nil
}

// The trusted shim runs outside bwrap and verifies actual kernel controls
// before executing the sandbox. A manager that accepts properties without
// delegated controllers must not silently launch an unlimited workload.
const cgroupCheck = `set -eu
memory=$1; cpu=$2; tasks=$3; shift 3
group=
while IFS=: read -r hierarchy controllers path; do
  if [ "$hierarchy" = 0 ] && [ -z "$controllers" ]; then group=$path; fi
done < /proc/self/cgroup
case "$group" in /*) ;; *) exit 125;; esac
case "$group" in *'/../'*|*'/..') exit 125;; esac
base=/sys/fs/cgroup$group
if [ "$memory" -gt 0 ]; then
  read -r actual < "$base/memory.max"
  [ "$actual" != max ] && [ "$actual" -le "$memory" ] || exit 125
  read -r swap < "$base/memory.swap.max"; [ "$swap" = 0 ] || exit 125
  read -r oom < "$base/memory.oom.group"; [ "$oom" = 1 ] || exit 125
fi
if [ "$cpu" -gt 0 ]; then
  read -r quota period < "$base/cpu.max"
  [ "$quota" != max ] && [ "$((quota * 100))" -le "$((cpu * period))" ] || exit 125
fi
if [ "$tasks" -gt 0 ]; then
  read -r actual < "$base/pids.max"
  [ "$actual" != max ] && [ "$actual" -le "$tasks" ] || exit 125
fi
exec "$@"
`

func wrapResources(spec harness.Spec, limits Limits) (harness.Spec, error) {
	if err := limits.Validate(); err != nil {
		return harness.Spec{}, err
	}
	if !limits.Enabled() {
		return spec, nil
	}
	if runtime.GOOS != "linux" {
		return harness.Spec{}, fmt.Errorf("resource bounds require Linux")
	}
	if limits.OpenFiles != 0 || limits.CPUSeconds != 0 || limits.AddressSpaceBytes != 0 {
		binary, err := exec.LookPath("prlimit")
		if err != nil {
			return harness.Spec{}, fmt.Errorf("requested process resource bounds require prlimit")
		}
		args := []string{}
		for _, limit := range []struct {
			flag  string
			value int64
		}{{"--cpu", int64(limits.CPUSeconds)}, {"--as", limits.AddressSpaceBytes}, {"--nofile", int64(limits.OpenFiles)}} {
			if limit.value != 0 {
				value := strconv.FormatInt(limit.value, 10)
				args = append(args, limit.flag+"="+value+":"+value)
			}
		}
		spec.Args = append(append(args, "--", spec.Binary), spec.Args...)
		spec.Binary = binary
	}
	if !limits.cgroup() {
		return spec, nil
	}
	binary, err := exec.LookPath("systemd-run")
	if err != nil {
		return harness.Spec{}, fmt.Errorf("requested aggregate resource bounds require systemd-run and a user manager")
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return harness.Spec{}, fmt.Errorf("could not generate resource scope identity")
	}
	args := []string{"--user", "--scope", "--quiet", "--collect", "--no-ask-password", "--expand-environment=no", "--description=Agentmux isolated turn", "--unit=agentmux-turn-" + hex.EncodeToString(nonce[:]) + ".scope"}
	if limits.MemoryBytes != 0 {
		args = append(args, "--property=MemoryMax="+strconv.FormatInt(limits.MemoryBytes, 10), "--property=MemorySwapMax=0", "--property=OOMPolicy=kill")
	}
	if limits.CPUPercent != 0 {
		args = append(args, "--property=CPUQuota="+strconv.Itoa(limits.CPUPercent)+"%", "--property=CPUQuotaPeriodSec=100ms")
	}
	if limits.Processes != 0 {
		args = append(args, "--property=TasksMax="+strconv.Itoa(limits.Processes))
	}
	args = append(args, "--", "/bin/sh", "-c", cgroupCheck, "agentmux-resource-check", strconv.FormatInt(limits.MemoryBytes, 10), strconv.Itoa(limits.CPUPercent), strconv.Itoa(limits.Processes), spec.Binary)
	args = append(args, spec.Args...)
	spec.Binary, spec.Args = binary, args
	// Only the launcher sees the host runtime directory. Wrap explicitly unsets
	// this variable and any user bus address before the untrusted child starts.
	env := make([]string, 0, len(spec.Env)+1)
	for _, value := range spec.Env {
		if !strings.HasPrefix(value, "XDG_RUNTIME_DIR=") && !strings.HasPrefix(value, "DBUS_SESSION_BUS_ADDRESS=") {
			env = append(env, value)
		}
	}
	spec.Env = append(env, "XDG_RUNTIME_DIR=/run/user/"+strconv.Itoa(os.Getuid()))
	return spec, nil
}

// ProbeLimits creates at most one harmless transient scope. It checks actual
// delegated kernel controls, without model calls or changes to global settings.
func ProbeLimits(ctx context.Context, limits Limits) error {
	spec, err := wrapResources(harness.Spec{Name: "command", Binary: "/bin/true", Env: []string{"PATH=/usr/bin:/bin", "LANG=C"}}, limits)
	if err != nil {
		return err
	}
	if !limits.Enabled() {
		return nil
	}
	command := exec.CommandContext(ctx, spec.Binary, spec.Args...)
	command.Env = spec.Env
	if err := command.Run(); err != nil {
		return fmt.Errorf("requested resource bounds are unavailable or could not be verified: %w", err)
	}
	return nil
}
