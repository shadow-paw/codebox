package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"codebox/internal/container"
)

// VSCodeRequest is the use-case input for App.VSCode. Fields mirror the
// `codebox vscode` flags and positional argument, plus two environment
// facts the CLI captures so the app can pick the open strategy:
//
//   - InsideVSCodeRemote: codebox is running inside a VS Code SSH-remote
//     integrated terminal (TERM_PROGRAM=vscode with SSH_CONNECTION present).
//     `code` here opens paths on this host's filesystem, so the instance is
//     sshfs-mounted and opened locally.
//   - InsideSSHRemote: codebox is running inside an SSH session at all
//     (SSH_CONNECTION present), VS Code or not. When this is set but
//     InsideVSCodeRemote is not, codebox is on a remote host reached over
//     plain SSH with no local VS Code to drive — there is no sensible way to
//     launch the editor, so the command refuses rather than guessing.
//
// When neither is set codebox is on the operator's own workstation (a local
// VS Code or a plain terminal) and opens the instance over Remote-SSH.
type VSCodeRequest struct {
	Instance           string
	Orchestrator       string
	Remote             string
	InstanceKeys       []string
	InsideVSCodeRemote bool
	InsideSSHRemote    bool
}

// VSCode opens the named sandbox instance's ~/source in VS Code. The open
// strategy depends on where codebox is running:
//
//   - Inside a VS Code SSH-remote terminal (InsideVSCodeRemote): the
//     instance's ~/source is sshfs-mounted onto the default local
//     directory (mounting it first when that directory is missing or
//     empty) and `code` is pointed at that path, so the editor — whose
//     server shares this host's filesystem — opens the files directly.
//   - Inside a plain SSH session with no VS Code (InsideSSHRemote but not
//     InsideVSCodeRemote): there is no local editor to drive, so the
//     command refuses with an actionable message rather than launching a
//     `code` that would target the wrong machine.
//   - Otherwise (a local VS Code, or a non-VS-Code terminal on the
//     operator's workstation): a Remote-SSH URI (vscode-remote://ssh-remote+…)
//     targeting the in-container sshd is constructed, the connection string
//     is printed, and `code` is launched to open the instance over SSH.
//
// In the mount and Remote-SSH strategies a *.code-workspace file in
// ~/source is opened as a workspace; otherwise the directory is opened.
func (a *App) VSCode(ctx context.Context, stdout, stderr io.Writer, req VSCodeRequest) error {
	if err := validateInstanceName(req.Instance); err != nil {
		return err
	}
	switch {
	case req.InsideVSCodeRemote:
		return a.vscodeMounted(ctx, stdout, stderr, req)
	case req.InsideSSHRemote:
		return errVSCodeInsidePlainSSH
	default:
		eng, err := container.New(req.Orchestrator)
		if err != nil {
			return err
		}
		return a.vscodeRemote(ctx, stdout, req, eng)
	}
}

// errVSCodeInsidePlainSSH is returned when codebox runs inside an SSH
// session that is not a VS Code Remote-SSH terminal. In that situation the
// `code` CLI is not connected to an editor that can open the instance:
// launching it would either fail or target the wrong machine, so the
// command refuses and points the operator at the two modes that do work.
var errVSCodeInsidePlainSSH = errors.New(
	"cannot launch VS Code from a plain SSH session: this terminal is on a remote host reached over SSH " +
		"but not through VS Code's Remote-SSH, so there is no local VS Code to drive. " +
		"Run `codebox vscode` from your workstation's terminal (it will open the instance over Remote-SSH), " +
		"or from a VS Code Remote-SSH window (it will sshfs-mount and open the instance here)")

// vscodeRemote opens the instance over Remote-SSH from a VS Code that does
// not already share the instance host's filesystem. It runs the standard
// preflight (existence + host port), detects a workspace file in ~/source,
// builds a vscode-remote:// URI, prints the connection string, then
// launches `code` locally to open it.
//
// The URI authority depends on whether a bastion is in play. For a direct
// connection it is user@localhost:<hostPort>, which VS Code resolves with
// the operator's default ssh client. When the instance is reached through
// a bastion (--remote), VS Code's Remote-SSH offers no command-line
// ProxyJump, so codebox registers a managed host alias carrying the
// ProxyJump in ~/.ssh/codebox_config and the URI targets that alias
// instead, letting the connection traverse the bastion automatically.
func (a *App) vscodeRemote(
	ctx context.Context,
	stdout io.Writer,
	req VSCodeRequest,
	eng *container.Engine,
) error {
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

	keyPaths := expandHomeAll(req.InstanceKeys, a.home)
	workspace := a.detectWorkspace(ctx, req.Remote, hostPort, keyPaths)

	uriFlag := "--folder-uri"
	remotePath := instanceSourceAbs()
	if workspace != "" {
		uriFlag = "--file-uri"
		remotePath = path.Join(instanceSourceAbs(), workspace)
	}

	authority := fmt.Sprintf("%s@localhost:%s", instanceUser, hostPort)
	if req.Remote != "" {
		alias, err := a.ensureVSCodeSSHHost(req.Instance, hostPort, keyPaths, req.Remote)
		if err != nil {
			return err
		}
		authority = alias
	}
	uri := "vscode-remote://ssh-remote+" + authority + remotePath

	writeVSCodeConnectBlock(stdout, req.Instance, authority, uri, req.Remote)

	return a.launchCode(ctx, "code "+uriFlag+" "+shquote(uri))
}

// vscodeMounted opens the instance from inside a VS Code SSH-remote
// terminal, where `code` resolves paths on this host's filesystem. It
// sshfs-mounts the instance's ~/source onto the default local directory
// when that directory is missing or empty (reusing App.Mount), then
// points `code` at the mounted path — the workspace file when one is
// present, else the directory.
//
// The "missing or empty" test is a deliberate local-filesystem check
// rather than a /proc/mounts lookup: an absent or empty mount directory
// is the signal that ~/source has not been mounted yet, while any
// existing contents are taken to be an earlier mount and reused as-is.
func (a *App) vscodeMounted(ctx context.Context, stdout, stderr io.Writer, req VSCodeRequest) error {
	localDir, err := resolveMountDir("", req.Instance, a.home)
	if err != nil {
		return err
	}

	if dirHasContents(localDir) {
		_, _ = fmt.Fprintf(stdout, "Instance %q is already mounted at %s; reusing.\n", req.Instance, localDir)
	} else {
		_, _ = fmt.Fprintf(stdout,
			"Instance %q is not mounted (%s is missing or empty); mounting...\n", req.Instance, localDir)
		if err := a.Mount(ctx, stdout, stderr, MountRequest{
			Instance:     req.Instance,
			Orchestrator: req.Orchestrator,
			Remote:       req.Remote,
			InstanceKeys: req.InstanceKeys,
		}); err != nil {
			return err
		}
	}

	target := localDir
	if ws := workspaceInDir(localDir); ws != "" {
		target = ws
	}

	_, _ = fmt.Fprintf(stdout, "Opening %s in VS Code.\n", target)
	return a.launchCode(ctx, "code "+shquote(target))
}

// detectWorkspace returns the basename of the first *.code-workspace file
// in the instance's ~/source, or "" when none is present. The query is a
// one-shot ssh into the in-container sshd that lists the workspace glob and
// takes the first match. A lookup failure is treated as "no workspace
// file" — the directory is opened instead — so a transient error never
// blocks opening the editor.
func (a *App) detectWorkspace(ctx context.Context, remote, hostPort string, keyPaths []string) string {
	inner := fmt.Sprintf("ls -1 ~/%s/*.code-workspace 2>/dev/null | head -n 1", instanceSourceDir)
	cmd := buildInstanceSSHCommand(remote, hostPort, keyPaths, inner)
	var out bytes.Buffer
	if err := a.runners("").Run(ctx, cmd, nil, &out, io.Discard); err != nil {
		return ""
	}
	first := strings.TrimSpace(out.String())
	if first == "" {
		return ""
	}
	return path.Base(first)
}

// dirHasContents reports whether dir exists and holds at least one
// entry. A missing directory, an empty directory, or any read error all
// yield false — the caller treats that as "needs mounting", so a stat
// hiccup errs toward mounting (which then surfaces the real failure)
// rather than silently opening an empty path.
func dirHasContents(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// workspaceInDir returns the absolute path of the first *.code-workspace
// file directly inside dir, or "" when there is none. filepath.Glob
// returns its matches already sorted, so the choice is deterministic.
func workspaceInDir(dir string) string {
	matches, err := filepath.Glob(filepath.Join(dir, "*.code-workspace"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// launchCode runs a `code …` command on the operator's machine. A non-zero
// exit — most commonly `code` not being on PATH (127) — is wrapped with a
// hint, since launching the editor is the whole point of the command.
func (a *App) launchCode(ctx context.Context, codeCmd string) error {
	var errBuf bytes.Buffer
	if err := a.runners("").Run(ctx, codeCmd, nil, io.Discard, &errBuf); err != nil {
		if msg := strings.TrimSpace(errBuf.String()); msg != "" {
			return fmt.Errorf("launch VS Code (is the `code` command on your PATH?): %s", msg)
		}
		return fmt.Errorf("launch VS Code (is the `code` command on your PATH?): %w", err)
	}
	return nil
}

const (
	vscodeTopBar    = "──────── vscode ─────────────────────────────────────────────"
	vscodeBottomBar = "──────────────────────────────────────────────────────────────"
)

// writeVSCodeConnectBlock prints the Remote-SSH target and the
// vscode-remote URI bracketed by horizontal rules, mirroring the sshfs and
// git blocks elsewhere. When the instance is reached via a bastion
// (--remote), the ssh target is the codebox-managed host alias and the
// ProxyJump codebox wrote to ~/.ssh/codebox_config is surfaced so the
// operator can see how the hop is configured.
func writeVSCodeConnectBlock(w io.Writer, instance, authority, uri, remote string) {
	_, _ = fmt.Fprintln(w, vscodeTopBar)
	_, _ = fmt.Fprintf(w, "Opening instance %q in VS Code over Remote-SSH:\n", instance)
	_, _ = fmt.Fprintf(w, "  ssh target: %s\n", authority)
	_, _ = fmt.Fprintf(w, "  uri:        %s\n", uri)
	if remote != "" {
		_, _ = fmt.Fprintf(w,
			"  proxyjump:  %s (written to ~/.ssh/codebox_config, included from ~/.ssh/config)\n",
			remote)
	}
	_, _ = fmt.Fprintln(w, vscodeBottomBar)
	_, _ = fmt.Fprintln(w)
}

// vscodeIncludeFile is the dedicated ssh_config fragment codebox owns. It
// is referenced from ~/.ssh/config with a bare `Include codebox_config`,
// which ssh resolves relative to ~/.ssh — so codebox only ever rewrites
// its own file and never touches the operator's hand-written hosts.
const vscodeIncludeFile = "codebox_config"

// vscodeSSHAlias is the per-instance Host alias used as the vscode-remote
// authority when a bastion is in play. validateInstanceName has already
// constrained the instance name to characters safe both as an ssh_config
// token and inside the URI authority, so no escaping is needed.
func vscodeSSHAlias(instance string) string {
	return "codebox-" + instance
}

// vscodeBlockMarkers returns the begin/end comment markers that fence an
// instance's managed block inside the include file, so a later run can
// find and replace exactly that block without disturbing the others.
func vscodeBlockMarkers(instance string) (begin, end string) {
	return "# >>> codebox vscode " + instance + " >>>",
		"# <<< codebox vscode " + instance + " <<<"
}

// ensureVSCodeSSHHost writes (or refreshes) the managed Host block for the
// instance in ~/.ssh/codebox_config and makes sure ~/.ssh/config pulls it
// in via an `Include codebox_config` line, returning the Host alias to use
// as the vscode-remote authority.
//
// This is only needed when the instance is reached through a bastion
// (--remote): VS Code's Remote-SSH resolves its target through the
// operator's ssh config and accepts no command-line ProxyJump, so the hop
// has to live in config. The block mirrors the options codebox passes on
// its own ssh command lines — StrictHostKeyChecking off plus a throwaway
// known_hosts, since the localhost:<port> target is reused across
// instances and would otherwise trip a host-key mismatch — and adds one
// IdentityFile per --instance-key supplied.
func (a *App) ensureVSCodeSSHHost(instance, hostPort string, keyPaths []string, remote string) (string, error) {
	sshDir := filepath.Join(a.home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", sshDir, err)
	}

	alias := vscodeSSHAlias(instance)
	begin, end := vscodeBlockMarkers(instance)
	block := buildVSCodeSSHBlock(begin, end, alias, hostPort, keyPaths, remote)

	if err := upsertManagedBlock(filepath.Join(sshDir, vscodeIncludeFile), begin, end, block); err != nil {
		return "", err
	}
	if err := ensureSSHInclude(filepath.Join(sshDir, "config"), vscodeIncludeFile); err != nil {
		return "", err
	}
	return alias, nil
}

// buildVSCodeSSHBlock renders one fenced Host block. Values that may carry
// spaces (the IdentityFile path) are double-quoted, the form ssh_config
// uses for quoting; the alias, port, user and bastion are single tokens.
// The login is always the unprivileged in-container user (instanceUser).
func buildVSCodeSSHBlock(begin, end, alias, hostPort string, keyPaths []string, remote string) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "%s\n", begin)
	_, _ = fmt.Fprintf(&b, "Host %s\n", alias)
	_, _ = fmt.Fprintf(&b, "    HostName localhost\n")
	_, _ = fmt.Fprintf(&b, "    Port %s\n", hostPort)
	_, _ = fmt.Fprintf(&b, "    User %s\n", instanceUser)
	_, _ = fmt.Fprintf(&b, "    StrictHostKeyChecking no\n")
	_, _ = fmt.Fprintf(&b, "    UserKnownHostsFile /dev/null\n")
	hasKey := false
	for _, k := range keyPaths {
		if k != "" {
			_, _ = fmt.Fprintf(&b, "    IdentityFile %q\n", k)
			hasKey = true
		}
	}
	if hasKey {
		_, _ = fmt.Fprintf(&b, "    IdentitiesOnly yes\n")
	}
	_, _ = fmt.Fprintf(&b, "    ProxyJump %s\n", remote)
	_, _ = fmt.Fprintf(&b, "%s\n", end)
	return b.String()
}

// upsertManagedBlock writes block into path between the begin/end markers,
// replacing any existing block with the same markers and appending a fresh
// one when none is present. A missing file is created (0600, the mode ssh
// expects for config). Everything outside the marker pair is preserved
// verbatim, so the operator's own entries survive untouched.
func upsertManagedBlock(path, begin, end, block string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	stripped := removeManagedBlock(string(existing), begin, end)
	var b strings.Builder
	b.WriteString(stripped)
	if stripped != "" && !strings.HasSuffix(stripped, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(block)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// removeManagedBlock returns s with the first begin…end marker block
// (inclusive, plus a single trailing newline) removed. When the markers
// are absent, or present but malformed (begin without a following end), s
// is returned unchanged rather than risking truncation of real content.
func removeManagedBlock(s, begin, end string) string {
	bi := strings.Index(s, begin)
	if bi < 0 {
		return s
	}
	rel := strings.Index(s[bi:], end)
	if rel < 0 {
		return s
	}
	ei := bi + rel + len(end)
	rest := strings.TrimPrefix(s[ei:], "\n")
	return s[:bi] + rest
}

// ensureSSHInclude makes sure configPath carries an `Include <file>` line
// so the codebox-managed block is honoured. The line is prepended — ahead
// of any `Host *` defaults — because ssh resolves each parameter on a
// first-match basis, so the managed host's settings must be reachable
// before a broad wildcard could shadow them. A missing config file is
// created with just the Include line; an already-present Include is left
// alone.
func ensureSSHInclude(configPath, includeFile string) error {
	existing, err := os.ReadFile(configPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	if hasSSHInclude(string(existing), includeFile) {
		return nil
	}
	var b strings.Builder
	b.WriteString("Include " + includeFile + "\n")
	if len(existing) > 0 {
		b.WriteByte('\n')
		b.Write(existing)
	}
	if err := os.WriteFile(configPath, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}

// hasSSHInclude reports whether config already contains an Include
// directive referencing includeFile. The match is on the bare filename so
// an absolute or ~-prefixed form the operator added by hand still counts,
// avoiding a duplicate Include line.
func hasSSHInclude(config, includeFile string) bool {
	for _, line := range strings.Split(config, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Include") {
			continue
		}
		for _, f := range fields[1:] {
			if f == includeFile || path.Base(f) == includeFile {
				return true
			}
		}
	}
	return false
}

// removeVSCodeSSHHost deletes the instance's managed Host block from
// ~/.ssh/codebox_config, called by App.Delete so a torn-down instance
// leaves no stale ssh alias behind. When the removal empties the managed
// file it is deleted and the `Include codebox_config` line codebox added
// to ~/.ssh/config is removed too, leaving no dangling reference.
//
// A missing managed file — the common case, since the block is only ever
// written for a bastion connection — is a silent no-op. Genuine read/write
// failures are returned so the operator can resolve them; a notice is
// printed only when an alias was actually present, to keep ordinary
// deletes quiet.
func (a *App) removeVSCodeSSHHost(w io.Writer, instance string) error {
	sshDir := filepath.Join(a.home, ".ssh")
	fragPath := filepath.Join(sshDir, vscodeIncludeFile)

	content, err := os.ReadFile(fragPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", fragPath, err)
	}

	begin, end := vscodeBlockMarkers(instance)
	if !strings.Contains(string(content), begin) {
		return nil // no alias registered for this instance
	}

	_, _ = fmt.Fprintf(w, "Removing VS Code ssh alias %q from %s...\n", vscodeSSHAlias(instance), fragPath)
	stripped := removeManagedBlock(string(content), begin, end)

	// When the last managed block is gone, drop the file and its Include
	// rather than leaving an empty fragment behind.
	if strings.TrimSpace(stripped) == "" {
		if err := os.Remove(fragPath); err != nil {
			return fmt.Errorf("remove %s: %w", fragPath, err)
		}
		return removeSSHInclude(filepath.Join(sshDir, "config"), vscodeIncludeFile)
	}
	if err := os.WriteFile(fragPath, []byte(stripped), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", fragPath, err)
	}
	return nil
}

// removeSSHInclude drops the exact `Include <file>` line codebox prepended
// to configPath, used once the managed fragment has been emptied so no
// dangling include remains. Only codebox's own bare-filename form is
// matched; an absolute or ~-prefixed Include the operator wrote by hand is
// left untouched, as is the rest of the file. A leading blank line left
// where the directive sat (codebox writes the Include followed by a blank
// separator) is swallowed so the file does not accrete blank lines across
// create/delete cycles. A missing config file is a no-op.
func removeSSHInclude(configPath, includeFile string) error {
	content, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}

	want := "Include " + includeFile
	lines := strings.Split(string(content), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == want {
			continue
		}
		kept = append(kept, line)
	}
	out := strings.TrimLeft(strings.Join(kept, "\n"), "\n")
	if err := os.WriteFile(configPath, []byte(out), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}
