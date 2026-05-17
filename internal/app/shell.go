package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"codebox/internal/container"
)

// ShellRequest is the use-case input for App.Shell. Fields mirror the
// `codebox shell` flags. Ports holds `LOCAL:REMOTE` strings supplied
// via repeated `--port` flags; each one becomes an `-L` forward to the
// container's localhost.
type ShellRequest struct {
	Instance     string
	Orchestrator string
	Remote       string
	InstanceKey  string
	Ports        []string
}

// Shell opens an interactive shell on the named sandbox instance. It
// confirms the container exists, looks up the host-side port mapped to
// the in-container sshd, then exec's ssh on the operator's machine
// with stdin/stdout/stderr wired straight through so the user gets a
// real tty.
//
// When Remote is set, the orchestrator lookups run via ssh to the
// orchestrator host; the operator's normal ssh configuration is used
// (InstanceKey is **not** passed to that outer ssh). The
// container-bound ssh always runs locally and adds `-J Remote` so
// `localhost` resolves on the orchestrator host. InstanceKey, when
// supplied, is passed as `-i` to that inner ssh.
func (a *App) Shell(
	ctx context.Context,
	stdin io.Reader,
	stdout, stderr io.Writer,
	req ShellRequest,
) error {
	if err := validateInstanceName(req.Instance); err != nil {
		return err
	}
	eng, err := container.New(req.Orchestrator)
	if err != nil {
		return err
	}
	rnr := a.runners(req.Remote)

	if err := requireExists(ctx, rnr, eng, req.Instance); err != nil {
		return err
	}

	var portOut, portErr bytes.Buffer
	if err := rnr.Run(ctx, eng.HostPort(req.Instance), nil, &portOut, &portErr); err != nil {
		return wrapRunErr("look up host port", err, &portErr)
	}
	hostPort := parsePortLines(portOut.String())
	if hostPort == "" {
		return fmt.Errorf("instance %q is not exposing port %s; is it running?",
			req.Instance, instancePort)
	}

	sshCmd := buildShellSSHCommand(req.Remote, hostPort,
		expandHome(req.InstanceKey, a.home), req.Ports)

	return a.runners("").Run(ctx, sshCmd, stdin, stdout, stderr)
}

// portLineRe matches `<addr>:<port>` lines emitted by `<engine> port
// <name> 2222`. The address is tolerated in IPv4 (`0.0.0.0`), IPv6
// (`[::]`), or interface-name form; only the trailing numeric port is
// captured.
var portLineRe = regexp.MustCompile(`:(\d+)\s*$`)

// parsePortLines returns the first numeric port found in the output of
// `<engine> port <name> 2222`, or the empty string when no usable
// mapping is present.
func parsePortLines(s string) string {
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if m := portLineRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			return m[1]
		}
	}
	return ""
}

// buildShellSSHCommand assembles the ssh command that opens the
// interactive shell. The command is later passed to a local
// `sh -c`-style runner, so every user-supplied argument is
// single-quoted; numeric values (host port, port-forward components)
// are emitted verbatim. Forwards target `localhost:R` so the remote
// end is interpreted on the container side of an `-J` jump.
func buildShellSSHCommand(remote, hostPort, instanceKey string, ports []string) string {
	parts := []string{"ssh", "-o", "StrictHostKeyChecking=no"}
	if instanceKey != "" {
		parts = append(parts, "-i", shquote(instanceKey))
	}
	for _, p := range ports {
		l, r, ok := strings.Cut(p, ":")
		if !ok || l == "" || r == "" {
			continue
		}
		parts = append(parts, "-L", shquote(fmt.Sprintf("%s:localhost:%s", l, r)))
	}
	if remote != "" {
		parts = append(parts, "-J", shquote(remote))
	}
	parts = append(parts,
		fmt.Sprintf("%s@localhost", instanceUser),
		"-p", hostPort,
	)
	return strings.Join(parts, " ")
}

// shquote single-quotes s for safe embedding in a `sh -c` command.
// Mirrors container.shquote; duplicated here so the app layer does
// not reach into a domain package for a shell helper.
func shquote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
