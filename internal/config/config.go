package config

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var knownTopLevelFields = map[string]struct{}{
	"version":     {},
	"name":        {},
	"session":     {},
	"description": {},
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
	Params      map[string]string `yaml:"params"`
	Env         map[string]string `yaml:"env"`
	Agents      map[string]Agent  `yaml:"agents"`
	Layout      []Window          `yaml:"layout"`
	Hooks       Hooks             `yaml:"hooks"`
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unable to read config %q: %w", path, err)
	}

	var node yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&node); err != nil {
		return nil, fmt.Errorf("invalid YAML in %q: %w", path, err)
	}

	var cfg Config
	if len(node.Content) == 0 {
		return nil, fmt.Errorf("config %q is empty", path)
	}
	if node.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config %q must be a YAML mapping at the top level", path)
	}
	if err := node.Content[0].Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config in %q: %w", path, err)
	}

	doc := &Document{
		Path:            path,
		Config:          cfg,
		UnknownTopLevel: unknownTopLevelFields(node.Content[0]),
	}
	if agentsNode := findMappingValue(node.Content[0], "agents"); agentsNode != nil {
		doc.DuplicateAgentIDs = duplicateMappingKeys(agentsNode)
	}

	return doc, nil
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
