package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var knownTopLevelFields = map[string]struct{}{
	"version":     {},
	"name":        {},
	"session":     {},
	"description": {},
	"include":     {},
	"requires":    {},
	"params":      {},
	"env":         {},
	"agents":      {},
	"layout":      {},
	"hooks":       {},
}

type Config struct {
	Version     int               `yaml:"version"`
	Name        string            `yaml:"name"`
	Session     string            `yaml:"session"`
	Description string            `yaml:"description"`
	Include     []string          `yaml:"include"`
	Requires    Requirements      `yaml:"requires"`
	Params      map[string]string `yaml:"params"`
	Env         map[string]string `yaml:"env"`
	Agents      map[string]Agent  `yaml:"agents"`
	Layout      []Window          `yaml:"layout"`
	Hooks       Hooks             `yaml:"hooks"`
}

type Requirements struct {
	Binaries []string `yaml:"binaries"`
	Env      []string `yaml:"env"`
	Params   []string `yaml:"params"`
}

type Agent struct {
	Provider string            `yaml:"provider"`
	Command  string            `yaml:"command"`
	Cwd      string            `yaml:"cwd"`
	Env      map[string]string `yaml:"env"`
	Title    string            `yaml:"title"`
	Shell    string            `yaml:"shell"`
	OnExit   string            `yaml:"on_exit"`
}

type Window struct {
	Window      string            `yaml:"window"`
	Root        string            `yaml:"root"`
	Layout      string            `yaml:"layout"`
	Env         map[string]string `yaml:"env"`
	GridRows    int               `yaml:"grid_rows"`
	GridColumns int               `yaml:"grid_columns"`
	Panes       []Pane            `yaml:"panes"`
}

type Pane struct {
	Name    string            `yaml:"name"`
	Agent   string            `yaml:"agent"`
	Command string            `yaml:"command"`
	Title   string            `yaml:"title"`
	Cwd     string            `yaml:"cwd"`
	Env     map[string]string `yaml:"env"`
	Focus   bool              `yaml:"focus"`
	OnExit  string            `yaml:"on_exit"`
}

type Hooks struct {
	PreSession  string `yaml:"pre_session"`
	PostSession string `yaml:"post_session"`
}

type Document struct {
	Path              string
	Config            Config
	UnknownTopLevel   []string
	DuplicateAgentIDs []string
}

const (
	OnExitShell   = "shell"
	OnExitRemain  = "remain"
	OnExitRestart = "restart"
	OnExitKill    = "kill"
)

func NormalizeOnExit(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func IsValidOnExit(value string) bool {
	switch NormalizeOnExit(value) {
	case "", OnExitShell, OnExitRemain, OnExitRestart, OnExitKill:
		return true
	default:
		return false
	}
}

func LoadFile(path string) (*Document, error) {
	return loadRecursive(path, make(map[string]bool))
}

func loadRecursive(path string, seen map[string]bool) (*Document, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", path, err)
	}
	if seen[absPath] {
		return nil, fmt.Errorf("circular include detected: %s", absPath)
	}
	seen[absPath] = true

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read config %q: %w", absPath, err)
	}

	var node yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&node); err != nil {
		return nil, fmt.Errorf("invalid YAML in %q: %w", absPath, err)
	}

	var cfg Config
	if len(node.Content) == 0 {
		return nil, fmt.Errorf("config %q is empty", absPath)
	}
	if node.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config %q must be a YAML mapping at the top level", absPath)
	}
	if err := node.Content[0].Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config in %q: %w", absPath, err)
	}

	doc := &Document{
		Path:            absPath,
		Config:          cfg,
		UnknownTopLevel: unknownTopLevelFields(node.Content[0]),
	}
	if agentsNode := findMappingValue(node.Content[0], "agents"); agentsNode != nil {
		doc.DuplicateAgentIDs = duplicateMappingKeys(agentsNode)
	}

	// Handle includes
	baseDir := filepath.Dir(absPath)
	for _, incPath := range cfg.Include {
		fullIncPath := incPath
		if !filepath.IsAbs(incPath) {
			fullIncPath = filepath.Join(baseDir, incPath)
		}

		incDoc, err := loadRecursive(fullIncPath, seen)
		if err != nil {
			return nil, fmt.Errorf("in %q: include %q: %w", absPath, incPath, err)
		}

		mergeConfigs(&doc.Config, &incDoc.Config)
		doc.UnknownTopLevel = append(doc.UnknownTopLevel, incDoc.UnknownTopLevel...)
		doc.DuplicateAgentIDs = append(doc.DuplicateAgentIDs, incDoc.DuplicateAgentIDs...)
	}

	return doc, nil
}

func mergeConfigs(target, source *Config) {
	// Requirements (Combine all)
	target.Requires.Binaries = combineUnique(target.Requires.Binaries, source.Requires.Binaries)
	target.Requires.Env = combineUnique(target.Requires.Env, source.Requires.Env)
	target.Requires.Params = combineUnique(target.Requires.Params, source.Requires.Params)

	// Params (target wins)
	if target.Params == nil {
		target.Params = make(map[string]string)
	}
	for k, v := range source.Params {
		if _, ok := target.Params[k]; !ok {
			target.Params[k] = v
		}
	}

	// Env (target wins)
	if target.Env == nil {
		target.Env = make(map[string]string)
	}
	for k, v := range source.Env {
		if _, ok := target.Env[k]; !ok {
			target.Env[k] = v
		}
	}

	// Agents (target wins)
	if target.Agents == nil {
		target.Agents = make(map[string]Agent)
	}
	for k, v := range source.Agents {
		if _, ok := target.Agents[k]; !ok {
			target.Agents[k] = v
		}
	}

	// Layout (source appends before target)
	target.Layout = append(source.Layout, target.Layout...)

	// Hooks (target wins if not empty)
	if target.Hooks.PreSession == "" {
		target.Hooks.PreSession = source.Hooks.PreSession
	}
	if target.Hooks.PostSession == "" {
		target.Hooks.PostSession = source.Hooks.PostSession
	}
}

func combineUnique(target, source []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(target)+len(source))
	for _, v := range target {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	for _, v := range source {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}

func unknownTopLevelFields(root *yaml.Node) []string {
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}

	var unknown []string
	for i := 0; i < len(root.Content); i += 2 {
		key := root.Content[i].Value
		if _, ok := knownTopLevelFields[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func duplicateMappingKeys(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}

	counts := make(map[string]int)
	for i := 0; i < len(node.Content); i += 2 {
		counts[node.Content[i].Value]++
	}

	var duplicates []string
	for key, count := range counts {
		if count > 1 {
			duplicates = append(duplicates, key)
		}
	}
	sort.Strings(duplicates)
	return duplicates
}

func findMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}

	return nil
}
