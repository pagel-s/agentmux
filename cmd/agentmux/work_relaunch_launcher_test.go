package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestPublicRelaunchLauncherIsAbsoluteExplicitAndScoped(t *testing.T) {
	o, err := parseWorkOptions([]string{"native", "--state", "/tmp/selected-workspace", "--native-launcher", "/tmp/public launcher", "--resume", "last"})
	if err != nil || o.nativeLauncher != "/tmp/public launcher" || o.resume != "last" {
		t.Fatalf("public launcher/resume options changed: %+v err=%v", o, err)
	}
	for _, args := range [][]string{
		{"native", "--native-launcher", "relative/native"},
		{"native", "--native-launcher"},
		{"native", "--native-launcher", "/first", "--native-launcher", "/second"},
	} {
		if _, err := parseWorkOptions(args); err == nil {
			t.Fatalf("invalid recovery selection accepted: %q", args)
		}
	}
	var diagnostics bytes.Buffer
	if code := runWorkSession("start", []string{"--native-launcher", "/tmp/public-native"}, strings.NewReader(""), io.Discard, &diagnostics); code != exitValidation || !strings.Contains(diagnostics.String(), "applies only to relaunch") {
		t.Fatalf("another command silently ignored launcher recovery: code=%d diagnostics=%s", code, diagnostics.String())
	}
	var help bytes.Buffer
	if code := runWorkRelaunch([]string{"--help"}, &help, io.Discard); code != exitSuccess {
		t.Fatal(code)
	}
	for _, text := range []string{"--native-launcher ABSOLUTE_PATH", "this attempt only", "trailing arguments", "running controller"} {
		if !strings.Contains(help.String(), text) {
			t.Fatalf("public recovery help omits %q", text)
		}
	}
}
