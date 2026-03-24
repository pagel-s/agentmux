package scaffold

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

//go:embed templates/*.yaml
var templateFS embed.FS

type Template struct {
	Name        string
	Description string
	Content     string
}

type InitOptions struct {
	Force bool
}

var descriptions = map[string]string{
	"ai-dev":          "Multi-window coding workspace with coder and reviewer agents",
	"coding-starter":  "Minimal copy-and-tweak coding workspace",
	"minimal":         "Smallest valid starter config",
	"on-exit":         "Pane exit-policy demo with shell, remain, restart, and kill",
	"review-loop":     "Coding and review loop with reusable agents",
	"review-starter":  "Minimal copy-and-tweak review workspace",
	"ssh-ops":         "Remote operations starter with an SSH-driven agent",
}

var aliases = map[string]string{
	"default": "ai-dev",
}

var sessionCharPattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func List() ([]Template, error) {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		return nil, err
	}

	templates := make([]Template, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".yaml")
		content, err := templateFS.ReadFile(filepath.Join("templates", entry.Name()))
		if err != nil {
			return nil, err
		}
		templates = append(templates, Template{
			Name:        name,
			Description: descriptions[name],
			Content:     string(content),
		})
	}

	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Name < templates[j].Name
	})
	return templates, nil
}

func Lookup(name string) (Template, bool, error) {
	if target, ok := aliases[name]; ok {
		name = target
	}
	templates, err := List()
	if err != nil {
		return Template{}, false, err
	}
	for _, template := range templates {
		if template.Name == name {
			return template, true, nil
		}
	}
	return Template{}, false, nil
}

func Init(templateName string, targetPath string, opts InitOptions) (string, string, error) {
	template, ok, err := Lookup(templateName)
	if err != nil {
		return "", "", err
	}
	if !ok {
		names, namesErr := Names()
		if namesErr != nil {
			return "", "", fmt.Errorf("unknown template %q", templateName)
		}
		return "", "", fmt.Errorf("unknown template %q; available templates: %s", templateName, strings.Join(names, ", "))
	}

	expandedTarget := expandUserPath(targetPath)
	absTarget, err := filepath.Abs(expandedTarget)
	if err != nil {
		return "", "", fmt.Errorf("resolve target path %q: %w", targetPath, err)
	}

	if !opts.Force {
		if _, err := os.Stat(absTarget); err == nil {
			return "", "", fmt.Errorf("target %q already exists; rerun with --force to overwrite", absTarget)
		} else if !os.IsNotExist(err) {
			return "", "", fmt.Errorf("inspect target %q: %w", absTarget, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(absTarget), 0o755); err != nil {
		return "", "", fmt.Errorf("create parent directories for %q: %w", absTarget, err)
	}

	sessionName := deriveSessionName(absTarget, template.Name)
	content := rewriteSession(template.Content, sessionName)
	if err := os.WriteFile(absTarget, []byte(content), 0o644); err != nil {
		return "", "", fmt.Errorf("write template %q to %q: %w", templateName, absTarget, err)
	}

	return absTarget, sessionName, nil
}

func Names() ([]string, error) {
	templates, err := List()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(templates)+len(aliases))
	for _, template := range templates {
		names = append(names, template.Name)
	}
	for alias := range aliases {
		names = append(names, alias)
	}
	sort.Strings(names)
	return names, nil
}

func Aliases() map[string]string {
	out := make(map[string]string, len(aliases))
	for alias, target := range aliases {
		out[alias] = target
	}
	return out
}

func expandUserPath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func deriveSessionName(targetPath string, fallback string) string {
	base := strings.TrimSuffix(filepath.Base(targetPath), filepath.Ext(targetPath))
	base = strings.TrimSpace(strings.ToLower(base))
	if base == "" {
		return fallback
	}
	base = sessionCharPattern.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-.")
	if base == "" {
		return fallback
	}
	return base
}

func rewriteSession(content string, sessionName string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "session:") {
			lines[i] = "session: " + sessionName
			return strings.Join(lines, "\n")
		}
	}
	return content
}
