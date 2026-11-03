package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"agentmux/internal/workflow"
)

// Tests replace only the desktop handoff; selection, rendering and validation
// still run against the saved evidence without starting a browser.
var guidedHistoryReportOpener = openGuidedReportURL

func openGuidedHistoryReport(ctx context.Context, directory, id string, out io.Writer) error {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("selected run must be a regular directory")
	}
	private, err := os.MkdirTemp(directory, ".report-open-")
	if err != nil {
		return fmt.Errorf("create private report directory: %w", err)
	}
	path := filepath.Join(private, "report.html")
	if _, err = workflow.RenderRunReportToContext(ctx, directory, path, id); err != nil {
		// The renderer writes exclusively. Remove an empty directory after a
		// pre-publication refusal, but retain any partial evidence on failure.
		_ = os.Remove(private)
		return fmt.Errorf("render selected run report: %w", err)
	}
	fileURL, err := guidedHistoryReportURL(directory, path)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Fresh report for run %s:\n%s\n", guidedLine(id), guidedLine(path))
	if err = guidedHistoryReportOpener(ctx, fileURL); err != nil {
		return fmt.Errorf("could not open the browser; the fresh report is retained at %q: %w", path, err)
	}
	return nil
}

func guidedHistoryReportURL(directory, path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("fresh report must be a regular file inside the selected run")
	}
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, actual)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("fresh report must stay inside the selected run")
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(actual)}).String(), nil
}

func openGuidedReportURL(ctx context.Context, fileURL string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, opener, fileURL)
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		return fmt.Errorf("desktop opener: %w", err)
	}
	return nil
}
