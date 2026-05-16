// Command codebox is the entrypoint for the codebox CLI.
//
// It is intentionally a thin wrapper: it wires real-world IO (stdin, stdout,
// stderr, process arguments, signal-driven context) into the application
// module at codebox/internal/app and exits with the status code returned
// by app.Run.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"codebox/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(app.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
