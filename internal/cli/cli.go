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
// invocations whose stdout is consumed by another program: `codebox
// exec` (piped to downstream tools), `codebox completion` (shell
// script written to a file), and the hidden `__complete` /
// `__completeNoDesc` runtime calls cobra uses to feed shell tab
// completion.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if !suppressBanner(args) {
		if _, err := fmt.Fprint(stdout, banner()); err != nil {
			_, _ = fmt.Fprintf(stderr, "codebox: %v\n", err)
			return 1
		}
	}

	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	if err := root.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}

// suppressBanner reports whether the first positional command is one
// whose stdout must not be prefixed by the banner: `exec` (piped),
// `completion` (shell script), and the hidden `__complete` /
// `__completeNoDesc` runtime calls cobra fires for tab completion.
// Detection is positional — the first non-flag token determines the
// subcommand — so global flags before it do not perturb the check.
//
// A `--help` / `-h` anywhere before `--` cancels the suppression: help
// text is for humans and the banner must stay in every help path so
// `codebox`, `codebox help`, `codebox --help`, `codebox <cmd> --help`,
// and `codebox help <cmd>` all render the same opening lines.
func suppressBanner(args []string) bool {
	cmd := ""
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--help" || a == "-h" {
			return false
		}
		if cmd != "" {
			continue
		}
		if len(a) > 0 && a[0] == '-' {
			continue
		}
		cmd = a
	}
	switch cmd {
	case "exec", "completion", "__complete", "__completeNoDesc":
		return true
	}
	return false
}
