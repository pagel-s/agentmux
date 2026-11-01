package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"agentmux/internal/managed"
	"agentmux/internal/nativebridge"
	claudebridge "agentmux/internal/nativebridge/claude"
	codexbridge "agentmux/internal/nativebridge/codex"
	pibridge "agentmux/internal/nativebridge/pi"
)

func nativeBridgeConnection() (*nativebridge.Client, error) {
	path := os.Getenv("AGENTMUX_CREDENTIAL_FILE")
	if path == "" {
		return nil, errors.New("native integration requires its scoped agent credential")
	}
	credential, err := managed.LoadCredential(path)
	if err != nil {
		return nil, err
	}
	if socket := os.Getenv("AGENTMUX_SOCKET"); socket != "" {
		credential.Socket = socket
	}
	client := managed.NewClient(credential)
	return &nativebridge.Client{Call: func(ctx context.Context, op string, args any) (json.RawMessage, error) {
		var key [24]byte
		if _, err := rand.Read(key[:]); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		result, problem := client.Call(ctx, managed.Request{Operation: op, RequestID: "native_" + hex.EncodeToString(key[:]), Args: raw})
		if problem != nil {
			return nil, problem
		}
		return result, nil
	}}, nil
}

func runNativeBridge(args []string, stdin io.Reader, stderr io.Writer) int {
	// Ctrl-C belongs to the native TUI, including while it is running a peer
	// turn. Cancelling this supervisor on SIGINT would kill normal interaction.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt)
	defer signal.Stop(sigint)
	conn, err := nativeBridgeConnection()
	if err == nil {
		switch {
		case len(args) == 2 && args[0] == "native-hook" && args[1] == "claude":
			err = claudebridge.RunHook(ctx, stdin, conn)
		case len(args) >= 5 && args[0] == "native-run" && args[1] == "--harness" && args[3] == "--":
			switch args[2] {
			case "claude":
				err = claudebridge.Run(ctx, args[4:], conn)
			case "codex":
				err = codexbridge.Run(ctx, args[4:], conn)
			case "pi":
				err = pibridge.Run(ctx, args[4:])
			default:
				err = errors.New("unsupported native delivery harness")
			}
		default:
			err = errors.New("usage: managed native-run --harness <claude|codex|pi> -- <executable> [args...] or managed native-hook claude")
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "Agentmux native bridge:", err)
		return exitRuntime
	}
	return exitSuccess
}
