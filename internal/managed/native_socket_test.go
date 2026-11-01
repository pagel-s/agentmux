package managed

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeSandboxSocketRetainsMountedInodeAcrossBrokerRestart(t *testing.T) {
	dir, err := os.MkdirTemp("", "amx-relay-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	backend := filepath.Join(dir, "broker.sock")
	start := func(reply string) net.Listener {
		l, err := net.Listen("unix", backend)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				conn.Write([]byte(reply))
				conn.Close()
			}
		}()
		return l
	}
	first := start("before restart")
	defer first.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	front, cleanup, err := nativeSocketRelay(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	info, err := os.Stat(front)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("relay socket is not private")
	}
	read := func(want string) {
		t.Helper()
		conn, err := net.DialTimeout("unix", front, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		body, err := io.ReadAll(conn)
		if err != nil || string(body) != want {
			t.Fatalf("relay reply %q %v", body, err)
		}
	}
	read("before restart")
	first.Close()
	second := start("after restart")
	defer second.Close()
	read("after restart")
	after, err := os.Stat(front)
	if err != nil || !os.SameFile(info, after) {
		t.Fatal("front socket inode changed across backend replacement")
	}
	cleanup()
	if _, err := os.Stat(front); !os.IsNotExist(err) {
		t.Fatal("private relay was not cleaned up")
	}
}
