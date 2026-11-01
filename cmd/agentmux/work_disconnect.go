package main

import (
	"agentmux/internal/managed"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

func disconnectWorkGroup(ctx context.Context, client *managed.Client, o workOptions, out io.Writer) error {
	if o.group != "" && len(o.args) > 0 {
		return errors.New("select --group ID or an exact set of session IDs")
	}
	if o.group == "" && len(o.args) < 2 {
		return errors.New("unlink requires an exact pair/group of session IDs, or --group ID; isolate ID disconnects all peers")
	}
	raw, err := workCall(ctx, client, "control.get", map[string]any{})
	if err != nil {
		return err
	}
	var controls managed.ControlState
	if err := json.Unmarshal(raw, &controls); err != nil {
		return err
	}
	selected := []managed.WorkspaceGroup{}
	ids := append([]string(nil), o.args...)
	sort.Strings(ids)
	for _, group := range controls.Groups {
		match := group.ID == o.group && o.group != ""
		if o.group == "" {
			members := append([]string(nil), group.Agents...)
			sort.Strings(members)
			match = strings.Join(ids, "\x00") == strings.Join(members, "\x00")
		}
		if match {
			selected = append(selected, group)
		}
	}
	if len(selected) != 1 {
		return errors.New("exact collaboration group not found; inspect Connections or select --group ID")
	}
	group := selected[0]
	if _, err := workCall(ctx, client, "control.update", map[string]any{"version": controls.Version, "kind": "collaboration", "agents": group.Agents, "commands": group.Commands, "enabled": false}); err != nil {
		return err
	}
	fmt.Fprintf(out, "Disconnected group %s (%s). Other groups and native terminals remain.\n", group.ID, strings.Join(group.Agents, ", "))
	fmt.Fprintln(out, "Other groups or explicit grants may still allow communication; inspect effective Connections. Accepted tasks and previously read information remain.")
	return nil
}
