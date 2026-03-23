package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
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

type PaneTargetOptions struct {
	Session string
	Pane    string
}

type SendOptions struct {
	Session string
	Pane    string
	Command string
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
	var params paramFlags
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
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
	if fs.NArg() != 1 {
		return LoadOptions{}, errors.New("load requires exactly one config path")
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
		Replace: *replace,
	}, nil
}

func ParseValidate(args []string) (CommonOptions, error) {
	return parseCommon("validate", args)
}

func ParseRender(args []string) (CommonOptions, error) {
	return parseCommon("render", args)
}

func ParseStatus(args []string) (string, error) {
	if len(args) != 1 {
		return "", errors.New("status requires exactly one session name")
	}
	return args[0], nil
}

func ParseFocus(args []string) (PaneTargetOptions, error) {
	if len(args) != 2 {
		return PaneTargetOptions{}, errors.New("focus requires exactly one session name and one pane name")
	}
	return PaneTargetOptions{
		Session: args[0],
		Pane:    args[1],
	}, nil
}

func ParseRestart(args []string) (PaneTargetOptions, error) {
	if len(args) != 2 {
		return PaneTargetOptions{}, errors.New("restart requires exactly one session name and one pane name")
	}
	return PaneTargetOptions{
		Session: args[0],
		Pane:    args[1],
	}, nil
}

func ParseRerun(args []string) (PaneTargetOptions, error) {
	if len(args) != 2 {
		return PaneTargetOptions{}, errors.New("rerun requires exactly one session name and one pane name")
	}
	return PaneTargetOptions{
		Session: args[0],
		Pane:    args[1],
	}, nil
}

func ParseSend(args []string) (SendOptions, error) {
	if len(args) < 3 {
		return SendOptions{}, errors.New("send requires a session name, pane name, and command")
	}

	command := strings.TrimSpace(strings.Join(args[2:], " "))
	if command == "" {
		return SendOptions{}, errors.New("send requires a non-empty command")
	}

	return SendOptions{
		Session: args[0],
		Pane:    args[1],
		Command: command,
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
