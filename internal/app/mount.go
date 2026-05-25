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

	"codebox/internal/container"
)

// MountRequest is the use-case input for App.Mount. Fields mirror the
// `codebox mount` flags and positional arguments. LocalDir is the
// operator-side directory the instance's ~/source will be mounted onto
// via sshfs; the empty string defaults to .codebox/<instance>/ relative
// to the current working directory.
type MountRequest struct {
	Instance     string
	Orchestrator string
	Remote       string
	InstanceKey  string
	LocalDir     string
}

// UnmountRequest is the use-case input for App.Unmount. Mirrors
// MountRequest; LocalDir applies the same default.
type UnmountRequest struct {
	Instance     string
	Orchestrator string
	Remote       string
	InstanceKey  string
	LocalDir     string
}

// fsnameFor returns the fsname tag passed to sshfs via `-o fsname=`.
// The tag becomes the source column in /proc/mounts, which is how
// `codebox delete` later identifies and tears down mounts that belong
// to this instance.
func fsnameFor(instance string) string {
	return "codebox-" + instance
}

// Mount sshfs-mounts the instance's ~/source directory onto a local
// directory on the operator's machine. The orchestrator preflight
// (existence check + host port lookup) is identical to push/pull/exec;
// the sshfs invocation itself runs locally so its progress and any
// permission-denied messages stream straight to the operator.
func (a *App) Mount(ctx context.Context, stdout, stderr io.Writer, req MountRequest) error {
	if err := validateInstanceName(req.Instance); err != nil {
		return err
	}
	eng, err := container.New(req.Orchestrator)
	if err != nil {
		return err
	}

	local := a.runners("")
	if err := requireSshfsInstalled(ctx, local); err != nil {
		return err
	}

	localDir, err := resolveMountDir(req.LocalDir, req.Instance, a.home)
	if err != nil {
		return err
	}

	mounted, err := isMountPoint(ctx, local, localDir)
	if err != nil {
		return err
	}
	if mounted {
		return fmt.Errorf(
			"local directory %q is already a mount point; unmount it first:\n  codebox umount %s %s",
			localDir, req.Instance, localDir)
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

	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return fmt.Errorf("create local directory %q: %w", localDir, err)
	}

	keyPath := expandHome(req.InstanceKey, a.home)
	initCmd := buildInstanceSSHCommand(req.Remote, hostPort, keyPath,
		fmt.Sprintf("mkdir -p ~/%s", instanceSourceDir))
	var initErr bytes.Buffer
	if err := local.Run(ctx, initCmd, nil, io.Discard, &initErr); err != nil {
		return wrapRunErr("create instance source directory", err, &initErr)
	}

	sshfsCmd := buildSshfsCommand(req.Instance, req.Remote, hostPort, keyPath, localDir)
	writeSshfsBlock(stdout, sshfsCmd)
	var sshfsErr bytes.Buffer
	if err := local.Run(ctx, sshfsCmd, nil, stdout, &sshfsErr); err != nil {
		msg := strings.TrimSpace(sshfsErr.String())
		switch {
		case strings.Contains(strings.ToLower(msg), "permission denied"):
			return fmt.Errorf("sshfs: permission denied: %s", msg)
		case msg != "":
			return fmt.Errorf("sshfs: %s", msg)
		default:
			return fmt.Errorf("sshfs: %w", err)
		}
	}

	_, _ = fmt.Fprintf(stdout, "Mounted instance %q ~/%s at %s.\n",
		req.Instance, instanceSourceDir, localDir)
	return nil
}

// Unmount tears down an sshfs mount previously established by Mount.
// fusermount itself is the source of truth — no pre-check against
// /proc/mounts — so the command works against any path the operator
// believes is a stale mount and fusermount's own error surfaces when
// it really is not. The directory is removed afterwards when empty.
func (a *App) Unmount(ctx context.Context, stdout, stderr io.Writer, req UnmountRequest) error {
	if err := validateInstanceName(req.Instance); err != nil {
		return err
	}
	if _, err := container.New(req.Orchestrator); err != nil {
		return err
	}
	localDir, err := resolveMountDir(req.LocalDir, req.Instance, a.home)
	if err != nil {
		return err
	}
	local := a.runners("")
	if err := runFusermount(ctx, local, localDir); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Unmounted %s.\n", localDir)
	removeIfEmpty(stdout, localDir)
	return nil
}

// removeIfEmpty deletes dir when empty and reports the removal to w.
// os.Remove on a directory only succeeds when it has no entries, so a
// populated leftover (operator dropped files inside, or a pre-existing
// path was used as the mount point) is left in place silently.
func removeIfEmpty(w io.Writer, dir string) {
	if err := os.Remove(dir); err == nil {
		_, _ = fmt.Fprintf(w, "Removed empty %s.\n", dir)
	}
}

// resolveMountDir returns the absolute local directory to mount or
// unmount. An empty input becomes .codebox/<instance>/ relative to the
// current working directory; a relative path is resolved against cwd
// too so subsequent operations from a different directory still target
// the same on-disk location.
func resolveMountDir(in, instance, home string) (string, error) {
	if in == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("locate current directory: %w", err)
		}
		return filepath.Join(cwd, ".codebox", instance), nil
	}
	expanded := expandHome(in, home)
	if filepath.IsAbs(expanded) {
		return expanded, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate current directory: %w", err)
	}
	return filepath.Join(cwd, expanded), nil
}

// requireSshfsInstalled fails fast when sshfs is not on the operator's
// PATH. `command -v` is a POSIX builtin that prints the resolved path
// and exits 0 on hit, exits non-zero otherwise — both are usable
// signals through the local runner.
func requireSshfsInstalled(ctx context.Context, local CommandRunner) error {
	var out bytes.Buffer
	err := local.Run(ctx, "command -v sshfs", nil, &out, io.Discard)
	if err != nil || strings.TrimSpace(out.String()) == "" {
		return errors.New(
			"sshfs is not installed on this machine; install it (e.g. `sudo apt install sshfs`) and try again")
	}
	return nil
}

// mountEntry captures the source and target columns parsed from one
// /proc/mounts line.
type mountEntry struct {
	source string
	target string
}

// readMounts returns the parsed /proc/mounts table on Linux. A failure
// to read it (non-Linux, unreadable, etc.) returns nil — callers treat
// that as "no mounts visible" rather than erroring out.
func readMounts(ctx context.Context, local CommandRunner) []mountEntry {
	var out bytes.Buffer
	if err := local.Run(ctx, "cat /proc/mounts 2>/dev/null", nil, &out, io.Discard); err != nil {
		return nil
	}
	return parseProcMounts(out.String())
}

// isMountPoint reports whether localDir appears as a target in
// /proc/mounts. Paths are compared after symlink-free absolute
// resolution so a relative input matches its canonical form in the
// mount table.
func isMountPoint(ctx context.Context, local CommandRunner, localDir string) (bool, error) {
	abs, err := filepath.Abs(localDir)
	if err != nil {
		return false, fmt.Errorf("resolve %q: %w", localDir, err)
	}
	for _, e := range readMounts(ctx, local) {
		if e.target == abs {
			return true, nil
		}
	}
	return false, nil
}

// parseProcMounts splits the `<source> <target> <fstype> ...` lines
// emitted in /proc/mounts. Whitespace inside source/target is
// octal-escaped on the kernel side; unescapeProcMounts puts it back.
func parseProcMounts(s string) []mountEntry {
	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		return nil
	}
	var entries []mountEntry
	for _, line := range strings.Split(trimmed, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		entries = append(entries, mountEntry{
			source: unescapeProcMounts(fields[0]),
			target: unescapeProcMounts(fields[1]),
		})
	}
	return entries
}

// unescapeProcMounts replaces the four octal escapes the kernel uses
// for whitespace and backslashes inside /proc/mounts fields.
func unescapeProcMounts(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	r := strings.NewReplacer(
		`\040`, " ",
		`\011`, "\t",
		`\012`, "\n",
		`\134`, `\`,
	)
	return r.Replace(s)
}

// buildSshfsCommand assembles the sshfs invocation that mounts the
// instance's ~/source directory onto localDir. The options mirror the
// transport used by push/pull/exec: StrictHostKeyChecking is disabled
// (codebox controls the keypair via authorized_keys), an explicit
// IdentityFile is set when --instance-key is supplied, and ProxyJump
// is added when the orchestrator host is reached via a bastion. The
// `fsname=` tag is what `codebox delete` later greps for in
// /proc/mounts to clean up dangling mounts.
func buildSshfsCommand(instance, remote, hostPort, instanceKey, localDir string) string {
	parts := []string{
		"sshfs",
		shquote(fmt.Sprintf("%s@localhost:%s", instanceUser, instanceSourceAbs())),
		shquote(localDir),
		"-p", hostPort,
		"-o", "StrictHostKeyChecking=no",
		"-o", "fsname=" + shquote(fsnameFor(instance)),
		"-o", "reconnect",
	}
	if instanceKey != "" {
		parts = append(parts, "-o", "IdentityFile="+shquote(instanceKey))
	}
	if remote != "" {
		parts = append(parts, "-o", "ProxyJump="+shquote(remote))
	}
	return strings.Join(parts, " ")
}

// runFusermount unmounts a sshfs mount point using `fusermount -u`,
// the standard non-root tear-down for fuse mounts on Linux. stderr is
// captured into the wrapped error so the operator gets the underlying
// reason on failure (busy mount, stale handle, etc.).
func runFusermount(ctx context.Context, local CommandRunner, localDir string) error {
	cmd := fmt.Sprintf("fusermount -u %s", shquote(localDir))
	var errBuf bytes.Buffer
	if err := local.Run(ctx, cmd, nil, io.Discard, &errBuf); err != nil {
		return wrapRunErr("unmount", err, &errBuf)
	}
	return nil
}

// unmountInstanceMounts tears down every sshfs mount whose source
// column is the fsname tag for instance. Called by App.Delete before
// the container is stopped so the operator never ends up with a
// dangling mount pointing at a defunct sshd. Failures to read
// /proc/mounts (non-Linux, restricted) silently yield zero mounts;
// failures to unmount a found entry are propagated so the operator can
// resolve them (e.g. close open files) and re-run delete.
func (a *App) unmountInstanceMounts(ctx context.Context, w io.Writer, instance string) error {
	local := a.runners("")
	tag := fsnameFor(instance)
	for _, e := range readMounts(ctx, local) {
		if e.source != tag {
			continue
		}
		_, _ = fmt.Fprintf(w, "Unmounting %s...\n", e.target)
		if err := runFusermount(ctx, local, e.target); err != nil {
			return err
		}
		removeIfEmpty(w, e.target)
	}
	return nil
}

const (
	sshfsTopBar    = "──────── sshfs ──────────────────────────────────────────────"
	sshfsBottomBar = "──────────────────────────────────────────────────────────────"
)

// writeSshfsBlock prints the sshfs command bracketed by horizontal
// rules so it stands apart from sshfs's own output, mirroring the
// Dockerfile, rsync and git blocks elsewhere.
func writeSshfsBlock(w io.Writer, cmd string) {
	_, _ = fmt.Fprintln(w, sshfsTopBar)
	_, _ = fmt.Fprintln(w, cmd)
	_, _ = fmt.Fprintln(w, sshfsBottomBar)
	_, _ = fmt.Fprintln(w)
}
