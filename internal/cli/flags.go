package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type CommonOptions struct {
	ConfigPath string
	Params     map[string]string
	Strict     bool
	Verbose    bool
}

type LoadOptions struct {
	CommonOptions
	Attach  bool
	DryRun  bool
	Replace bool
}

type StatusOptions struct {
	Session string
	Pane    string
	JSON    bool
	Logs    int
}

type InspectOptions struct {
	Session string
	Pane    string
	JSON    bool
}

type KVOptions struct {
	Session string
	Key     string
	Value   string
	JSON    bool
}

type WaitOptions struct {
	Session string
	Pane    string
	Pattern string
	Timeout string
	JSON    bool
}

type ScaleOptions struct {
	Session string
	Agent   string
	Count   int
	Window  string
	JSON    bool
}

type PaneTargetOptions struct {
	Session string
	Pane    string
	JSON    bool
}

type CaptureOptions struct {
	Session    string
	Pane       string
	JSON       bool
	Tail       int
	StripEmpty bool
}

type SendOptions struct {
	Session string
	Pane    string
	Command string
	NoEnter bool
	JSON    bool
}

type InitOptions struct {
	Template string
	Path     string
	Force    bool
}

type EditOptions struct {
	Target string
}

type RememberOptions struct {
	Session string
	Path    string
}

type paramFlags struct {
	values map[string]string
}

func (p *paramFlags) String() string {
	if len(p.values) == 0 {
		return ""
	}

	parts := make([]string, 0, len(p.values))
	for key, value := range p.values {
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ",")
}

func (p *paramFlags) Set(value string) error {
	if p.values == nil {
		p.values = make(map[string]string)
	}

	key, val, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return fmt.Errorf("invalid --param %q, expected key=value", value)
	}

	p.values[strings.TrimSpace(key)] = val
	return nil
}

func ParseLoad(args []string) (LoadOptions, error) {
	return parseLaunch("load", args, true)
}

func ParseReload(args []string) (LoadOptions, error) {
	return parseLaunch("reload", args, false)
}

func parseLaunch(name string, args []string, allowReplace bool) (LoadOptions, error) {
	var params paramFlags
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	attach := fs.Bool("attach", false, "")
	noAttach := fs.Bool("no-attach", false, "")
	dryRun := fs.Bool("dry-run", false, "")
	verbose := fs.Bool("verbose", false, "")
	strict := fs.Bool("strict", false, "")
	replace := fs.Bool("replace", false, "")
	fs.Var(&params, "param", "")

	normalized, err := normalizeArgs(args)
	if err != nil {
		return LoadOptions{}, err
	}

	if err := fs.Parse(normalized); err != nil {
		return LoadOptions{}, err
	}
	if *attach && *noAttach {
		return LoadOptions{}, errors.New("cannot use --attach and --no-attach together")
	}
	if !allowReplace && *replace {
		return LoadOptions{}, fmt.Errorf("%s always replaces the existing session and does not accept --replace", name)
	}
	if fs.NArg() != 1 {
		return LoadOptions{}, fmt.Errorf("%s requires exactly one config path", name)
	}

	return LoadOptions{
		CommonOptions: CommonOptions{
			ConfigPath: fs.Arg(0),
			Params:     params.values,
			Strict:     *strict,
			Verbose:    *verbose,
		},
		Attach:  !*noAttach && !*dryRun,
		DryRun:  *dryRun,
		Replace: allowReplace && *replace,
	}, nil
}

func ParseValidate(args []string) (CommonOptions, error) {
	return parseCommon("validate", args)
}

func ParseRender(args []string) (CommonOptions, error) {
	return parseCommon("render", args)
}

func ParseInit(args []string) (InitOptions, error) {
	force := false
	positional := make([]string, 0, 2)

	for _, arg := range args {
		switch arg {
		case "--force":
			force = true
		default:
			if strings.HasPrefix(arg, "-") {
				return InitOptions{}, fmt.Errorf("init does not accept flag %q", arg)
			}
			positional = append(positional, arg)
		}
	}

	if len(positional) != 2 {
		return InitOptions{}, errors.New("init requires exactly one template name and one target path")
	}

	return InitOptions{
		Template: positional[0],
		Path:     positional[1],
		Force:    force,
	}, nil
}

func ParseEdit(args []string) (EditOptions, error) {
	if len(args) != 1 {
		return EditOptions{}, errors.New("edit requires exactly one path or session name")
	}
	if strings.TrimSpace(args[0]) == "" {
		return EditOptions{}, errors.New("edit target must not be empty")
	}
	return EditOptions{Target: args[0]}, nil
}

func ParseRemember(args []string) (RememberOptions, error) {
	if len(args) != 2 {
		return RememberOptions{}, errors.New("remember requires exactly one session name and one config path")
	}
	if strings.TrimSpace(args[0]) == "" {
		return RememberOptions{}, errors.New("remember session name must not be empty")
	}
	if strings.TrimSpace(args[1]) == "" {
		return RememberOptions{}, errors.New("remember config path must not be empty")
	}
	return RememberOptions{
		Session: args[0],
		Path:    args[1],
	}, nil
}

func ParseStatus(args []string) (StatusOptions, error) {
	jsonOut := false
	pane := ""
	logs := 0
	positional := make([]string, 0, 1)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--logs":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				parsed, err := strconv.Atoi(args[i+1])
				if err == nil && parsed > 0 {
					logs = parsed
					i++
				} else {
					logs = 10
				}
			} else {
				logs = 10
			}
		case "--pane":
			if i+1 >= len(args) {
				return StatusOptions{}, fmt.Errorf("status flag %q requires a value", arg)
			}
			pane = strings.TrimSpace(args[i+1])
			if pane == "" {
				return StatusOptions{}, fmt.Errorf("status flag %q requires a non-empty value", arg)
			}
			i++
		default:
			if strings.HasPrefix(arg, "-") {
				return StatusOptions{}, fmt.Errorf("status does not accept flag %q", arg)
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		return StatusOptions{}, errors.New("status requires exactly one session name")
	}
	return StatusOptions{
		Session: positional[0],
		Pane:    pane,
		JSON:    jsonOut,
		Logs:    logs,
	}, nil
}

func ParseInspect(args []string) (InspectOptions, error) {
	positional, jsonOut, err := parseOptionalJSONArgs("inspect", args)
	if err != nil {
		return InspectOptions{}, err
	}
	if len(positional) != 2 {
		return InspectOptions{}, errors.New("inspect requires exactly one session name and one pane name")
	}
	return InspectOptions{
		Session: positional[0],
		Pane:    positional[1],
		JSON:    jsonOut,
	}, nil
}

func ParseKVSet(args []string) (KVOptions, error) {
	positional, jsonOut, err := parseOptionalJSONArgs("kv-set", args)
	if err != nil {
		return KVOptions{}, err
	}
	if len(positional) != 3 {
		return KVOptions{}, errors.New("kv-set requires session, key, and value")
	}
	return KVOptions{
		Session: positional[0],
		Key:     positional[1],
		Value:   positional[2],
		JSON:    jsonOut,
	}, nil
}

func ParseKVGet(args []string) (KVOptions, error) {
	positional, jsonOut, err := parseOptionalJSONArgs("kv-get", args)
	if err != nil {
		return KVOptions{}, err
	}
	if len(positional) != 2 {
		return KVOptions{}, errors.New("kv-get requires session and key")
	}
	return KVOptions{
		Session: positional[0],
		Key:     positional[1],
		JSON:    jsonOut,
	}, nil
}

func ParseKVList(args []string) (KVOptions, error) {
	positional, jsonOut, err := parseOptionalJSONArgs("kv-list", args)
	if err != nil {
		return KVOptions{}, err
	}
	if len(positional) != 1 {
		return KVOptions{}, errors.New("kv-list requires exactly one session name")
	}
	return KVOptions{
		Session: positional[0],
		JSON:    jsonOut,
	}, nil
}

func ParseKVDel(args []string) (KVOptions, error) {
	positional, jsonOut, err := parseOptionalJSONArgs("kv-del", args)
	if err != nil {
		return KVOptions{}, err
	}
	if len(positional) != 2 {
		return KVOptions{}, errors.New("kv-del requires session and key")
	}
	return KVOptions{
		Session: positional[0],
		Key:     positional[1],
		JSON:    jsonOut,
	}, nil
}

func ParseWait(args []string) (WaitOptions, error) {
	timeout := ""
	positional := make([]string, 0, 3)
	jsonOut := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--timeout":
			if i+1 >= len(args) {
				return WaitOptions{}, fmt.Errorf("wait flag %q requires a value", arg)
			}
			timeout = args[i+1]
			i++
		default:
			if strings.HasPrefix(arg, "-") {
				return WaitOptions{}, fmt.Errorf("wait does not accept flag %q", arg)
			}
			positional = append(positional, arg)
		}
	}

	if len(positional) != 3 {
		return WaitOptions{}, errors.New("wait requires session name, pane name, and pattern")
	}

	return WaitOptions{
		Session: positional[0],
		Pane:    positional[1],
		Pattern: positional[2],
		Timeout: timeout,
		JSON:    jsonOut,
	}, nil
}

func ParseScale(args []string) (ScaleOptions, error) {
	window := ""
	positional := make([]string, 0, 3)
	jsonOut := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--window":
			if i+1 >= len(args) {
				return ScaleOptions{}, fmt.Errorf("scale flag %q requires a value", arg)
			}
			window = args[i+1]
			i++
		default:
			if strings.HasPrefix(arg, "-") {
				return ScaleOptions{}, fmt.Errorf("scale does not accept flag %q", arg)
			}
			positional = append(positional, arg)
		}
	}

	if len(positional) != 3 {
		return ScaleOptions{}, errors.New("scale requires session name, agent name, and count")
	}

	count, err := strconv.Atoi(positional[2])
	if err != nil || count < 0 {
		return ScaleOptions{}, fmt.Errorf("invalid count %q; must be a non-negative integer", positional[2])
	}

	return ScaleOptions{
		Session: positional[0],
		Agent:   positional[1],
		Count:   count,
		Window:  window,
		JSON:    jsonOut,
	}, nil
}

func ParseFocus(args []string) (PaneTargetOptions, error) {
	return parsePaneTarget("focus", args)
}

func ParseRestart(args []string) (PaneTargetOptions, error) {
	return parsePaneTarget("restart", args)
}

func ParseRerun(args []string) (PaneTargetOptions, error) {
	return parsePaneTarget("rerun", args)
}

func ParseStop(args []string) (PaneTargetOptions, error) {
	return parsePaneTarget("stop", args)
}

func ParseKill(args []string) (PaneTargetOptions, error) {
	return parsePaneTarget("kill", args)
}

func ParseCapture(args []string) (CaptureOptions, error) {
	tail := 0
	jsonOut := false
	stripEmpty := false
	positional := make([]string, 0, 2)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--strip-empty":
			stripEmpty = true
		case "--tail":
			if i+1 >= len(args) {
				return CaptureOptions{}, fmt.Errorf("capture flag %q requires a value", arg)
			}
			parsed, err := parsePositiveIntFlag("capture", arg, args[i+1])
			if err != nil {
				return CaptureOptions{}, err
			}
			tail = parsed
			i++
		default:
			if strings.HasPrefix(arg, "-") {
				return CaptureOptions{}, fmt.Errorf("capture does not accept flag %q", arg)
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 2 {
		return CaptureOptions{}, errors.New("capture requires exactly one session name and one pane name")
	}
	return CaptureOptions{
		Session:    positional[0],
		Pane:       positional[1],
		JSON:       jsonOut,
		Tail:       tail,
		StripEmpty: stripEmpty,
	}, nil
}

func ParseSend(args []string) (SendOptions, error) {
	noEnter := false
	jsonOut := false
	positional := make([]string, 0, 2)
	command := make([]string, 0, len(args))
	commandMode := false

	for _, arg := range args {
		switch arg {
		case "--json":
			if commandMode {
				command = append(command, arg)
			} else {
				jsonOut = true
			}
		case "--no-enter":
			if commandMode {
				command = append(command, arg)
			} else {
				noEnter = true
			}
		case "--":
			commandMode = true
		default:
			if commandMode {
				command = append(command, arg)
				continue
			}
			if len(positional) < 2 {
				if strings.HasPrefix(arg, "-") {
					return SendOptions{}, fmt.Errorf("send does not accept flag %q", arg)
				}
				positional = append(positional, arg)
				continue
			}
			commandMode = true
			command = append(command, arg)
		}
	}

	if len(positional) != 2 || len(command) == 0 {
		return SendOptions{}, errors.New("send requires a session name, pane name, and command")
	}

	commandText := strings.TrimSpace(strings.Join(command, " "))
	if commandText == "" {
		return SendOptions{}, errors.New("send requires a non-empty command")
	}

	return SendOptions{
		Session: positional[0],
		Pane:    positional[1],
		Command: commandText,
		NoEnter: noEnter,
		JSON:    jsonOut,
	}, nil
}

func parsePaneTarget(name string, args []string) (PaneTargetOptions, error) {
	positional, jsonOut, err := parseOptionalJSONArgs(name, args)
	if err != nil {
		return PaneTargetOptions{}, err
	}
	if len(positional) != 2 {
		return PaneTargetOptions{}, fmt.Errorf("%s requires exactly one session name and one pane name", name)
	}
	return PaneTargetOptions{
		Session: positional[0],
		Pane:    positional[1],
		JSON:    jsonOut,
	}, nil
}

func parseCommon(name string, args []string) (CommonOptions, error) {
	var params paramFlags
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	verbose := fs.Bool("verbose", false, "")
	strict := fs.Bool("strict", false, "")
	fs.Var(&params, "param", "")

	normalized, err := normalizeArgs(args)
	if err != nil {
		return CommonOptions{}, err
	}

	if err := fs.Parse(normalized); err != nil {
		return CommonOptions{}, err
	}
	if fs.NArg() != 1 {
		return CommonOptions{}, fmt.Errorf("%s requires exactly one config path", name)
	}

	return CommonOptions{
		ConfigPath: fs.Arg(0),
		Params:     params.values,
		Strict:     *strict,
		Verbose:    *verbose,
	}, nil
}

func normalizeArgs(args []string) ([]string, error) {
	normalized := make([]string, 0, len(args))
	configPath := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--param":
			if i+1 >= len(args) {
				return nil, errors.New("--param requires a value")
			}
			normalized = append(normalized, arg, args[i+1])
			i++
		case strings.HasPrefix(arg, "-"):
			normalized = append(normalized, arg)
		default:
			if configPath != "" {
				return nil, errors.New("expected exactly one config path")
			}
			configPath = arg
		}
	}

	if configPath == "" {
		return nil, errors.New("missing config path")
	}

	normalized = append(normalized, configPath)
	return normalized, nil
}

func parseOptionalJSONArgs(name string, args []string, custom ...func(flag string, value string) error) ([]string, bool, error) {
	positional := make([]string, 0, len(args))
	jsonOut := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(arg, "-") {
				if len(custom) == 0 {
					return nil, false, fmt.Errorf("%s does not accept flag %q", name, arg)
				}
				if i+1 >= len(args) {
					return nil, false, fmt.Errorf("%s flag %q requires a value", name, arg)
				}
				if err := custom[0](arg, args[i+1]); err != nil {
					return nil, false, err
				}
				i++
				continue
			}
			positional = append(positional, arg)
		}
	}

	return positional, jsonOut, nil
}

func parsePositiveIntFlag(command string, flagName string, value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s flag %q requires a positive integer", command, flagName)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s flag %q requires a positive integer", command, flagName)
	}
	return parsed, nil
}
