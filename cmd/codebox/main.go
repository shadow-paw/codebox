// Command codebox is the entrypoint for the codebox CLI.
//
// It is intentionally a thin wrapper: it wires real-world IO (process
// arguments, stdout/stderr, a signal-driven context) into the CLI module
// at codebox/internal/cli and exits with the status code returned by Run.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"codebox/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
