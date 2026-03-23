package validate

import (
	"path/filepath"
	"testing"

	"agentmux/internal/config"
)

func TestExampleConfigsValidate(t *testing.T) {
	t.Parallel()

	paths := []string{
		filepath.Join("..", "..", "testdata", "examples", "minimal.yaml"),
		filepath.Join("..", "..", "testdata", "examples", "ai-dev.yaml"),
		filepath.Join("..", "..", "testdata", "examples", "on-exit.yaml"),
		filepath.Join("..", "..", "testdata", "examples", "review-loop.yaml"),
		filepath.Join("..", "..", "testdata", "examples", "ssh-ops.yaml"),
	}

	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			doc, err := config.LoadFile(path)
			if err != nil {
				t.Fatalf("LoadFile returned error: %v", err)
			}

			issues := Document(doc, nil)
			if HasErrors(issues) {
				t.Fatalf("example %s has validation errors: %v", path, issues)
			}
		})
	}
}
