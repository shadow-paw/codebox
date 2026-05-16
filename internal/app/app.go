// Package app implements the codebox command-line application.
//
// The package exposes a single entrypoint, Run, that takes injected IO
// streams and arguments. Keeping the application logic behind this
// signature lets the cmd/codebox wrapper stay trivial and lets tests
// exercise the CLI without spawning a subprocess.
package app

import (
	"context"
	"fmt"
	"io"
)

// Run executes the codebox CLI with the supplied context, arguments, and
// IO streams, and returns a process exit code (0 on success).
//
// The function is the seam between the thin main wrapper and the
// application's business logic. Callers should not assume anything about
// the process environment beyond what is passed in explicitly.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	_ = ctx
	_ = args

	if _, err := fmt.Fprintln(stdout, "hello world"); err != nil {
		_, _ = fmt.Fprintf(stderr, "codebox: %v\n", err)
		return 1
	}
	return 0
}
