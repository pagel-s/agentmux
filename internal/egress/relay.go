package egress

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// RunRelay runs inside a worker's isolated network namespace. Only a Unix
// socket to the host policy proxy is exposed. Native clients which ignore
// HTTP(S)_PROXY have no route to the host network and fail closed.
func RunRelay(ctx context.Context, socketPath string, argv, env []string, workdir string) error {
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("egress relay requires a command")
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer l.Close()
	proxy := "http://" + l.Addr().String()
	clean := make([]string, 0, len(env)+6)
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		switch strings.ToUpper(k) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		clean = append(clean, e)
	}
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		clean = append(clean, k+"="+proxy)
	}
	var mu sync.Mutex
	active := map[net.Conn]bool{}
	slots := make(chan struct{}, 32)
	closeAll := func() {
		_ = l.Close()
		mu.Lock()
		defer mu.Unlock()
		for c := range active {
			_ = c.Close()
		}
	}
	defer closeAll()
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				c.Close()
				continue
			}
			mu.Lock()
			active[c] = true
			mu.Unlock()
			go func() {
				defer func() { <-slots; mu.Lock(); delete(active, c); mu.Unlock(); c.Close() }()
				up, e := net.Dial("unix", socketPath)
				if e != nil {
					return
				}
				defer up.Close()
				mu.Lock()
				active[up] = true
				mu.Unlock()
				defer func() { mu.Lock(); delete(active, up); mu.Unlock() }()
				done := make(chan struct{}, 1)
				go func() { _, _ = io.Copy(up, c); up.Close(); done <- struct{}{} }()
				_, _ = io.Copy(c, up)
				c.Close()
				up.Close()
				<-done
			}()
		}
	}()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = clean
	cmd.Dir = workdir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
