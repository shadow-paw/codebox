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

// PushRequest is the use-case input for App.Push. Fields mirror the
// `codebox push` flags. LocalPath is the file or directory to upload;
// InstancePath is the directory inside the container to copy into.
type PushRequest struct {
	Instance     string
	Orchestrator string
	Remote       string
	InstanceKeys []string
	LocalPath    string
	InstancePath string
}

// PullRequest is the use-case input for App.Pull. Fields mirror the
// `codebox pull` flags. InstancePath is the file or directory inside
// the container to download; LocalPath is the local directory to copy
// into.
type PullRequest struct {
	Instance     string
	Orchestrator string
	Remote       string
	InstanceKeys []string
	InstancePath string
	LocalPath    string
}

// Push uploads a file or directory from the operator's machine into a
// sandbox instance using rsync over ssh. The rsync command is echoed
// back, bracketed by a separator, so the operator can audit the exact
// invocation that is about to run.
func (a *App) Push(ctx context.Context, stdout, stderr io.Writer, req PushRequest) error {
	if err := req.validate(); err != nil {
		return err
	}
	local := expandHome(req.LocalPath, a.home)
	dst := fmt.Sprintf("%s@localhost:%s", instanceUser, req.InstancePath)
	return a.transfer(ctx, stdout, stderr, transferRequest{
		instance:     req.Instance,
		orchestrator: req.Orchestrator,
		remote:       req.Remote,
		instanceKeys: req.InstanceKeys,
		src:          local,
		dst:          dst,
	})
}

// Pull downloads a file or directory from a sandbox instance to the
// operator's machine using rsync over ssh. The rsync command is echoed
// back, bracketed by a separator, so the operator can audit the exact
// invocation that is about to run.
func (a *App) Pull(ctx context.Context, stdout, stderr io.Writer, req PullRequest) error {
	if err := req.validate(); err != nil {
		return err
	}
	src := fmt.Sprintf("%s@localhost:%s", instanceUser, req.InstancePath)
	local := expandHome(req.LocalPath, a.home)
	return a.transfer(ctx, stdout, stderr, transferRequest{
		instance:     req.Instance,
		orchestrator: req.Orchestrator,
		remote:       req.Remote,
		instanceKeys: req.InstanceKeys,
		src:          src,
		dst:          local,
	})
}

func (r PushRequest) validate() error {
	if err := validateInstanceName(r.Instance); err != nil {
		return err
	}
	if r.LocalPath == "" {
		return errors.New("--local-path is required")
	}
	if r.InstancePath == "" {
		return errors.New("--instance-path is required")
	}
	return nil
}

func (r PullRequest) validate() error {
	if err := validateInstanceName(r.Instance); err != nil {
		return err
	}
	if r.InstancePath == "" {
		return errors.New("--instance-path is required")
	}
	if r.LocalPath == "" {
		return errors.New("--local-path is required")
	}
	return nil
}

// transferRequest is the merged input for the shared push/pull flow.
type transferRequest struct {
	instance     string
	orchestrator string
	remote       string
	instanceKeys []string
	src          string
	dst          string
}

// transfer is the shared body of Push and Pull. It validates the
// orchestrator, confirms the container exists, looks up the host port
// mapped to the in-container sshd, builds an rsync command that uses
// ssh as its transport (with `-J Remote` for ProxyJump and `-i KEY`
// for the inner hop only), prints the command bracketed by a
// separator, and runs it locally so the operator sees rsync's progress
// stream directly.
func (a *App) transfer(
	ctx context.Context,
	stdout, stderr io.Writer,
	req transferRequest,
) error {
	eng, err := container.New(req.orchestrator)
	if err != nil {
		return err
	}
	rnr := a.runners(req.remote)

	if err := requireExists(ctx, rnr, eng, req.instance); err != nil {
		return err
	}

	var portOut, portErr bytes.Buffer
	if err := rnr.Run(ctx, eng.HostPort(req.instance), nil, &portOut, &portErr); err != nil {
		return wrapRunErr("look up host port", err, &portErr)
	}
	hostPort := parsePortLines(portOut.String())
	if hostPort == "" {
		return fmt.Errorf("instance %q is not exposing port %s; is it running?",
			req.instance, instancePort)
	}

	rsyncCmd := buildRsyncCommand(req.remote, hostPort,
		expandHomeAll(req.instanceKeys, a.home), req.src, req.dst)
	writeRsyncBlock(stdout, rsyncCmd)

	return a.runners("").Run(ctx, rsyncCmd, nil, stdout, stderr)
}

// buildRsyncCommand assembles an `rsync ... -e ssh ... SRC DST` shell
// command. The ssh transport is embedded as a single shell-quoted
// argument to `-e`; tokens within it (the key path, the jump host)
// are themselves single-quoted so they survive both the outer `sh -c`
// unquoting and the inner tokenisation rsync performs on the `-e`
// value. instanceKeys, when supplied, are only passed to this inner
// ssh — the orchestrator-side runner that looked up the host port
// already used the operator's normal ssh configuration.
func buildRsyncCommand(remote, hostPort string, instanceKeys []string, src, dst string) string {
	sshParts := []string{"ssh", "-o", "StrictHostKeyChecking=no"}
	sshParts = appendIdentityArgs(sshParts, instanceKeys)
	if remote != "" {
		sshParts = append(sshParts, "-J", shquote(remote))
	}
	sshParts = append(sshParts, "-p", hostPort)
	sshCmd := strings.Join(sshParts, " ")

	parts := []string{
		"rsync",
		"--verbose",
		"--archive",
		"--compress",
		"--update",
		"--progress",
		"-e", shquote(sshCmd),
		shquote(src),
		shquote(dst),
	}
	return strings.Join(parts, " ")
}

// buildCredentialsRsyncCommand assembles the rsync command used by
// App.Create to push the operator's ~/.claude/credentials.json into
// the freshly-started container. It is identical to buildRsyncCommand
// except for two add-ons that matter when the destination is a
// known-sensitive single file deep in the user's home:
//
//   - --mkpath so /home/user/.claude is created on the receiving side
//     when it does not yet exist (rsync >= 3.2.3, which all base
//     images ship).
//   - --chmod=F0600 so the credentials file lands with the same mode
//     Claude expects on the host, regardless of the source's perms.
func buildCredentialsRsyncCommand(remote, hostPort string, instanceKeys []string, src, dst string) string {
	sshParts := []string{"ssh", "-o", "StrictHostKeyChecking=no"}
	sshParts = appendIdentityArgs(sshParts, instanceKeys)
	if remote != "" {
		sshParts = append(sshParts, "-J", shquote(remote))
	}
	sshParts = append(sshParts, "-p", hostPort)
	sshCmd := strings.Join(sshParts, " ")

	parts := []string{
		"rsync",
		"--verbose",
		"--archive",
		"--compress",
		"--update",
		"--progress",
		"--mkpath",
		"--chmod=F0600",
		"-e", shquote(sshCmd),
		shquote(src),
		shquote(dst),
	}
	return strings.Join(parts, " ")
}

const (
	rsyncTopBar    = "──────── rsync ───────────────────────────────────────────────"
	rsyncBottomBar = "──────────────────────────────────────────────────────────────"
)

// writeRsyncBlock prints the rsync command bracketed by horizontal
// rules so it stands apart from rsync's own output. Mirrors the
// Dockerfile block create prints during provisioning.
func writeRsyncBlock(w io.Writer, cmd string) {
	_, _ = fmt.Fprintln(w, rsyncTopBar)
	_, _ = fmt.Fprintln(w, cmd)
	_, _ = fmt.Fprintln(w, rsyncBottomBar)
	_, _ = fmt.Fprintln(w)
}
