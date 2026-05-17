// Package cli implements the codebox command-line interface.
//
// Run is the single entrypoint. It is intentionally injectable: the caller
// supplies a context, the argument slice (typically os.Args[1:]), and the
// stdout/stderr writers, so the CLI can be exercised end-to-end from tests
// without spawning a subprocess.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// Run executes the codebox CLI with the supplied context, arguments, and
// IO streams. It returns a process exit code (0 on success, non-zero on
// failure or signal-driven cancellation).
//
// The banner is always written to stdout before any command runs, regardless
// of whether the invocation is a real command, --help, or --version.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if _, err := fmt.Fprint(stdout, banner()); err != nil {
		_, _ = fmt.Fprintf(stderr, "codebox: %v\n", err)
		return 1
	}

	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	if len(args) == 0 {
		if err := root.Help(); err != nil {
			_, _ = fmt.Fprintf(stderr, "codebox: %v\n", err)
			return 1
		}
		return 0
	}

	if err := root.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}
