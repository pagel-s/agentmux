// Package pi adds Agentmux delivery to Pi's ordinary interactive terminal.
package pi

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed extension.mjs
var extension []byte

// Prepare adds one explicit extension without disabling any of Pi's native
// resources. Call this inside the session sandbox, or mount the returned asset
// read-only when preparing a sandbox outside it. cleanup owns only its temp dir.
func Prepare(argv []string) ([]string, func(), error) {
	if len(argv) == 0 || argv[0] == "" {
		return nil, nil, errors.New("Pi native bridge requires an executable")
	}
	dir, err := os.MkdirTemp("", "agentmux-pi-bridge-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "extension.mjs")
	if err := os.WriteFile(path, extension, 0600); err != nil {
		cleanup()
		return nil, nil, err
	}
	out := append([]string(nil), argv...)
	if prefix, ok := piPrefix(argv); ok && slices.Contains(argv[prefix:], "--") {
		// Flags after Pi's separator become prompt text. Keep every original
		// argument in order and insert bridge flags before the owner's options.
		out = append(append(append([]string(nil), argv[:prefix]...), "--extension", path), argv[prefix:]...)
	} else {
		out = append(out, "--extension", path)
	}
	return out, cleanup, nil
}

// Run is called by the native bridge entry point inside the original sandbox.
// Pi retains its real PTY, environment, configuration, tools and approval UI.
func Run(ctx context.Context, argv []string) error {
	return run(ctx, argv, os.Stderr, 2*time.Second)
}

func run(ctx context.Context, argv []string, stderr io.Writer, probeTimeout time.Duration) error {
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("Pi native bridge requires an executable")
	}
	args := argv
	if probeVersion(ctx, argv, probeTimeout) {
		var cleanup func()
		var err error
		args, cleanup, err = Prepare(argv)
		if err != nil {
			args = argv
			fmt.Fprintln(stderr, "Agentmux: native delivery setup unavailable; starting your original Pi terminal with manual coordination.")
		} else {
			defer cleanup()
		}
	} else {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprintln(stderr, "Agentmux: automatic native delivery requires maintained Pi >=1.4.0. Version unsupported or unavailable; starting your original Pi terminal with manual coordination.")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, stderr
	return cmd.Run()
}

var versionPattern = regexp.MustCompile(`^(?:[Pp]i\s+)?v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

func supportedVersion(text string) bool {
	parts := versionPattern.FindStringSubmatch(strings.TrimSpace(text))
	if parts == nil {
		return false
	}
	for i, suffix := range parts[4:] {
		if suffix == "" {
			continue
		}
		for _, identifier := range strings.Split(suffix[1:], ".") {
			if identifier == "" || i == 0 && len(identifier) > 1 && identifier[0] == '0' && strings.Trim(identifier, "0123456789") == "" {
				return false
			}
		}
	}
	major, e1 := strconv.ParseUint(parts[1], 10, 32)
	minor, e2 := strconv.ParseUint(parts[2], 10, 32)
	patch, e3 := strconv.ParseUint(parts[3], 10, 32)
	if e1 != nil || e2 != nil || e3 != nil {
		return false
	}
	return major > 1 || major == 1 && (minor > 4 || minor == 4 && (patch > 0 || parts[4] == ""))
}

type probeOutput struct {
	data     []byte
	overflow bool
}

func (o *probeOutput) Write(p []byte) (int, error) {
	n := len(p)
	room := 4096 - len(o.data)
	if n > room {
		o.overflow = true
		p = p[:room]
	}
	o.data = append(o.data, p...)
	return n, nil
}

func probeVersion(ctx context.Context, argv []string, timeout time.Duration) bool {
	prefix, ok := piPrefix(argv)
	if !ok {
		return false
	}
	if len(argv) > prefix {
		switch argv[prefix] {
		case "auth", "login", "logout", "install", "remove", "uninstall", "update", "list", "config", "mcp", "package", "packages":
			return false
		}
	}
	probe, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Probe only the CLI's version, never owner prompts, subcommands or options.
	// Keep the verified runtime/script prefix for `node path/to/pi.js`.
	args := append(append([]string(nil), argv[1:prefix]...), "--version")
	cmd := exec.CommandContext(probe, argv[0], args...)
	var output probeOutput
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	// Shims may exit while descendants retain their inherited pipes. WaitDelay
	// bounds pipe-copy cleanup as well as the command's execution deadline.
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd.Run() == nil && !output.overflow && supportedVersion(string(output.data))
}

func piPrefix(argv []string) (int, bool) {
	if len(argv) == 0 {
		return 0, false
	}
	name := filepath.Base(argv[0])
	switch name {
	case "pi", "pi.exe", "pi.js", "pi.mjs", "pi.cjs", "cli.js", "cli.mjs", "cli.cjs":
		return 1, true
	case "node", "nodejs", "bun":
		script := 1
		if len(argv) > 1 && (argv[1] == "--" || name == "bun" && argv[1] == "run") {
			script = 2
		}
		if len(argv) <= script || strings.HasPrefix(argv[script], "-") {
			return 0, false
		}
		switch filepath.Base(argv[script]) {
		case "pi", "pi.js", "pi.mjs", "pi.cjs", "cli.js", "cli.mjs", "cli.cjs":
			return script + 1, true
		}
	}
	return 0, false
}
