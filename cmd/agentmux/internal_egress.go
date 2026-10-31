package main

import (
	"context"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"agentmux/internal/egress"
)

func runInternalEgress(args []string) int {
	flags := flag.NewFlagSet("internal-egress", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socket := flags.String("socket", "", "")
	if flags.Parse(args) != nil || *socket == "" || flags.NArg() == 0 {
		return exitValidation
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	wd, err := os.Getwd()
	if err != nil {
		return exitRuntime
	}
	if err := egress.RunRelay(ctx, *socket, flags.Args(), os.Environ(), wd); err != nil {
		return exitRuntime
	}
	return exitSuccess
}
