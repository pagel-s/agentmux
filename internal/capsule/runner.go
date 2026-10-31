// Package capsule confines writable workspaces and native history to sized
// tmpfs mounts and checkpoints only a bounded regular-file snapshot through a
// private Unix socket. It does not configure host filesystem project quotas.
package capsule

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const checkpointEnd = "AGENTMUX-CHECKPOINT-1\n"

type manifest struct {
	Inputs     []volume `json:"inputs"`
	Outputs    []volume `json:"outputs"`
	MaxEntries int      `json:"max_entries"`
	Socket     string   `json:"socket"`
	Workdir    string   `json:"workdir"`
}

// Run is the trusted inner shim. Its manifest is a read-only host-generated
// file; it preserves native stdio and returns native process errors. A failed
// checkpoint also fails the turn, preserving the host's previous snapshot.
func Run(ctx context.Context, manifestPath string, argv, env []string) error {
	if len(argv) == 0 {
		return errors.New("capsule runner requires native argv")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil || len(data) > 65536 {
		return errors.New("capsule manifest unavailable or too large")
	}
	var m manifest
	if err = json.Unmarshal(data, &m); err != nil {
		return err
	}
	if m.MaxEntries < 1 || m.MaxEntries > 100000 || len(m.Inputs) > 2 || len(m.Inputs) != len(m.Outputs) {
		return errors.New("invalid capsule manifest")
	}
	for _, v := range m.Inputs {
		if err = copyTree(v.Source, v.Target, v.Bytes, m.MaxEntries); err != nil {
			return err
		}
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = m.Workdir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", m.Socket)
	if err != nil {
		return errors.New("capsule checkpoint connection failed")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	if err = writeArchive(conn, m.Outputs, m.MaxEntries); err != nil {
		return err
	}
	if _, err = io.WriteString(conn, checkpointEnd); err != nil {
		return err
	}
	if u, ok := conn.(*net.UnixConn); ok {
		_ = u.CloseWrite()
	}
	ack := make([]byte, 3)
	if _, err = io.ReadFull(conn, ack); err != nil || string(ack) != "OK\n" {
		return errors.New("capsule checkpoint rejected")
	}
	return runErr
}

func copyTree(source, target string, maxBytes int64, maxEntries int) error {
	if err := os.MkdirAll(target, 0700); err != nil {
		return err
	}
	// Pipe the same validated archive protocol used by the host. Native output
	// cannot cause copy-in to follow links in either source or destination.
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		e := writeArchive(pw, []volume{{Name: "volume", Source: source, Bytes: maxBytes}}, maxEntries)
		_ = pw.CloseWithError(e)
		done <- e
	}()
	parent, err := os.MkdirTemp(target, ".capsule-import-")
	if err != nil {
		pr.Close()
		<-done
		return err
	}
	defer os.RemoveAll(parent)
	err = receiveArchive(pr, parent, []volume{{Name: "volume", Bytes: maxBytes}}, maxEntries)
	pr.Close()
	writeErr := <-done
	if err != nil {
		return err
	}
	if writeErr != nil {
		return writeErr
	}
	entries, err := os.ReadDir(filepath.Join(parent, "volume"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		destination := filepath.Join(target, entry.Name())
		if _, e := os.Lstat(destination); !os.IsNotExist(e) {
			return errors.New("capsule import would overwrite a mounted file")
		}
		if err = os.Rename(filepath.Join(parent, "volume", entry.Name()), destination); err != nil {
			return err
		}
	}
	return nil
}
