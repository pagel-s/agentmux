package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"agentmux/internal/agybridge"
)

func runProvider(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "antigravity" || args[1] != "serve" {
		fmt.Fprintln(stderr, "Usage: agentmux provider antigravity serve --listen 127.0.0.1:8097 --models gemini-3.8-flash-low --key-env AGENTMUX_AGY_BRIDGE_KEY")
		return exitValidation
	}
	flags := flag.NewFlagSet("provider antigravity serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", "127.0.0.1:8097", "numeric loopback address")
	models := flags.String("models", "", "explicit comma-separated allowed native model IDs")
	keyEnv := flags.String("key-env", "AGENTMUX_AGY_BRIDGE_KEY", "environment variable containing a private bridge key of at least32bytes")
	binary := flags.String("binary", "agy", "owner-selected native executable")
	timeout := flags.Duration("timeout", 45*time.Second, "per-native-turn deadline (maximum2minutes)")
	concurrent := flags.Int("max-concurrent", 1, "maximum simultaneous native turns")
	requests := flags.Int("max-requests", 100, "finite native request allowance before restart")
	if err := flags.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitSuccess
		}
		return exitValidation
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "provider serve accepts flags only")
		return exitValidation
	}
	allowed := strings.Split(*models, ",")
	for i := range allowed {
		allowed[i] = strings.TrimSpace(allowed[i])
	}
	env := map[string]string{}
	for _, name := range []string{"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		if value := os.Getenv(name); value != "" {
			env[name] = value
		}
	}
	bridge, err := agybridge.New(agybridge.Options{Key: os.Getenv(*keyEnv), Models: allowed, Command: []string{*binary}, Env: env, Timeout: *timeout, MaxConcurrent: *concurrent, MaxRequests: *requests})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitValidation
	}
	listener, err := agybridge.Listen(*listen)
	if err != nil {
		fmt.Fprintln(stderr, "bridge listen failed; require an available numeric loopback address")
		return exitRuntime
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	server := &http.Server{Handler: bridge, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: *timeout + 10*time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Fprintf(stdout, "Antigravity inference bridge listening at http://%s/v1 (trusted-local inference only; native tools denied)\n", listener.Addr())
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(stderr, "inference service stopped unexpectedly")
		return exitRuntime
	}
	return exitSuccess
}
