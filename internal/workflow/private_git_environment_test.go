package workflow

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestPrivateGitIsolationEnvironmentRetainsOnlyExactDenyValues(t *testing.T) {
	allowed := []string{"PATH=/usr/bin", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "GIT_NO_REPLACE_OBJECTS=1"}
	input := append(append([]string(nil), allowed...), "GIT_ALLOW_PROTOCOL=file", "GIT_NO_LAZY_FETCH=0", "GIT_CONFIG_NOSYSTEM=0", "GIT_TERMINAL_PROMPT=1", "GIT_NO_REPLACE_OBJECTS=0", "GIT_NO_REPLACE_OBJECTS=", "GIT_CONFIG_GLOBAL=/outside/config", "GIT_DIR=/outside/metadata", "GIT_CONFIG_COUNT=1")
	if got := isolationGitEnvironment(input); !reflect.DeepEqual(got, allowed) {
		t.Fatalf("isolation stripped controller transport denial or admitted Git redirection: %q", got)
	}
}

func TestPrivateTrustedSourceEnumerationAcceptsExactDenyValues(t *testing.T) {
	repo, _ := fixtureRepo(t)
	cmd := exec.Command("git", "-C", repo, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "ls-files", "--cached", "-z")
	allowed := []string{"PATH=/usr/bin", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "GIT_NO_REPLACE_OBJECTS=1"}
	cmd.Env = append([]string(nil), allowed...)
	if !trustedSourceEnumeration(cmd, repo) {
		t.Fatal("controller transport-denial constants disabled trusted source enumeration")
	}
	for _, entry := range []string{"GIT_ALLOW_PROTOCOL=file", "GIT_NO_LAZY_FETCH=0", "GIT_CONFIG_NOSYSTEM=0", "GIT_TERMINAL_PROMPT=1", "GIT_NO_REPLACE_OBJECTS=0", "GIT_NO_REPLACE_OBJECTS=", "GIT_CONFIG_GLOBAL=/outside/config", "GIT_DIR=/outside/metadata"} {
		cmd.Env = append(append([]string(nil), allowed...), entry)
		if trustedSourceEnumeration(cmd, repo) {
			t.Fatalf("trusted source enumeration admitted unsafe Git environment %q", entry)
		}
	}
}
