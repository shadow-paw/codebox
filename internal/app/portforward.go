package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"codebox/internal/container"
)

// PortForwardRequest is the use-case input for App.PortForward. Ports
// holds normalized "LOCAL:REMOTE" strings (both sides numeric, never
// empty) resolved by the CLI from the project's port-forward config or
// a compose file. Each becomes an `-L LOCAL:localhost:REMOTE`
// forward to the container's localhost.
type PortForwardRequest struct {
	Instance     string
	Orchestrator string
	Remote       string
	InstanceKey  string
	Ports        []string
}

// PortForward sets up TCP forwards from the operator's localhost to the
// named instance and holds them open until interrupted. The preflight
// (existence check + host port lookup) is identical to shell/mount; the
// forwarding ssh then runs locally with `-N` so no remote command — and
// thus no usable shell — is started. The mapped ports are printed before
// the connection blocks; Ctrl-C (context cancellation) is the expected
// way to stop and is reported as a clean exit, not an error.
//
// As with shell, when Remote is set the orchestrator lookups tunnel to
// the orchestrator host while the forwarding ssh always runs locally and
// adds `-J Remote` so `localhost` resolves on the orchestrator side.
func (a *App) PortForward(ctx context.Context, stdout, stderr io.Writer, req PortForwardRequest) error {
	if err := validateInstanceName(req.Instance); err != nil {
		return err
	}
	if len(req.Ports) == 0 {
		return fmt.Errorf("no ports to forward")
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

	sshCmd := buildPortForwardSSHCommand(req.Remote, hostPort,
		expandHome(req.InstanceKey, a.home), req.Ports)

	writePortForwardBanner(stdout, req.Instance, req.Ports)

	var errBuf bytes.Buffer
	err = a.runners("").Run(ctx, sshCmd, nil, io.Discard, &errBuf)
	if ctx.Err() != nil {
		// Ctrl-C / SIGTERM is how the operator stops forwarding; treat
		// it as a clean shutdown rather than surfacing the killed-ssh
		// error.
		_, _ = fmt.Fprintln(stdout, "\nStopped port forwarding.")
		return nil
	}
	if err != nil {
		return wrapRunErr("port forward", err, &errBuf)
	}
	return nil
}

// buildPortForwardSSHCommand assembles the `ssh -N` invocation that
// opens the requested `-L` forwards without running any remote command,
// so the operator gets working tunnels but no shell. As in
// buildShellSSHCommand, forwards target `localhost:REMOTE` so the far
// end resolves on the container side of an `-J` jump, and every
// user-influenced argument is single-quoted. ExitOnForwardFailure makes
// ssh fail loudly when a local port is already taken instead of
// connecting with a silently-dropped forward; ServerAliveInterval keeps
// the otherwise-idle session from being dropped by a NAT/firewall.
//
// ports are pre-normalized "LOCAL:REMOTE" pairs (see PortForwardRequest);
// any malformed entry is skipped defensively.
func buildPortForwardSSHCommand(remote, hostPort, instanceKey string, ports []string) string {
	parts := []string{"ssh", "-N",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=30",
	}
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
		"-p", hostPort,
		fmt.Sprintf("%s@localhost", instanceUser),
	)
	return strings.Join(parts, " ")
}

// writePortForwardBanner prints the active forwards and the stop hint
// before the connection blocks, since `ssh -N` itself emits nothing on
// success.
func writePortForwardBanner(w io.Writer, instance string, ports []string) {
	_, _ = fmt.Fprintf(w, "Forwarding ports to instance %s:\n", instance)
	for _, p := range ports {
		l, r, ok := strings.Cut(p, ":")
		if !ok {
			continue
		}
		_, _ = fmt.Fprintf(w, "  localhost:%s -> %s\n", l, r)
	}
	_, _ = fmt.Fprintln(w, "Press Ctrl-C to stop.")
}
