package main

import (
	"context"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"agentmux/internal/capsule"
)

func runInternalCapsule(args []string) int {
	flags := flag.NewFlagSet("internal-capsule", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifest := flags.String("manifest", "", "")
	if flags.Parse(args) != nil || *manifest == "" || flags.NArg() == 0 {
		return exitValidation
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if capsule.Run(ctx, *manifest, flags.Args(), os.Environ()) != nil {
		return exitRuntime
	}
	return exitSuccess
}
