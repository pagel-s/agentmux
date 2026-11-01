//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func workerResultPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("PTY unavailable: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	return master, slave
}

func TestManagedWorkerRealTerminalLeavesExitGuidanceInsteadOfJSON(t *testing.T) {
	for _, tc := range []struct {
		name, expected string
		native         bool
		err            error
		code           int
	}{
		{"normal-native", "exited normally (exit 0)", true, nil, exitSuccess},
		{"failed-native", "stopped with an error", true, errors.New("exit status 7"), exitRuntime},
		{"cancelled-native", "stopped after cancellation", true, context.Canceled, exitSuccess},
		{"normal-managed", "Worker", false, nil, exitSuccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave := workerResultPTY(t)
			if !managedWorkerTerminal(slave) {
				t.Fatal("real PTY was not identified as a terminal")
			}
			output := make(chan string, 1)
			go func() {
				data, _ := io.ReadAll(master) // Linux returns EIO after the slave closes.
				output <- string(data)
			}()
			if code := writeManagedWorkerResult(slave, "native", tc.native, tc.err); code != tc.code {
				t.Fatalf("terminal result changed exit code: %d", code)
			}
			slave.Close()
			select {
			case text := <-output:
				if !strings.Contains(text, tc.expected) || !strings.Contains(text, "F1") || strings.Contains(text, `"schema_version"`) || strings.Contains(text, `"stopped": true`) {
					t.Fatalf("terminal still contains a raw envelope or lacks guidance: %q", text)
				}
				if tc.native && !strings.Contains(text, "Enter or Shift+U to review relaunch") {
					t.Fatalf("native exit lacks an explicit recovery route: %q", text)
				}
				if !tc.native && strings.Contains(text, "Native updates") {
					t.Fatalf("managed worker was labeled a native updater: %q", text)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("terminal exit output did not finish")
			}
		})
	}
}
