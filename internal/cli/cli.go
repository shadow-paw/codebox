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
// The banner is written to stdout before any command runs — except for
// `codebox exec`, whose output is intended to be piped into other tools
// so a decorative header would corrupt the stream.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if !isExecInvocation(args) {
		if _, err := fmt.Fprint(stdout, banner()); err != nil {
			_, _ = fmt.Fprintf(stderr, "codebox: %v\n", err)
			return 1
		}
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

// isExecInvocation reports whether the operator is running `codebox
// exec`. Detection is positional: the first non-flag token determines
// the subcommand, so global flags placed before it do not perturb the
// check.
func isExecInvocation(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if len(a) > 0 && a[0] == '-' {
			continue
		}
		return a == "exec"
	}
	return false
}
