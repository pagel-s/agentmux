package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"agentmux/internal/managedlaunch"
)

func runWorkLayout(operation string, args []string, out, diagnostics io.Writer) int {
	if workHelpRequested(args) {
		fmt.Fprintln(out, "Usage: amx split FIRST SECOND [--vertical] [-s WORKSPACE]\n       amx split ID [--vertical] [-s WORKSPACE]\n       amx unsplit ID [-s WORKSPACE]\nMove existing native terminals into one tmux window, or back into separate windows.\nWith one ID, split beside the current agent/scratch pane in this workspace.\nDefault: side by side; --vertical stacks panes. Processes, IDs and permissions stay the same.\nNative windows and layouts are shared by all attachments. Scratch splits: Ctrl-b then % or double quote; new scratch window: Ctrl-b then c.")
		return exitSuccess
	}
	vertical := false
	filtered := []string{}
	literal := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			literal = true
		}
		if literal {
			filtered = append(filtered, arg)
			continue
		}
		if arg == "--vertical" {
			if vertical {
				fmt.Fprintln(diagnostics, "error: duplicate --vertical; no action was taken")
				return exitValidation
			}
			vertical = true
		} else {
			name, _, inline := strings.Cut(arg, "=")
			if strings.HasPrefix(name, "-") && name != "--session" && name != "--state" {
				fmt.Fprintln(diagnostics, "error: unsupported layout option", name)
				return exitValidation
			}
			filtered = append(filtered, arg)
			if (name == "--session" || name == "--state") && !inline && i+1 < len(args) {
				i++
				filtered = append(filtered, args[i])
			}
		}
	}
	o, err := parseWorkOptions(filtered)
	if err == nil && (len(o.args) < 1 || len(o.args) > 2 || operation == "unsplit" && (len(o.args) != 1 || vertical)) {
		err = fmt.Errorf("use amx %s --help for layout arguments", operation)
	}
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitValidation
	}
	exe, err := os.Executable()
	var launcher *managedlaunch.Launcher
	var runtime managedlaunch.Runtime
	if err == nil {
		launcher, err = managedlaunch.New(managedlaunch.Options{StateDir: o.state, Executable: exe})
	}
	if err == nil {
		runtime, err = launcher.Describe(context.Background())
	}
	if err == nil {
		if operation == "unsplit" {
			err = launcher.Unsplit(context.Background(), runtime, o.args[0])
		} else {
			source, beside := o.args[0], ""
			if len(o.args) == 2 {
				beside, source = o.args[0], o.args[1]
			}
			err = launcher.Split(context.Background(), runtime, source, beside, vertical)
		}
	}
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitRuntime
	}
	fmt.Fprintln(out, "Layout updated. Native processes and coordination permissions are unchanged.")
	return exitSuccess
}
