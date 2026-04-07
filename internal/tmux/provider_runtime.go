package tmux

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	prov "agentmux/internal/provider"
	"agentmux/internal/resolve"
)

var runtimeComponentPattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

const geminiPersonaFileName = "AGENTMUX_PERSONA.md"

type providerRuntimeDescriptor struct {
	Env                map[string]string
	IncludeDirectories []string
	PersonaSource      string
	PersonaArtifact    string
}

type ProviderRuntime struct {
	PersonaSource   string
	PersonaArtifact string
}

func prepareProviderRuntime(session string, pane resolve.Pane, profile prov.LaunchProfile) error {
	descriptor := describeProviderRuntime(session, pane, profile)

	if profile.Spec.ID == "gemini" && profile.Mode == string(prov.LaunchModeNativeMCP) {
		path := geminiSystemSettingsPath(session, pane)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create Gemini runtime settings directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(geminiSystemSettingsJSON(session, pane)), 0o644); err != nil {
			return fmt.Errorf("write Gemini runtime settings: %w", err)
		}
	}

	if descriptor.PersonaArtifact != "" {
		content, err := os.ReadFile(pane.Persona)
		if err != nil {
			return fmt.Errorf("read persona file %q: %w", pane.Persona, err)
		}
		if err := os.MkdirAll(filepath.Dir(descriptor.PersonaArtifact), 0o755); err != nil {
			return fmt.Errorf("create persona artifact directory: %w", err)
		}
		if err := os.WriteFile(descriptor.PersonaArtifact, content, 0o644); err != nil {
			return fmt.Errorf("write persona artifact %q: %w", descriptor.PersonaArtifact, err)
		}
	}

	return nil
}

func prepareProviderRuntimeForPaneStatus(session string, status PaneStatus) error {
	if status.Provider == "" || status.IntegrationMode == "" {
		return nil
	}
	profile := prov.ProfileForMode(status.Provider, status.IntegrationMode)
	if profile.Spec.ID == "" {
		return nil
	}
	return prepareProviderRuntime(session, resolve.Pane{
		Name:      status.Name,
		AgentName: status.AgentName,
		Provider:  status.Provider,
		Persona:   status.Persona,
	}, profile)
}

func providerRuntimeEnv(session string, profile prov.LaunchProfile) map[string]string {
	return describeProviderRuntime(session, resolve.Pane{Provider: profile.Spec.ID}, profile).Env
}

func DescribeProviderRuntime(session string, pane resolve.Pane) ProviderRuntime {
	profile := resolveLaunchProfile(pane.Provider)
	descriptor := describeProviderRuntime(session, pane, profile)
	return ProviderRuntime{
		PersonaSource:   descriptor.PersonaSource,
		PersonaArtifact: descriptor.PersonaArtifact,
	}
}

func describeProviderRuntime(session string, pane resolve.Pane, profile prov.LaunchProfile) providerRuntimeDescriptor {
	descriptor := providerRuntimeDescriptor{}
	if strings.TrimSpace(pane.Persona) != "" {
		descriptor.PersonaSource = profile.PersonaSource
	}

	switch {
	case profile.Spec.ID == "gemini" && profile.Mode == string(prov.LaunchModeNativeMCP):
		descriptor.Env = map[string]string{
			"GEMINI_CLI_SYSTEM_SETTINGS_PATH": geminiSystemSettingsPath(session, pane),
		}
		if strings.TrimSpace(pane.Persona) != "" {
			artifact := geminiPersonaPath(session, resolve.Pane{
				Name:      pane.Name,
				AgentName: pane.AgentName,
				Provider:  profile.Spec.ID,
				Persona:   pane.Persona,
			})
			descriptor.IncludeDirectories = []string{filepath.Dir(artifact)}
			descriptor.PersonaArtifact = artifact
		}
	}

	return descriptor
}

func geminiSystemSettingsPath(session string, pane resolve.Pane) string {
	root := filepath.Join(os.TempDir(), "agentmux", "gemini")
	sessionName := sanitizeRuntimeComponent(session)
	if sessionName == "" {
		sessionName = "default"
	}
	return filepath.Join(root, sessionName, "panes", runtimePaneKey(pane.Name, pane.AgentName, prov.Normalize(pane.Provider), pane.Persona), "settings.json")
}

func geminiSystemSettingsJSON(session string, pane resolve.Pane) string {
	server := agentmuxMCPServerConfig(session, pane)
	payload := map[string]any{
		"mcpServers": map[string]any{
			"agentmux": map[string]any{
				"command": server.Command,
				"args":    server.Args,
				"env":     server.Env,
			},
		},
		"context": map[string]any{
			"fileName":                         []string{geminiPersonaFileName, "GEMINI.md"},
			"loadMemoryFromIncludeDirectories": true,
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "{\n  \"mcpServers\": {\n    \"agentmux\": {\n      \"command\": \"agentmux\",\n      \"args\": [\n        \"mcp-server\"\n      ],\n      \"env\": {\n        \"AGENTMUX_SESSION\": \"default\"\n      }\n    }\n  },\n  \"context\": {\n    \"fileName\": [\n      \"AGENTMUX_PERSONA.md\",\n      \"GEMINI.md\"\n    ],\n    \"loadMemoryFromIncludeDirectories\": true\n  }\n}"
	}
	return string(data)
}

func geminiPersonaPath(session string, pane resolve.Pane) string {
	root := filepath.Join(os.TempDir(), "agentmux", "gemini")
	name := sanitizeRuntimeComponent(session)
	if name == "" {
		name = "default"
	}
	return filepath.Join(root, name, "personas", runtimePaneKey(pane.Name, pane.AgentName, pane.Provider, pane.Persona), geminiPersonaFileName)
}

func sanitizeRuntimeComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = runtimeComponentPattern.ReplaceAllString(value, "_")
	value = strings.Trim(value, "._-")
	return value
}

func runtimePaneKey(name string, agentName string, provider string, persona string) string {
	for _, candidate := range []string{name, agentName} {
		if value := sanitizeRuntimeComponent(candidate); value != "" {
			return value
		}
	}

	sum := sha1.Sum([]byte(strings.Join([]string{provider, persona}, "\x00")))
	return "pane-" + hex.EncodeToString(sum[:6])
}
