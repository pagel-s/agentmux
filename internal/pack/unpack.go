package pack

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func UnpackBundle(bundlePath string) (string, string, error) {
	tmpDir, err := os.MkdirTemp("", "agentmux-bundle-*")
	if err != nil {
		return "", "", fmt.Errorf("create temp dir for bundle: %w", err)
	}

	r, err := zip.OpenReader(bundlePath)
	if err != nil {
		return "", "", fmt.Errorf("open bundle %q: %w", bundlePath, err)
	}
	defer r.Close()

	var mainYaml string
	bundleName := strings.TrimSuffix(filepath.Base(bundlePath), ".amx")

	for _, f := range r.File {
		path := filepath.Join(tmpDir, f.Name)

		// Harden against path traversal
		if !strings.HasPrefix(path, filepath.Clean(tmpDir)+string(os.PathSeparator)) {
			return "", "", fmt.Errorf("illegal file path in bundle: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(path, f.Mode())
			continue
		}

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", "", err
		}

		rc, err := f.Open()
		if err != nil {
			return "", "", err
		}

		outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return "", "", err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			return "", "", err
		}

		// Try to identify the main YAML
		rel := f.Name
		if !strings.Contains(rel, "/") {
			ext := filepath.Ext(rel)
			if ext == ".yaml" || ext == ".yml" {
				// Perfect match if it has the same name as the bundle
				name := strings.TrimSuffix(rel, ext)
				if name == bundleName {
					mainYaml = path
				} else if mainYaml == "" {
					mainYaml = path
				}
			}
		}
	}

	if mainYaml == "" {
		return "", "", fmt.Errorf("bundle does not contain a root YAML configuration")
	}

	return tmpDir, mainYaml, nil
}
