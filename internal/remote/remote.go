package remote

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func IsRemote(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")
}

func Fetch(url string) (string, error) {
	fmt.Printf("fetching remote configuration from %s...\n", url)

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned status %s", resp.Status)
	}

	// Determine extension from URL or Content-Type
	ext := filepath.Ext(url)
	if ext == "" {
		ct := resp.Header.Get("Content-Type")
		if strings.Contains(ct, "application/zip") || strings.Contains(ct, "application/octet-stream") {
			ext = ".amx"
		} else {
			ext = ".yaml"
		}
	}

	tmpFile, err := os.CreateTemp("", "agentmux-remote-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {
		return "", fmt.Errorf("save download: %w", err)
	}

	return tmpFile.Name(), nil
}
