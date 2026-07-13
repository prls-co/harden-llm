package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	hardenllm "github.com/prls-co/harden-llm"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Getenv); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "harden-llm-gateway: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		_, err := hardenllm.New(hardenllm.Options{})
		return err
	}
	if args[0] == "bootstrap-user" {
		return runBootstrapUser(ctx, args[1:], stdin, stdout, getenv)
	}
	return fmt.Errorf("unknown command %q", args[0])
}
