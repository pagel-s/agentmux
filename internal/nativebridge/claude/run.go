// Package claude connects Claude Code's native peer inbox to Agentmux. It never
// reads the terminal screen, edits a composer draft, or submits terminal keys.
package claude

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agentmux/internal/nativebridge"
)

const (
	bridgeDirEnv   = "AGENTMUX_CLAUDE_BRIDGE_DIR"
	bridgeNonceEnv = "AGENTMUX_CLAUDE_BRIDGE_NONCE"
)

// Run keeps Claude's native terminal, account, settings, plugins and MCP servers.
// The extra settings are private to this invocation. Its asynchronous hook is a
// child of Claude, so the native inbox token never leaves Claude's child bridge.
// conn is deliberately used only by the separately invoked RunHook, which
// registers the actual native session ID received in SessionStart.
func Run(ctx context.Context, argv []string, conn nativebridge.Connection) error {
	_ = conn
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("Claude native bridge requires an executable")
	}
	cli := os.Getenv("AGENTMUX_CLI")
	if !filepath.IsAbs(cli) {
		return errors.New("Claude native bridge requires an absolute AGENTMUX_CLI executable")
	}
	if st, err := os.Stat(cli); err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
		return errors.New("Claude native bridge helper is not executable")
	}
	if err := checkVersion(ctx, argv[0]); err != nil {
		fmt.Fprintln(os.Stderr, "Agentmux: "+err.Error()+". Continuing the native terminal with manual inbox access.")
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	dir, err := os.MkdirTemp("", "amx-claude-")
	if err != nil {
		return errors.New("cannot create private Claude bridge state")
	}
	defer os.RemoveAll(dir)
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	marker := hex.EncodeToString(nonce[:])
	if err := os.WriteFile(filepath.Join(dir, "alive"), []byte(marker), 0600); err != nil {
		return err
	}
	args, err := overlaySettings(argv[1:], cli, dir)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, argv[0], args...)
	cmd.Env = append(os.Environ(), bridgeDirEnv+"="+dir, bridgeNonceEnv+"="+marker)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

var versionPattern = regexp.MustCompile(`(?m)^([0-9]+)\.([0-9]+)\.([0-9]+)(?:[ \r\n(]|$)`)

// Same-machine inbox messaging is documented for all providers from 2.1.248.
// A missing SessionStart socket still fails closed: version alone is not a
// claim that a particular account/policy has enabled the capability.
func checkVersion(ctx context.Context, executable string) error {
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probe, executable, "--version")
	var out cappedOutput
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return errors.New("cannot verify Claude native inbox support; Claude Code 2.1.248 or later is required")
	}
	m := versionPattern.FindStringSubmatch(out.String())
	if len(m) != 4 {
		return errors.New("cannot verify Claude native inbox version; Claude Code 2.1.248 or later is required")
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	if major < 2 || major == 2 && (minor < 1 || minor == 1 && patch < 248) {
		return errors.New("Claude native collaboration requires Claude Code 2.1.248 or later")
	}
	return nil
}

type cappedOutput struct{ strings.Builder }

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 4096 - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Builder.Write(p)
	}
	return n, nil
}

func overlaySettings(argv []string, executable, dir string) ([]string, error) {
	args := make([]string, 0, len(argv)+2)
	settings := map[string]json.RawMessage{}
	// Commander uses the last --settings value. Merge that effective value with
	// our hook rather than dropping a caller's per-session configuration.
	var supplied string
	provided := false
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			args = append(args, argv[i:]...)
			break
		}
		if arg == "--settings" {
			if i+1 >= len(argv) {
				return nil, errors.New("Claude --settings requires a value")
			}
			i++
			supplied = argv[i]
			provided = true
			continue
		}
		if strings.HasPrefix(arg, "--settings=") {
			supplied = strings.TrimPrefix(arg, "--settings=")
			provided = true
			continue
		}
		args = append(args, arg)
	}
	if provided {
		data := []byte(supplied)
		if !strings.HasPrefix(strings.TrimSpace(supplied), "{") {
			st, err := os.Stat(supplied)
			if err != nil || !st.Mode().IsRegular() || st.Size() > 8<<20 {
				return nil, errors.New("cannot merge Claude per-session settings file")
			}
			data, err = os.ReadFile(supplied)
			if err != nil {
				return nil, errors.New("cannot read Claude per-session settings file")
			}
		}
		if len(data) > 8<<20 || json.Unmarshal(data, &settings) != nil || settings == nil {
			return nil, errors.New("Claude per-session settings must be a JSON object")
		}
	}
	hooks := map[string]json.RawMessage{}
	if value, exists := settings["hooks"]; exists && json.Unmarshal(value, &hooks) != nil {
		return nil, errors.New("Claude per-session hooks must be a JSON object")
	}
	if hooks == nil {
		hooks = map[string]json.RawMessage{}
	}
	var start []json.RawMessage
	if value, exists := hooks["SessionStart"]; exists && json.Unmarshal(value, &start) != nil {
		return nil, errors.New("Claude SessionStart hooks must be an array")
	}
	entry, _ := json.Marshal(map[string]any{"hooks": []any{map[string]any{
		"type": "command", "command": executable,
		"args": []string{"managed", "native-hook", "claude"}, "async": true,
	}}})
	start = append(start, entry)
	hooks["SessionStart"], _ = json.Marshal(start)
	settings["hooks"], _ = json.Marshal(hooks)
	data, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, fmt.Errorf("cannot write private Claude bridge settings: %w", err)
	}
	// Place our option before a literal -- so prompts that resemble options stay
	// positional. No native prompt or existing argument is submitted or rewritten.
	for i, arg := range args {
		if arg == "--" {
			return append(append(append([]string{}, args[:i]...), "--settings", path), args[i:]...), nil
		}
	}
	return append(args, "--settings", path), nil
}
