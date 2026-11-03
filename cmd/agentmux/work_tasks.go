package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
)

type workTaskPage struct {
	Tasks   []managed.Task `json:"tasks"`
	Cursor  string         `json:"cursor"`
	HasMore bool           `json:"has_more"`
}

// Inspection uses only owner read operations. Recovery remains a separate,
// explicitly reviewed action against the selected task's current version.
func runWorkTasks(operation string, args []string, out, diagnostics io.Writer) int {
	if workHelpRequested(args) {
		fmt.Fprintln(out, `Usage: amx task TASK_ID [--json] [--session NAME | --state DIR]
       amx tasks [--to ID] [--status STATE] [--query TEXT] [--limit N]
                 [--after TASK_ID] [--json] [--session NAME | --state DIR]

Read retained delegation history, newest first (default page: 25; maximum: 200).
STATE is proposed, running, submitted, verified, failed or cancelled.
Task state and delivery evidence are separate; submitted does not mean verified.
Inspection never retries, cancels, verifies or resolves a task.`)
		return exitSuccess
	}
	fail := func(code int, err error) int {
		fmt.Fprintln(diagnostics, "error:", err)
		return code
	}
	flags := flag.NewFlagSet(operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	session := flags.String("session", "work", "workspace name")
	state := flags.String("state", "", "workspace state directory")
	to := flags.String("to", "", "exact recipient ID")
	status := flags.String("status", "", "task state")
	query := flags.String("query", "", "search task ID, instruction and result")
	limit := flags.Int("limit", 25, "page size")
	after := flags.String("after", "", "last task ID from the previous page")
	asJSON := flags.Bool("json", false, "print broker evidence as JSON")
	positionals, err := parseGuidedFlags(flags, args)
	if err != nil {
		return fail(exitValidation, err)
	}
	var selectors []string
	listOnly := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "session":
			selectors = append(selectors, "--session", *session)
		case "state":
			selectors = append(selectors, "--state", *state)
		case "to", "status", "query", "limit", "after":
			listOnly = true
		}
	})
	if operation != "task" && operation != "tasks" {
		return fail(exitValidation, errors.New("choose task or tasks"))
	}
	if operation == "task" && (len(positionals) != 1 || listOnly) {
		return fail(exitValidation, errors.New("task requires one TASK_ID and accepts only workspace selectors and --json; see task --help"))
	}
	if operation == "tasks" && len(positionals) != 0 {
		return fail(exitValidation, errors.New("tasks accepts filters, not positional arguments; use task TASK_ID for one item"))
	}
	if *limit < 1 || *limit > 200 || len(*query) > 512 {
		return fail(exitValidation, errors.New("--limit must be 1..200; --query is limited to 512 bytes"))
	}
	if *to != "" && !managedconfig.ValidID(*to) {
		return fail(exitValidation, errors.New("--to requires a stable recipient ID"))
	}
	if *status != "" {
		switch *status {
		case "proposed", "running", "submitted", "verified", "failed", "cancelled":
		default:
			return fail(exitValidation, errors.New("--status must be proposed, running, submitted, verified, failed or cancelled"))
		}
	}
	id := *after
	if operation == "task" {
		id = positionals[0]
	}
	if (operation == "task" && id == "") || (id != "" && (!strings.HasPrefix(id, "task_") || len(id) > 256 || !managedconfig.ValidID(id))) {
		return fail(exitValidation, errors.New("use an exact task_ ID from send, tasks or the workbench"))
	}
	options, err := parseWorkOptions(selectors)
	if err != nil {
		return fail(exitValidation, err)
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(options.state, ""))
	if err != nil {
		return fail(exitRuntime, fmt.Errorf("cannot load workspace owner credential at %s; check amx context with the same selector", consoleContent(options.state, len(options.state))))
	}
	client := managed.NewClient(credential)
	request := map[string]any{"to": *to, "state": *status, "query": *query, "limit": *limit, "after": *after, "order": "desc"}
	op := "task.list"
	if operation == "task" {
		op, request = "task.get", map[string]any{"id": id}
	}
	raw, err := workCall(context.Background(), client, op, request)
	if err != nil {
		return fail(exitRuntime, err)
	}
	var task managed.Task
	var page workTaskPage
	if operation == "task" {
		var result struct {
			Task managed.Task `json:"task"`
		}
		err = json.Unmarshal(raw, &result)
		task = result.Task
		if err == nil && task.ID != id {
			err = errors.New("broker returned a different or missing task")
		}
	} else {
		err = json.Unmarshal(raw, &page)
		if err == nil && (page.Tasks == nil || (page.HasMore && (len(page.Tasks) == 0 || page.Cursor == "" || page.Cursor == *after))) {
			err = errors.New("broker returned an invalid task history page")
		}
	}
	if err != nil {
		return fail(exitRuntime, err)
	}
	if *asJSON {
		var value any = page
		if operation == "task" {
			value = struct {
				Task managed.Task `json:"task"`
			}{task}
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err = enc.Encode(value); err != nil {
			return fail(exitRuntime, err)
		}
		return exitSuccess
	}
	fmt.Fprintln(out, "Workspace:", consoleContent(options.state, len(options.state)))
	if operation == "task" {
		printWorkTask(out, options.state, task)
		return exitSuccess
	}
	if len(page.Tasks) == 0 {
		fmt.Fprintln(out, "No matching tasks.")
	}
	for _, t := range page.Tasks {
		fmt.Fprintf(out, "%s  to %s  %s / %s  v%d\n  %s\n", consoleContent(t.ID, 256), consoleContent(t.To, 128), consoleContent(t.State, 80), consoleContent(t.Delivery, 80), t.Version, consoleContent(strings.Join(strings.Fields(t.Text), " "), 140))
	}
	if len(page.Tasks) > 0 {
		printWorkTaskCommand(out, "Inspect", []string{"amx", "task", page.Tasks[0].ID, "--state", options.state})
	}
	if page.HasMore {
		next := []string{"amx", "tasks", "--state", options.state, "--limit", fmt.Sprint(*limit)}
		for _, pair := range [][2]string{{"--to", *to}, {"--status", *status}, {"--query", *query}} {
			if pair[1] != "" {
				next = append(next, pair[0], pair[1])
			}
		}
		next = append(next, "--after", page.Cursor)
		printWorkTaskCommand(out, "More", next)
	}
	return exitSuccess
}

func printWorkTask(out io.Writer, state string, t managed.Task) {
	text := func(value string) string { return consoleContent(value, len(value)) }
	fmt.Fprintf(out, "Task: %s\nFrom: %s\nTo: %s\nState: %s\nDelivery: %s\nVersion: %d\n", text(t.ID), text(t.From), text(t.To), text(t.State), text(t.Delivery), t.Version)
	fmt.Fprintf(out, "Created: %s\nUpdated: %s\n\nInstruction:\n%s\n", text(t.CreatedAt), text(t.UpdatedAt), text(t.Text))
	if t.Result != "" {
		fmt.Fprintln(out, "\nResult:\n"+text(t.Result))
	}
	if t.Native != nil {
		raw, _ := json.MarshalIndent(t.Native, "", "  ")
		fmt.Fprintln(out, "\nNative receipt:\n"+text(string(raw)))
	}
	if t.Trace != nil {
		raw, _ := json.MarshalIndent(t.Trace, "", "  ")
		fmt.Fprintln(out, "\nTrace:\n"+text(string(raw)))
	}
	if t.Recovery != nil {
		fmt.Fprintf(out, "\nRecovery delivery: %s\n%s\nAvailable actions: %s\n", text(t.Recovery.Delivery), text(t.Recovery.Reason), text(strings.Join(t.Recovery.Actions, ", ")))
		fmt.Fprintln(out, "Review this exact task and its side effects in Tasks before acting.")
		printWorkTaskCommand(out, "Workbench", []string{"amx", "ui", "--state", state})
	}
}

func printWorkTaskCommand(out io.Writer, label string, args []string) {
	command := managedlaunch.ShellCommand(args)
	fmt.Fprintln(out, label+":", displayWorkCommand(command))
}
