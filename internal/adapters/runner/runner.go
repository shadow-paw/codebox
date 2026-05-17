// Package runner executes shell commands locally or via ssh.
//
// It is an adapter: business logic should depend on the CommandRunner
// interface declared in internal/app and let cmd/codebox wire in
// runner.Local or runner.SSH. Errors from ssh that indicate a
// connection-level failure (exit status 255) are surfaced as
// *ConnectError so callers can give the operator a targeted message.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// ConnectError signals that ssh could not reach Host or authenticate.
// The wrapped Err carries the underlying *exec.ExitError; the typical
// triage step is to inspect the operator's stderr (which the runner
// writes through live for ssh too).
type ConnectError struct {
	Host string
	Err  error
}

// Error formats the connect failure with the host name only — the
// underlying exit-status detail is rarely useful to a CLI user.
func (e *ConnectError) Error() string {
	return fmt.Sprintf("ssh: could not connect to %s", e.Host)
}

// Unwrap exposes the *exec.ExitError to errors.Is/errors.As callers.
func (e *ConnectError) Unwrap() error { return e.Err }

// Runner executes a single shell command per call. Reuse is safe but
// each Run spawns a fresh process.
type Runner struct {
	host string // empty for local
}

// Local returns a Runner whose Run executes `sh -c <cmd>` on this
// machine.
func Local() *Runner { return &Runner{} }

// SSH returns a Runner whose Run executes `ssh <host> <cmd>`. The
// operator's normal ssh configuration (~/.ssh/config, agent, default
// keys) is used; the --instance-key flag is intentionally not passed
// to ssh — it is only embedded inside the image.
func SSH(host string) *Runner { return &Runner{host: host} }

// Run executes shellCmd, wiring stdin/stdout/stderr through to the
// supplied streams. When the runner targets ssh, exit status 255 is
// translated into *ConnectError.
func (r *Runner) Run(
	ctx context.Context,
	shellCmd string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	name, args := r.execArgs(shellCmd)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if r.host != "" {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 255 {
			return &ConnectError{Host: r.host, Err: err}
		}
	}
	return err
}

// execArgs returns the exec name and argv used by Run. It is exported
// to package tests through ExecArgs (see export_test.go).
func (r *Runner) execArgs(shellCmd string) (string, []string) {
	if r.host == "" {
		return "sh", []string{"-c", shellCmd}
	}
	return "ssh", []string{r.host, shellCmd}
}
