package managed

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// A read-only bind mount of broker.sock pins its inode inside Bubblewrap. A
// restarted broker replaces that inode. Keep a private, credential-blind relay
// alive with the native worker, and mount only that stable socket into its
// sandbox. Each connection still authenticates end-to-end at the real broker.
func nativeSocketRelay(ctx context.Context, backend string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "amx-native-socket-")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return "", nil, err
	}
	relayCtx, cancel := context.WithCancel(ctx)
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	var workers sync.WaitGroup
	var once sync.Once
	closeRelay := func() {
		once.Do(func() {
			cancel()
			listener.Close()
			mu.Lock()
			for conn := range connections {
				conn.Close()
			}
			mu.Unlock()
		})
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-relayCtx.Done():
			closeRelay()
		case <-done:
		}
	}()
	slots := make(chan struct{}, 32)
	go func() {
		defer close(done)
		for {
			client, e := listener.Accept()
			if e != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				client.Close()
				continue
			}
			mu.Lock()
			connections[client] = true
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-slots; mu.Lock(); delete(connections, client); mu.Unlock(); client.Close() }()
				dialCtx, stop := context.WithTimeout(relayCtx, 3*time.Second)
				upstream, e := (&net.Dialer{}).DialContext(dialCtx, "unix", backend)
				stop()
				if e != nil {
					return
				}
				defer upstream.Close()
				mu.Lock()
				if relayCtx.Err() != nil {
					mu.Unlock()
					return
				}
				connections[upstream] = true
				mu.Unlock()
				defer func() { mu.Lock(); delete(connections, upstream); mu.Unlock() }()
				// Bound idle/partial requests and descriptor use; the CLI itself
				// uses shorter request deadlines and can reconnect after expiry.
				deadline := time.Now().Add(60 * time.Second)
				client.SetDeadline(deadline)
				upstream.SetDeadline(deadline)
				copied := make(chan struct{})
				go func() {
					_, _ = io.Copy(upstream, client)
					if unix, ok := upstream.(*net.UnixConn); ok {
						unix.CloseWrite()
					}
					close(copied)
				}()
				_, _ = io.Copy(client, upstream)
				client.Close()
				upstream.Close()
				<-copied
			}()
		}
	}()
	cleanup := func() { closeRelay(); <-done; workers.Wait(); os.RemoveAll(dir) }
	return path, cleanup, nil
}
