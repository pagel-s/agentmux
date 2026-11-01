package main

import (
	"fmt"
	"strings"
)

// Validate before resolving a workspace or invoking its controller. Shared
// parsing must never turn an irrelevant copied flag into a broader operation.
func validateWorkFlags(operation string, args []string) error {
	allowed := map[string]bool{"--session": true, "--state": true}
	add := func(names ...string) {
		for _, name := range names {
			allowed[name] = true
		}
	}
	switch operation {
	case "start":
		add("--project", "--config", "--sandbox", "--detach")
	case "dashboard":
		add("--view", "--popup")
	case "send":
		add("--mode")
	case "reply":
		add("--mode", "--to", "--board")
	case "board":
		add("--group", "--board")
	case "disconnect":
		add("--group")
	case "collaborate":
		add("--legacy-policy")
	case "relaunch":
		add("--resume", "--native-launcher")
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if !strings.HasPrefix(args[i], "--") {
			continue
		}
		name, _, inline := strings.Cut(args[i], "=")
		if !allowed[name] {
			if name == "--native-launcher" {
				return fmt.Errorf("--native-launcher applies only to relaunch; no action was taken")
			}
			if name == "--legacy-policy" {
				return fmt.Errorf("--legacy-policy applies only to collaborate; no action was taken")
			}
			return fmt.Errorf("%s does not accept %s; no action was taken", operation, name)
		}
		if !inline && workOptionValue(name) {
			i++
		}
	}
	return nil
}
