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
