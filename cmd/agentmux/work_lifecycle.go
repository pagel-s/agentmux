package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentmux/internal/managedlaunch"
)

func runWorkRelaunch(args []string, out, diagnostics io.Writer) int {
	if workHelpRequested(args) {
		fmt.Fprintln(out, "Usage: agentmux relaunch ID [--resume ID|last|picker] [--native-launcher ABSOLUTE_PATH] [--session NAME | --state DIR]\nRelaunch one exited native session with its stable ID, preserving healthy peers.\nDefault: fresh native conversation. --resume explicitly selects native history; no conversation is guessed.\n--native-launcher explicitly recovers a trusted-local interactive session using the selected public executable for this attempt only. Configured trailing arguments and saved definitions are preserved; the running controller must support this option.")
		return exitSuccess
	}
	o, err := parseWorkOptions(args)
	if err != nil || len(o.args) != 1 {
		if err == nil {
			err = errors.New("relaunch requires one stable session ID")
		}
		fmt.Fprintln(diagnostics, "error:", err)
		return exitValidation
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitRuntime
	}
	launcher, err := managedlaunch.New(managedlaunch.Options{StateDir: o.state, Executable: exe})
	if err == nil {
		_, err = launcher.RelaunchWithLauncher(context.Background(), o.args[0], o.resume, o.nativeLauncher)
	}
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitRuntime
	}
	mode := "fresh native conversation"
	if o.resume != "" {
		mode = "native history " + o.resume
	}
	fmt.Fprintf(out, "Relaunched %s in %s (%s). Peers remain running.\n", consoleContent(o.args[0], 100), consoleContent(o.state, 300), consoleContent(mode, 200))
	if o.nativeLauncher != "" {
		fmt.Fprintln(out, "This attempt uses launcher", consoleContent(o.nativeLauncher, 400)+"; the saved definition is unchanged.")
	}
	fmt.Fprintln(out, "Open:", managedlaunch.ShellCommand([]string{"agentmux", "attach", o.args[0], "--state", o.state}))
	return exitSuccess
}

func workspaceIndexPath() (string, error) {
	state, err := defaultWorkState("work")
	return filepath.Join(filepath.Dir(state), "known-workspaces.json"), err
}
func loadWorkspaceIndex(path string) ([]string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024*1024 {
		return nil, errors.New("workspace index must be a private regular file under 1 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var paths []string
	err = json.Unmarshal(data, &paths)
	return paths, err
}
func rememberWorkState(state string) error {
	index, err := workspaceIndexPath()
	if err != nil {
		return err
	}
	return managedlaunch.RecordWorkspace(index, state)
}

type workspaceListing struct {
	Session string `json:"session"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Agents  int    `json:"agents"`
	Problem string `json:"problem,omitempty"`
}

func workspaceInventory(root string) ([]workspaceListing, error) {
	index, err := workspaceIndexPath()
	if err != nil {
		return nil, err
	}
	paths, err := loadWorkspaceIndex(index)
	if err != nil {
		return nil, err
	}
	if root == "" {
		root = filepath.Dir(index)
	}
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			paths = append(paths, filepath.Join(root, entry.Name()))
		}
	}
	seen := map[string]bool{}
	result := []workspaceListing{}
	exe, _ := os.Executable()
	liveCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	for _, state := range paths {
		state, err = filepath.Abs(state)
		if err != nil || seen[state] {
			continue
		}
		if canonical, e := filepath.EvalSymlinks(state); e == nil {
			state = canonical
			if seen[state] {
				continue
			}
		}
		seen[state] = true
		saved, err := managedlaunch.Snapshot(state)
		if os.IsNotExist(err) {
			continue
		}
		item := workspaceListing{State: state, Status: "unknown"}
		if err != nil {
			item.Problem = err.Error()
		} else {
			item.Session = saved.Session
			item.Agents = len(saved.Agents)
			item.Status = saved.State
			launcher, e := managedlaunch.New(managedlaunch.Options{StateDir: state, Executable: exe})
			if e == nil {
				if liveCtx.Err() != nil {
					item.Status = "unknown"
					item.Problem = "Live inventory time budget exhausted; select this workspace to inspect it."
					result = append(result, item)
					continue
				}
				current, e := launcher.Describe(liveCtx)
				if e == nil {
					item.Status = current.State
				} else {
					item.Status = "unknown"
					item.Problem = e.Error()
				}
			} else {
				item.Status = "unknown"
				item.Problem = e.Error()
			}
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Session == result[j].Session {
			return result[i].State < result[j].State
		}
		return result[i].Session < result[j].Session
	})
	return result, nil
}
func runWorkspaceInventory(args []string, out, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("workspaces", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	root := flags.String("root", "", "additional directory containing workspace state directories")
	register := flags.String("register", "", "remember an existing custom workspace state directory")
	asJSON := flags.Bool("json", false, "emit JSON inventory")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitSuccess
		}
		return exitValidation
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(diagnostics, "error: workspaces accepts flags only")
		return exitValidation
	}
	if *register != "" {
		if _, err := managedlaunch.Snapshot(*register); err != nil {
			fmt.Fprintln(diagnostics, "error:", err)
			return exitValidation
		}
		if err := rememberWorkState(*register); err != nil {
			fmt.Fprintln(diagnostics, "error:", err)
			return exitRuntime
		}
	}
	rows, err := workspaceInventory(*root)
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitRuntime
	}
	if *asJSON {
		_ = json.NewEncoder(out).Encode(rows)
		return exitSuccess
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "No saved workspaces found. Start with agentmux start; register older custom paths with workspaces --register DIR.")
	}
	for _, row := range rows {
		fmt.Fprintf(out, "%s · %s · %d registered pane(s)\n  %s\n  %s\n", consoleContent(row.Session, 100), row.Status, row.Agents, consoleContent(row.State, 300), managedlaunch.ShellCommand([]string{"agentmux", "dashboard", "--state", row.State}))
		if row.Problem != "" {
			fmt.Fprintln(out, "  "+consoleContent(row.Problem, 300))
		}
	}
	return exitSuccess
}
