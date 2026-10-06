package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "harden-llm-gateway: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	if len(args) == 0 || len(args) == 1 && args[0] == "serve" {
		return runGatewayServer(ctx, stdout, stderr, getenv)
	}
	if args[0] == "healthcheck" {
		return runHealthcheck(ctx, args[1:], stdout)
	}
	if len(args) == 1 && args[0] == "version" {
		_, err := fmt.Fprintln(stdout, version)
		return err
	}
	return fmt.Errorf("unknown command %q", args[0])
}
