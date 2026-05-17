package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"codebox/internal/container"
)

// ExecRequest is the use-case input for App.Exec. Fields mirror the
// `codebox exec` flags and positional arguments. Command is the
// program to run inside the container; Args are its arguments, with
// embedded spaces and shell metacharacters preserved verbatim.
type ExecRequest struct {
	Instance     string
	Orchestrator string
	Remote       string
	InstanceKey  string
	Command      string
	Args         []string
}

// Exec runs a single command inside the named sandbox instance over
// ssh and exits with its status. stdin/stdout/stderr are wired through
// so the operator can pipe data in or out.
//
// When Remote is set the orchestrator lookups (existence + host port)
// run via ssh to that host using the operator's normal ssh
// configuration — InstanceKey is **not** passed to that outer ssh. The
// container-bound ssh always runs locally and adds `-J Remote` so
// `localhost` resolves on the orchestrator host. InstanceKey, when
// supplied, is passed as `-i` to that inner ssh only.
func (a *App) Exec(
	ctx context.Context,
	stdin io.Reader,
	stdout, stderr io.Writer,
	req ExecRequest,
) error {
	if err := validateInstanceName(req.Instance); err != nil {
		return err
	}
	if req.Command == "" {
		return errors.New("command is required")
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

	sshCmd := buildExecSSHCommand(req.Remote, hostPort,
		expandHome(req.InstanceKey, a.home), req.Command, req.Args)

	return a.runners("").Run(ctx, sshCmd, stdin, stdout, stderr)
}

// buildExecSSHCommand assembles the ssh command that runs `command
// args...` inside the container and exits. The command and each arg
// are single-quoted so the in-container login shell preserves their
// boundaries — ssh joins its trailing arguments with spaces before
// sending them to that shell, so this inner layer is what keeps spaces
// in arguments intact. The whole remote command is then single-quoted
// again for the outer `sh -c` that the local runner executes.
func buildExecSSHCommand(remote, hostPort, instanceKey, command string, args []string) string {
	parts := []string{"ssh", "-o", "StrictHostKeyChecking=no"}
	if instanceKey != "" {
		parts = append(parts, "-i", shquote(instanceKey))
	}
	if remote != "" {
		parts = append(parts, "-J", shquote(remote))
	}
	parts = append(parts,
		fmt.Sprintf("%s@localhost", instanceUser),
		"-p", hostPort,
	)

	inner := make([]string, 0, 1+len(args))
	inner = append(inner, shquote(command))
	for _, a := range args {
		inner = append(inner, shquote(a))
	}
	parts = append(parts, shquote(strings.Join(inner, " ")))
	return strings.Join(parts, " ")
}
