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

// SupportedOrchestrators returns the orchestrator names New accepts,
// in deterministic order. Intended for shell-completion candidate
// lookup; New uses the same underlying set.
func SupportedOrchestrators() []string {
	return []string{"podman", "docker"}
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
// codebox built for instance. Docker has no `untag` verb, so there we
// fall back to `rmi`, which deletes the image by tag.
func (e *Engine) Untag(instance string) string {
	verb := "untag"
	if e.bin == "docker" {
		verb = "rmi"
	}
	return fmt.Sprintf("%s %s %s", e.bin, verb, shquote(instance))
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

// podmanRunFlags are added to the run command when the instance hosts
// its own rootless Podman (--podman). Rather than --privileged, the
// nested engine is granted only the pieces it needs: /dev/fuse for the
// fuse-overlayfs storage driver, /dev/net/tun for pasta networking, the
// sys_admin/net_admin/mknod capabilities for mounting and device nodes,
// and SELinux/seccomp relaxations so the inner containers are not
// blocked by the host's confinement.
const podmanRunFlags = "--device /dev/fuse --device /dev/net/tun " +
	"--cap-add=sys_admin --cap-add=net_admin --cap-add=mknod " +
	"--security-opt label=disable --security-opt unmask=ALL"

// Run is the shell command that creates and starts a detached
// container labelled `codebox=true`, exposing every Dockerfile port
// on a host-assigned random port. The container's hostname is set to
// the instance name so an interactive shell shows the operator which
// sandbox they are in.
//
// labels carries extra `key=value` metadata stamped onto the container
// in addition to the mandatory `codebox=true` — e.g. `tmux=true` and a
// boolean label per installed agent (`claude=true`). `codebox shell`
// reads them back (see Labels) to decide whether to launch tmux on
// connect and which agent to run in its right-hand pane. Entries are
// emitted in the given order and must already be safe `key=value`
// literals (codebox controls them).
//
// podman adds the device/capability/security-opt flags
// (podmanRunFlags) the instance needs to run rootless Podman of its
// own (--podman); nested containers need them for fuse storage, pasta
// networking, and to escape the host's SELinux/seccomp confinement.
func (e *Engine) Run(instance string, podman bool, labels []string) string {
	q := shquote(instance)
	labelFlags := ""
	for _, l := range labels {
		labelFlags += " --label " + l
	}
	podmanFlags := ""
	if podman {
		podmanFlags = " " + podmanRunFlags
	}
	return fmt.Sprintf(
		`%s run -d --name %s --hostname %s --label codebox=true%s%s --publish-all %s`,
		e.bin, q, q, labelFlags, podmanFlags, q,
	)
}

// Labels returns the shell command that prints the values of the named
// container labels on stdout, one per line in the order requested; an
// unset label yields an empty line. The keys are codebox-controlled
// literals (e.g. "tmux", "agent"), embedded into the Go-template format
// directly; the instance name is shell-quoted.
func (e *Engine) Labels(instance string, keys ...string) string {
	exprs := make([]string, len(keys))
	for i, k := range keys {
		exprs[i] = fmt.Sprintf(`{{ index .Config.Labels "%s" }}`, k)
	}
	return fmt.Sprintf(
		`%s inspect %s --format '%s'`,
		e.bin, shquote(instance), strings.Join(exprs, `{{"\n"}}`),
	)
}

// Exec returns the shell command that runs argv inside the running
// container as user "user", with HOME pointed at the user's home so
// tools that read per-user state (rootless Podman's storage and config
// under ~/.local and ~/.config) resolve it correctly. The container
// name is shell-quoted; argv tokens are passed through verbatim and
// must already be safe literals.
func (e *Engine) Exec(instance string, argv ...string) string {
	parts := make([]string, 0, 7+len(argv))
	parts = append(parts,
		e.bin, "exec",
		"--user", "user",
		"--env", "HOME=/home/user",
		shquote(instance),
	)
	parts = append(parts, argv...)
	return strings.Join(parts, " ")
}

// shquote single-quotes s for safe embedding into a shell command.
// Empty input becomes the literal empty single-quoted string.
func shquote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
