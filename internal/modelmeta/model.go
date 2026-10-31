// Package modelmeta holds bounded, explicit model transport metadata. Capability
// declarations describe a model; they grant no Agentmux or filesystem authority.
package modelmeta

import "errors"

type Metadata struct {
	ContextWindow int      `yaml:"context_window,omitempty" json:"context_window,omitempty"`
	MaxOutput     int      `yaml:"max_output,omitempty" json:"max_output,omitempty"`
	Input         []string `yaml:"input,omitempty" json:"input,omitempty"`
	Reasoning     *bool    `yaml:"reasoning,omitempty" json:"reasoning,omitempty"`
	Tools         *bool    `yaml:"tools,omitempty" json:"tools,omitempty"`
}

func (m Metadata) Empty() bool {
	return m.ContextWindow == 0 && m.MaxOutput == 0 && len(m.Input) == 0 && m.Reasoning == nil && m.Tools == nil
}
func (m Metadata) Validate() error {
	if m.ContextWindow != 0 && (m.ContextWindow < 512 || m.ContextWindow > 8388608) {
		return errors.New("model_metadata.context_window must be between 512 and 8388608")
	}
	if m.MaxOutput < 0 || m.MaxOutput > 1048576 {
		return errors.New("model_metadata.max_output must be between 1 and 1048576, or omitted")
	}
	if m.ContextWindow != 0 && m.MaxOutput > m.ContextWindow {
		return errors.New("model_metadata.max_output exceeds context_window")
	}
	if m.ContextWindow == 0 && m.MaxOutput > 8192 {
		return errors.New("model_metadata.max_output above the fallback context requires explicit context_window")
	}
	seen := map[string]bool{}
	for _, kind := range m.Input {
		if (kind != "text" && kind != "image") || seen[kind] {
			return errors.New("model_metadata.input must contain distinct text/image modalities")
		}
		seen[kind] = true
	}
	if len(m.Input) > 0 && !seen["text"] {
		return errors.New("model_metadata.input must include text for the chat adapter")
	}
	return nil
}

// Overlay preserves explicit false and applies only fields actually supplied.
func Overlay(base, override Metadata) Metadata {
	if override.ContextWindow != 0 {
		base.ContextWindow = override.ContextWindow
	}
	if override.MaxOutput != 0 {
		base.MaxOutput = override.MaxOutput
	}
	if len(override.Input) > 0 {
		base.Input = append([]string(nil), override.Input...)
	}
	if override.Reasoning != nil {
		value := *override.Reasoning
		base.Reasoning = &value
	}
	if override.Tools != nil {
		value := *override.Tools
		base.Tools = &value
	}
	return base
}
func (m Metadata) Effective() Metadata {
	no := false
	result := Overlay(Metadata{ContextWindow: 8192, MaxOutput: 2048, Input: []string{"text"}, Reasoning: &no}, m)
	if m.MaxOutput == 0 && result.MaxOutput >= result.ContextWindow {
		result.MaxOutput = result.ContextWindow / 4
	}
	return result
}
