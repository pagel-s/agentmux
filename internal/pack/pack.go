package pack

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agentmux/internal/config"
)

func CreateBundle(configPath string, outputPath string) (string, error) {
	doc, err := config.LoadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("load config for packing: %w", err)
	}

	if outputPath == "" {
		session := doc.Config.Session
		if session == "" {
			session = "session"
		}
		outputPath = session + ".amx"
	}

	if !strings.HasSuffix(outputPath, ".amx") {
		outputPath += ".amx"
	}

	baseDir := filepath.Dir(doc.Path)

	outFile, err := os.Create(outputPath)
	if err != nil {
		return "", err
	}
	defer outFile.Close()

	w := zip.NewWriter(outFile)
	defer w.Close()

	walker := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}

		// Skip common metadata and self
		if info.IsDir() {
			if relPath == ".git" || relPath == ".cache" || relPath == ".tmp" || relPath == "bin" {
				return filepath.SkipDir
			}
			return nil
		}

		// Don't include the output bundle itself if it's in the same dir
		absOut, _ := filepath.Abs(outputPath)
		absPath, _ := filepath.Abs(path)
		if absOut == absPath {
			return nil
		}

		if strings.HasSuffix(relPath, ".amx") {
			return nil
		}

		f, err := w.Create(relPath)
		if err != nil {
			return err
		}

		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()

		_, err = io.Copy(f, in)
		return err
	}

	err = filepath.Walk(baseDir, walker)
	if err != nil {
		return "", err
	}

	return outputPath, nil
}
