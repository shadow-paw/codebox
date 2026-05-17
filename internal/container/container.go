// Package container translates codebox operations (list/build/run/...)
// into shell command strings for a specific container orchestrator
// (podman or docker). The two engines share argv syntax for the verbs
// codebox uses, so a single struct with a bin field is enough today;
// a divergence would split into per-engine implementations.
package container

import (
	"fmt"
	"strings"
)

// Engine emits shell commands for a container orchestrator.
type Engine struct {
	bin string
}

// New returns an Engine for "podman" or "docker". Other names are
// rejected with a message that lists the supported set.
func New(orchestrator string) (*Engine, error) {
	switch orchestrator {
	case "podman", "docker":
		return &Engine{bin: orchestrator}, nil
	default:
		return nil, fmt.Errorf("unsupported orchestrator %q (known: podman, docker)", orchestrator)
	}
}

// Name returns the engine bin name ("podman" or "docker").
func (e *Engine) Name() string { return e.bin }

// ListAllNames is the shell command that prints every container name
// managed by the engine, one per line on stdout. Intended for the
// pre-create existence check.
func (e *Engine) ListAllNames() string {
	return e.bin + " ps -a --format '{{.Names}}'"
}

// ListRunningNames is the shell command that prints only running
// container names, one per line on stdout.
func (e *Engine) ListRunningNames() string {
	return e.bin + " ps --format '{{.Names}}'"
}

// ListCodeboxInstances is the shell command that lists every container
// codebox manages (those carrying the `codebox=true` label), one per
// line. The format is `<name>|<createdAt>|<ports>` so the use-case
// layer can parse it without splitting on engine-specific whitespace.
func (e *Engine) ListCodeboxInstances() string {
	return e.bin + ` ps -a --filter label=codebox=true --format '{{.Names}}|{{.CreatedAt}}|{{.Ports}}'`
}

// Stop returns the shell command that stops a running container.
func (e *Engine) Stop(instance string) string {
	return fmt.Sprintf("%s stop %s", e.bin, shquote(instance))
}

// Remove returns the shell command that removes a stopped container.
func (e *Engine) Remove(instance string) string {
	return fmt.Sprintf("%s rm %s", e.bin, shquote(instance))
}

// Untag returns the shell command that strips every tag from the image
// codebox built for instance.
func (e *Engine) Untag(instance string) string {
	return fmt.Sprintf("%s untag %s", e.bin, shquote(instance))
}

// HostPort returns the shell command that prints the host-published
// address mapped to the container's sshd port (2222). Both podman and
// docker emit one `<addr>:<port>` line per protocol, e.g.
// `0.0.0.0:33000` or `[::]:33000`.
func (e *Engine) HostPort(instance string) string {
	return fmt.Sprintf("%s port %s 2222", e.bin, shquote(instance))
}

// Build returns a shell snippet that builds an image tagged `instance`
// from a Dockerfile supplied on stdin. The build context is a fresh
// empty directory so no files from the operator's working tree are
// sent to the engine; the directory is removed on EXIT.
//
// noCache adds --no-cache, used to honour --rebuild.
func (e *Engine) Build(instance string, noCache bool) string {
	noCacheFlag := ""
	if noCache {
		noCacheFlag = " --no-cache"
	}
	return fmt.Sprintf(
		`t=$(mktemp -d) && trap 'rm -rf "$t"' EXIT && %s build%s -t %s -f - "$t"`,
		e.bin, noCacheFlag, shquote(instance),
	)
}

// Run is the shell command that creates and starts a detached
// container labelled `codebox=true`, exposing every Dockerfile port
// on a host-assigned random port. The container's hostname is set to
// the instance name so an interactive shell shows the operator which
// sandbox they are in.
func (e *Engine) Run(instance string) string {
	q := shquote(instance)
	return fmt.Sprintf(
		`%s run -d --name %s --hostname %s --label codebox=true --publish-all %s`,
		e.bin, q, q, q,
	)
}

// shquote single-quotes s for safe embedding into a shell command.
// Empty input becomes the literal empty single-quoted string.
func shquote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
