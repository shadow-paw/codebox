package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"codebox/internal/adapters/runner"
	"codebox/internal/container"
	"codebox/internal/image"
)

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

	authKey, err := a.keys.Resolve(expandHome(req.InstanceKey, a.home))
	if err != nil {
		return err
	}
	var dockerfile bytes.Buffer
	if err := image.Generate(&dockerfile, image.Options{
		OS:            req.OS,
		AuthorizedKey: authKey,
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
	if err := rnr.Run(ctx, eng.Run(req.Instance), nil, &runOut, &runErr); err != nil {
		return wrapRunErr("start container", err, &runErr)
	}

	_, _ = fmt.Fprintf(w, "Instance %q is ready. Open a shell:\n  %s\n",
		req.Instance, shellHint(req))
	return nil
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
