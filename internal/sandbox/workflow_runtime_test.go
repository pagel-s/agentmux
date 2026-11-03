package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/harness"
)

func TestWorkflowRuntimeOverlapsBindSourcesDestinationsAndAncestors(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(root, "runtime-alias")
	if err := os.Symlink("/usr", alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/usr/local/project", "/etc/ssl", alias, filepath.Join(alias, "nonexistent-project")} {
		if !RuntimeOverlaps(path) {
			t.Fatalf("protected path overlaps a system runtime bind: %s", path)
		}
	}
	for _, path := range []string{root, filepath.Join(root, "not-created")} {
		if RuntimeOverlaps(path) {
			t.Fatalf("private path falsely overlaps system runtime: %s", path)
		}
	}
	args := runtimeMounts()
	for i := 0; i+2 < len(args); i += 3 {
		for _, path := range args[i+1 : i+3] {
			canonical, err := filepath.EvalSymlinks(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, selected := range []string{path, canonical, filepath.Dir(path), filepath.Dir(canonical)} {
				if !RuntimeOverlaps(selected) {
					t.Fatalf("runtime bind source/destination or ancestor not detected: %s", selected)
				}
			}
		}
	}
}

func TestWorkflowBoundaryRefusesRuntimeStateWorkspaceAndSource(t *testing.T) {
	root, workspace, options := workflowBoundaryFixture(t)
	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte("#!/bin/sh\nexit 125\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+":/usr/bin:/bin")
	spec := harness.Spec{Name: "command", Binary: "/usr/bin/true", Workdir: workspace}
	for _, change := range []func(*WorkflowOptions){
		func(o *WorkflowOptions) { o.StateDir = "/usr/local/private-run" },
		func(o *WorkflowOptions) { o.Workspace = "/usr/local/private-run/worktree" },
		func(o *WorkflowOptions) { o.ControllerSource = "/etc/ssl" },
	} {
		bad := options
		change(&bad)
		if _, err := WrapWorkflow(spec, bad); err == nil || !strings.Contains(err.Error(), "runtime mounts") {
			t.Fatalf("runtime overlap not refused before building mounts: %+v %v", bad, err)
		}
	}
}

func TestWorkflowBoundaryRefusesOriginalGitExecutableFromExplicitPATH(t *testing.T) {
	root, workspace, options := workflowBoundaryFixture(t)
	tools := filepath.Join(root, "tools")
	metadata := filepath.Join(root, "source.git")
	hooks := filepath.Join(metadata, "hooks")
	for _, dir := range []string{tools, hooks} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(tools, "bwrap"), filepath.Join(hooks, "source-script")} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 125\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools+":/usr/bin:/bin")
	options.ProtectedSourceGitPaths = []string{metadata}
	spec := harness.Spec{Name: "command", Binary: "source-script", Env: []string{"PATH=" + hooks}, Workdir: workspace}
	if _, err := WrapWorkflow(spec, options); err == nil || !strings.Contains(err.Error(), "original source Git metadata") {
		t.Fatalf("explicit native PATH exposed original Git executable: %v", err)
	}
}

func TestWorkflowBoundarySourceDenyChecksCanonicalAndNamespacePaths(t *testing.T) {
	root, workspace, options := workflowBoundaryFixture(t)
	tools := filepath.Join(root, "tools")
	source := filepath.Join(root, "source")
	external := filepath.Join(root, "external")
	for _, path := range []string{tools, source, external} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte("#!/bin/sh\nexit 125\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+":/usr/bin:/bin")
	insideAlias := filepath.Join(source, "external-alias")
	outsideAlias := filepath.Join(root, "source-alias")
	if err := os.Symlink(external, insideAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, outsideAlias); err != nil {
		t.Fatal(err)
	}
	options.ProtectedSourcePaths = []string{source}
	spec := harness.Spec{Name: "command", Binary: "/usr/bin/true", Workdir: workspace}
	for _, path := range []string{source, insideAlias, outsideAlias, root} {
		options.ReadOnlyPaths = []string{path}
		if _, err := WrapWorkflow(spec, options); err == nil || !strings.Contains(err.Error(), "original source/input paths") {
			t.Fatalf("source namespace/canonical dependency accepted: %s %v", path, err)
		}
	}
	options.ReadOnlyPaths = []string{external}
	if _, err := WrapWorkflow(spec, options); err != nil {
		t.Fatalf("unrelated external dependency refused: %v", err)
	}
}
