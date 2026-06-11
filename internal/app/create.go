package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codebox/internal/adapters/runner"
	"codebox/internal/container"
	"codebox/internal/image"
)

// claudeCredentialsRetryDelay is the wait between the first failed
// rsync of the Claude credentials and the single retry. The container
// is freshly started, so sshd inside it may not be accepting
// connections for a beat; one short wait + one retry covers the gap
// without forcing the operator to re-run `codebox create` by hand.
//
// Mutable for tests via export_test.go; production code never writes
// to it.
var claudeCredentialsRetryDelay = 2 * time.Second

// startCheckBackoff is the sequence of waits between attempts to
// confirm the freshly-started container is in the engine's running
// list. The first check runs immediately; if the instance is missing
// we wait startCheckBackoff[0], retry, then startCheckBackoff[1],
// retry, and so on. The number of entries sets the retry count (3
// here: 1s, 3s, 5s), so the total budget before giving up is 9s
// plus the cost of the four `ps` invocations.
//
// Mutable for tests via export_test.go; production code never writes
// to it.
var startCheckBackoff = []time.Duration{
	1 * time.Second,
	3 * time.Second,
	5 * time.Second,
}

// CreateRequest is the use-case input for App.Create. Fields mirror the
// `codebox create` flags. InstanceKey is the raw value the operator
// supplied (possibly with a leading "~/"); App.Create handles ~
// expansion before passing it to the key resolver, and echoes the raw
// value back in the success hint.
type CreateRequest struct {
	Instance     string
	Orchestrator string
	OS           string
	InstanceKey  string
	Remote       string
	Rebuild      bool

	// HTTPSProxy, when non-empty, becomes an ENV HTTPS_PROXY directive
	// in the generated Dockerfile so package managers, curl, and the
	// installed toolchains see it during the build.
	HTTPSProxy string

	// Optional language toolchains; an empty string disables the
	// corresponding install. Versions are passed through verbatim to
	// the installer inside the image.
	Python string
	Node   string
	Golang string
	Dotnet string

	// Optional agents.
	Claude bool
	// ClaudeCredentials, when true, rsyncs the operator's
	// ~/.claude/credentials.json into the instance after the container
	// starts. Ignored unless Claude is also set. The file is never baked
	// into the image.
	ClaudeCredentials bool
	// Codex installs the OpenAI Codex CLI. When set, App.Create also
	// pushes the operator's ~/.codex/config.toml into the instance after
	// it starts, if that file exists.
	Codex bool
	// CodexCredentials, when true, rsyncs the operator's ~/.codex/auth.json
	// into the instance after the container starts, if that file exists.
	// Ignored unless Codex is also set. The file is never baked into the
	// image.
	CodexCredentials bool
	// Opencode installs the opencode CLI. When set, App.Create also
	// pushes the operator's ~/.config/opencode/opencode.json into the
	// instance after it starts, if that file exists.
	Opencode bool
	// OpencodeCredentials, when true, rsyncs the operator's
	// ~/.local/share/opencode/auth.json into the instance after the
	// container starts, if that file exists. Ignored unless Opencode is
	// also set. The file is never baked into the image.
	OpencodeCredentials bool

	// Optional tools.
	Psql bool
	// Tmux installs tmux in the image and labels the container
	// `tmux=true` so `codebox shell` launches tmux on connect.
	Tmux bool
	// Podman installs rootless Podman inside the instance and starts the
	// container with --privileged so nested containers can run.
	Podman bool
}

// Create provisions a sandbox instance: it confirms the instance does
// not already exist, builds the image from a generated Dockerfile (no
// files from the operator's working tree leak into the build context),
// runs the container, and prints a one-line shell hint on success.
// The Dockerfile content itself is never written to w.
func (a *App) Create(ctx context.Context, w io.Writer, req CreateRequest) error {
	if err := validateInstanceName(req.Instance); err != nil {
		return err
	}
	eng, err := container.New(req.Orchestrator)
	if err != nil {
		return err
	}

	// Fail fast if --claude-credentials was requested but the source
	// file is unreadable: we'd rather error before building a multi-GB
	// image than after, and the check is local and cheap. The flag is
	// ignored unless --claude is also set, so the stat is gated on both.
	if req.Claude && req.ClaudeCredentials {
		if _, err := os.Stat(claudeCredentialsPath(a.home)); err != nil {
			return fmt.Errorf("--claude-credentials: %w", err)
		}
	}

	authKey, err := a.keys.Resolve(expandHome(req.InstanceKey, a.home))
	if err != nil {
		return err
	}
	var dockerfile bytes.Buffer
	if err := image.Generate(&dockerfile, image.Options{
		OS:            req.OS,
		AuthorizedKey: authKey,
		HTTPSProxy:    req.HTTPSProxy,
		Python:        req.Python,
		Node:          req.Node,
		Golang:        req.Golang,
		Dotnet:        req.Dotnet,
		Claude:        req.Claude,
		Codex:         req.Codex,
		Opencode:      req.Opencode,
		Psql:          req.Psql,
		Tmux:          req.Tmux,
		Podman:        req.Podman,
	}); err != nil {
		return err
	}

	rnr := a.runners(req.Remote)
	if err := precheckNotExists(ctx, rnr, eng, req); err != nil {
		return err
	}

	writeDockerfileBlock(w, dockerfile.String())
	_, _ = fmt.Fprintf(w, "Building image %q...\n", req.Instance)
	if err := rnr.Run(ctx, eng.Build(req.Instance, req.Rebuild),
		bytes.NewReader(dockerfile.Bytes()), w, w); err != nil {
		return wrapRunErr("image build", err, nil)
	}

	_, _ = fmt.Fprintf(w, "Starting container %q...\n", req.Instance)
	var runOut, runErr bytes.Buffer
	runCmd := eng.Run(req.Instance, req.Podman, metadataLabels(req))
	if err := rnr.Run(ctx, runCmd, nil, &runOut, &runErr); err != nil {
		return wrapRunErr("start container", err, &runErr)
	}

	if err := ensureStarted(ctx, w, rnr, eng, req.Instance); err != nil {
		return err
	}

	if req.Podman {
		if err := migratePodman(ctx, w, rnr, eng, req.Instance); err != nil {
			return err
		}
	}

	// The *Credentials flags are ignored unless their agent is also
	// installed, so each push is gated on both.
	if req.Claude && req.ClaudeCredentials {
		if err := a.pushClaudeCredentials(ctx, w, rnr, eng, req); err != nil {
			return err
		}
	}

	if req.Codex {
		if err := a.pushCodexConfig(ctx, w, rnr, eng, req); err != nil {
			return err
		}
	}
	if req.Codex && req.CodexCredentials {
		if err := a.pushCodexCredentials(ctx, w, rnr, eng, req); err != nil {
			return err
		}
	}

	if req.Opencode {
		if err := a.pushOpencodeConfig(ctx, w, rnr, eng, req); err != nil {
			return err
		}
	}
	if req.Opencode && req.OpencodeCredentials {
		if err := a.pushOpencodeCredentials(ctx, w, rnr, eng, req); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintf(w, "Instance %q is ready. Open a shell:\n  %s\n",
		req.Instance, shellHint(req))
	return nil
}

// ensureStarted polls the engine until the freshly-started container
// shows up in the running list. `<engine> run -d` returns as soon as
// the engine has accepted the request, but the container may still
// be transitioning to "running" — or may have exited immediately
// (e.g. a crash inside the entrypoint). We probe right away and, if
// the container is not yet in the running set, fall back on the
// startCheckBackoff schedule for up to len(startCheckBackoff)
// retries. Each retry announces the wait on w so the operator sees
// why the create is pausing. A non-nil return means the container
// never appeared; the caller surfaces it instead of claiming success.
func ensureStarted(
	ctx context.Context,
	w io.Writer,
	rnr CommandRunner,
	eng *container.Engine,
	instance string,
) error {
	attempts := len(startCheckBackoff) + 1
	var lastErr error
	for i := 0; i < attempts; i++ {
		running, err := isRunning(ctx, rnr, eng, instance)
		switch {
		case err != nil:
			lastErr = err
		case running:
			return nil
		default:
			lastErr = fmt.Errorf("container %q is not in the running list", instance)
		}
		if i == attempts-1 {
			break
		}
		wait := startCheckBackoff[i]
		_, _ = fmt.Fprintf(w,
			"Container %q is not running yet; waiting %s before retry (%d/%d)...\n",
			instance, wait, i+1, len(startCheckBackoff))
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("instance %q did not start after %d attempts: %w",
		instance, attempts, lastErr)
}

// migratePodman runs `podman system migrate` as user "user" inside the
// freshly-started container. The migrate step rebuilds the rootless
// user-namespace mappings from the /etc/subuid and /etc/subgid ranges
// the image baked in; without it the first `podman` invocation inside
// the sandbox fails with a UID/GID range mismatch. It runs once, at
// create time, via the engine's own `exec` so it does not depend on
// the in-container sshd being up yet.
func migratePodman(
	ctx context.Context,
	w io.Writer,
	rnr CommandRunner,
	eng *container.Engine,
	instance string,
) error {
	_, _ = fmt.Fprintf(w, "Migrating Podman storage in %q...\n", instance)
	var out, errBuf bytes.Buffer
	cmd := eng.Exec(instance, "podman", "system", "migrate")
	if err := rnr.Run(ctx, cmd, nil, &out, &errBuf); err != nil {
		return wrapRunErr("podman system migrate", err, &errBuf)
	}
	return nil
}

// pushClaudeCredentials transfers the operator's
// ~/.claude/credentials.json into the freshly-started container so the
// Claude Code CLI inside the sandbox can pick up the operator's
// existing session. rsync runs with --mkpath so /home/user/.claude is
// created on demand, and --chmod=F0600 pins file permissions to the
// same mode Claude expects on the host. Source file existence is
// already enforced at the top of Create; we re-resolve the path here
// rather than thread it through the call.
func (a *App) pushClaudeCredentials(
	ctx context.Context,
	w io.Writer,
	rnr CommandRunner,
	eng *container.Engine,
	req CreateRequest,
) error {
	dst := fmt.Sprintf("/home/%s/.claude/.credentials.json", instanceUser)
	return a.pushFile(ctx, w, rnr, eng, req,
		"Pushing Claude credentials", claudeCredentialsPath(a.home), dst)
}

// pushCodexConfig transfers the operator's ~/.codex/config.toml into the
// freshly-started container so the Codex CLI inside the sandbox picks up
// the operator's existing settings. Like the opencode config this is
// best-effort: the file is optional, so a missing source is silently
// skipped rather than failing the create. The file is never baked into
// the image.
func (a *App) pushCodexConfig(
	ctx context.Context,
	w io.Writer,
	rnr CommandRunner,
	eng *container.Engine,
	req CreateRequest,
) error {
	src := codexConfigPath(a.home)
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	dst := fmt.Sprintf("/home/%s/.codex/config.toml", instanceUser)
	return a.pushFile(ctx, w, rnr, eng, req,
		"Pushing Codex config", src, dst)
}

// pushCodexCredentials transfers the operator's ~/.codex/auth.json into
// the freshly-started container so the Codex CLI inside the sandbox picks
// up the operator's existing session. Like the Codex config this is
// best-effort: a missing source file is silently skipped. Create only
// calls this when Codex is also set. The file is never baked into the image.
func (a *App) pushCodexCredentials(
	ctx context.Context,
	w io.Writer,
	rnr CommandRunner,
	eng *container.Engine,
	req CreateRequest,
) error {
	src := codexAuthPath(a.home)
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	dst := fmt.Sprintf("/home/%s/.codex/auth.json", instanceUser)
	return a.pushFile(ctx, w, rnr, eng, req,
		"Pushing Codex credentials", src, dst)
}

// pushOpencodeConfig transfers the operator's
// ~/.config/opencode/opencode.json into the freshly-started container so
// the opencode CLI inside the sandbox picks up the operator's existing
// settings. Unlike Claude credentials this is best-effort: the config is
// optional, so a missing source file is silently skipped rather than
// failing the create. The file is never baked into the image.
func (a *App) pushOpencodeConfig(
	ctx context.Context,
	w io.Writer,
	rnr CommandRunner,
	eng *container.Engine,
	req CreateRequest,
) error {
	src := opencodeConfigPath(a.home)
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	dst := fmt.Sprintf("/home/%s/.config/opencode/opencode.json", instanceUser)
	return a.pushFile(ctx, w, rnr, eng, req,
		"Pushing opencode config", src, dst)
}

// pushOpencodeCredentials transfers the operator's
// ~/.local/share/opencode/auth.json into the freshly-started container so
// the opencode CLI inside the sandbox picks up the operator's existing
// session. Like the opencode config this is best-effort: a missing source
// file is silently skipped. Create only calls this when Opencode is also
// set. The file is never baked into the image.
func (a *App) pushOpencodeCredentials(
	ctx context.Context,
	w io.Writer,
	rnr CommandRunner,
	eng *container.Engine,
	req CreateRequest,
) error {
	src := opencodeAuthPath(a.home)
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	dst := fmt.Sprintf("/home/%s/.local/share/opencode/auth.json", instanceUser)
	return a.pushFile(ctx, w, rnr, eng, req,
		"Pushing opencode credentials", src, dst)
}

// pushFile rsyncs a single local file (src) into the freshly-started
// container at the absolute in-container path dst. It looks up the
// host-side sshd port, builds the rsync with --mkpath + --chmod=F0600 so
// dst's parent directory is created on demand and the file lands 0600,
// echoes a labelled block, and runs the transfer locally so progress
// streams to the operator. If the first attempt fails — most often
// because the in-container sshd is still coming up — it waits
// claudeCredentialsRetryDelay and retries exactly once. label is the
// human-facing status line (e.g. "Pushing Claude credentials").
func (a *App) pushFile(
	ctx context.Context,
	w io.Writer,
	rnr CommandRunner,
	eng *container.Engine,
	req CreateRequest,
	label, src, dst string,
) error {
	var portOut, portErr bytes.Buffer
	if err := rnr.Run(ctx, eng.HostPort(req.Instance), nil, &portOut, &portErr); err != nil {
		return wrapRunErr("look up host port", err, &portErr)
	}
	hostPort := parsePortLines(portOut.String())
	if hostPort == "" {
		return fmt.Errorf("instance %q is not exposing port %s; is it running?",
			req.Instance, instancePort)
	}

	fullDst := fmt.Sprintf("%s@localhost:%s", instanceUser, dst)
	rsyncCmd := buildCredentialsRsyncCommand(req.Remote, hostPort,
		expandHome(req.InstanceKey, a.home), src, fullDst)
	_, _ = fmt.Fprintf(w, "%s...\n", label)
	writeRsyncBlock(w, rsyncCmd)

	local := a.runners("")
	if err := local.Run(ctx, rsyncCmd, nil, w, w); err != nil {
		_, _ = fmt.Fprintf(w,
			"%s failed (%v); the in-container sshd may not be ready yet — retrying once...\n",
			label, err)
		select {
		case <-time.After(claudeCredentialsRetryDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
		return local.Run(ctx, rsyncCmd, nil, w, w)
	}
	return nil
}

// opencodeConfigPath returns the operator-side path to the opencode
// config file that --opencode pushes into the instance when present.
func opencodeConfigPath(home string) string {
	return filepath.Join(home, ".config", "opencode", "opencode.json")
}

// opencodeAuthPath returns the operator-side path to the opencode
// credentials file that --opencode-credentials pushes into the instance
// when present.
func opencodeAuthPath(home string) string {
	return filepath.Join(home, ".local", "share", "opencode", "auth.json")
}

// codexConfigPath returns the operator-side path to the Codex config
// file that --codex pushes into the instance when present.
func codexConfigPath(home string) string {
	return filepath.Join(home, ".codex", "config.toml")
}

// codexAuthPath returns the operator-side path to the Codex credentials
// file that --codex-credentials pushes into the instance when present.
func codexAuthPath(home string) string {
	return filepath.Join(home, ".codex", "auth.json")
}

// claudeCredentialsPath returns the operator-side path to the Claude
// CLI credentials file that --claude-credentials pushes into the
// instance. Centralised so the existence pre-check and the rsync
// source cannot drift apart. The filename is `.credentials.json`
// (dotfile) so it sits beside any other dotted state Claude writes
// under ~/.claude.
func claudeCredentialsPath(home string) string {
	return filepath.Join(home, ".claude", ".credentials.json")
}

// precheckNotExists fails if a container with the requested name is
// already present on the target host. The hint suggests `codebox
// delete` so the operator can resolve the collision without leaving
// the CLI.
func precheckNotExists(ctx context.Context, rnr CommandRunner, eng *container.Engine, req CreateRequest) error {
	var out, errBuf bytes.Buffer
	if err := rnr.Run(ctx, eng.ListAllNames(), nil, &out, &errBuf); err != nil {
		return wrapRunErr("list containers", err, &errBuf)
	}
	if nameInList(out.String(), req.Instance) {
		return fmt.Errorf("instance %q already exists; stop and delete it first:\n  %s",
			req.Instance, deleteHint(req))
	}
	return nil
}

// nameInList reports whether name appears as its own line in the
// newline-separated output produced by `<engine> ps ... --format
// '{{.Names}}'`. Surrounding whitespace is ignored.
func nameInList(out, name string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// wrapRunErr maps a runner error into a stable user-facing message.
// SSH connect failures are reported with the host name only; other
// errors are wrapped with the operation name (and stderr when
// captured).
func wrapRunErr(op string, err error, stderr *bytes.Buffer) error {
	var ce *runner.ConnectError
	if errors.As(err, &ce) {
		return ce
	}
	if stderr != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s: %s", op, msg)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// deleteHint formats the suggested `codebox delete` command that the
// operator can copy-paste after an "already exists" failure.
func deleteHint(req CreateRequest) string {
	parts := []string{"codebox", "delete", req.Instance}
	if req.Orchestrator != "" && req.Orchestrator != "podman" {
		parts = append(parts, "--orchestrator="+req.Orchestrator)
	}
	if req.Remote != "" {
		parts = append(parts, "--remote="+req.Remote)
	}
	return strings.Join(parts, " ")
}

// metadataLabels builds the `key=value` metadata labels codebox stamps
// on the container to record what was installed: `tmux=true` and a
// boolean label per AI agent (e.g. `claude=true`). `codebox shell` reads
// them back to decide whether to launch tmux and which agent to run in
// its right-hand pane; the label keys for agents match the command name
// so the shell can run them directly.
//
// The agent labels are emitted in the shellAgents precedence order
// (claude, codex, opencode) so the label block reads consistently; the
// order does not otherwise matter since `codebox shell` only acts when a
// single agent is installed.
func metadataLabels(req CreateRequest) []string {
	var labels []string
	if req.Tmux {
		labels = append(labels, "tmux=true")
	}
	if req.Claude {
		labels = append(labels, "claude=true")
	}
	if req.Codex {
		labels = append(labels, "codex=true")
	}
	if req.Opencode {
		labels = append(labels, "opencode=true")
	}
	return labels
}

// shellHint formats the `codebox shell` command suggested after a
// successful create. --orchestrator/--remote/--instance-key are only
// included when the operator passed a non-default value to create, so
// the hint matches what they would have typed.
func shellHint(req CreateRequest) string {
	parts := []string{"codebox", "shell", req.Instance}
	if req.Orchestrator != "" && req.Orchestrator != "podman" {
		parts = append(parts, "--orchestrator="+req.Orchestrator)
	}
	if req.Remote != "" {
		parts = append(parts, "--remote="+req.Remote)
	}
	if req.InstanceKey != "" {
		parts = append(parts, "--instance-key="+req.InstanceKey)
	}
	return strings.Join(parts, " ")
}

// Horizontal rules that bracket the Dockerfile when it is echoed back
// to the operator. Both bars are the same width so the block reads as
// a self-contained section in the surrounding build output.
const (
	dockerfileTopBar    = "──────── Dockerfile ──────────────────────────────────────────"
	dockerfileBottomBar = "──────────────────────────────────────────────────────────────"
)

// writeDockerfileBlock prints the generated Dockerfile bracketed by
// horizontal rules so it stands apart from the engine's build output.
// content already ends with a newline (image.Generate guarantees it),
// so the closing bar follows directly after it.
func writeDockerfileBlock(w io.Writer, content string) {
	_, _ = fmt.Fprintln(w, dockerfileTopBar)
	_, _ = fmt.Fprint(w, content)
	_, _ = fmt.Fprintln(w, dockerfileBottomBar)
	_, _ = fmt.Fprintln(w)
}

// maxInstanceNameLen is the upper bound on an instance name. Codebox
// uses the same string as the container name and the image tag, so
// the cap is tight enough to stay well inside engine-specific limits
// while leaving room for the human-readable suffixes users typically
// want (project-feature-shortsha, etc.).
const maxInstanceNameLen = 32

// validateInstanceName enforces `^[A-Za-z0-9_-]{1,16}$`. Each failure
// mode returns a distinct message so the operator can fix it in one
// shot.
func validateInstanceName(name string) error {
	if name == "" {
		return errors.New("instance name is required")
	}
	if len(name) > maxInstanceNameLen {
		return fmt.Errorf("instance name %q is too long (max %d characters)", name, maxInstanceNameLen)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			return fmt.Errorf(
				"instance name %q contains invalid character %q (allowed: A-Z a-z 0-9 _ -)",
				name, r,
			)
		}
	}
	return nil
}

// expandHome replaces a leading "~/" (or bare "~") with home so paths
// can be supplied the way the operator would type them in a shell.
func expandHome(p, home string) string {
	if p == "" || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
