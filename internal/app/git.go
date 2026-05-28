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

// GitPushRequest is the use-case input for App.GitPush. Refspec is
// either `<source_remote>/<source_branch>:<target_branch>` or
// `<local_branch>:<target_branch>`. In the first form codebox fetches
// source_remote locally, then pushes `source_remote/source_branch` to
// `refs/heads/target_branch` on the instance; in the second form (no
// slash before the colon) codebox skips the fetch and pushes the local
// branch as-is. Either way, `target_branch` is checked out inside the
// instance.
//
// The instance-side repo always lives at `~/source` — one repo per
// sandbox, named uniformly so the operator never has to remember a
// per-checkout path.
type GitPushRequest struct {
	Instance     string
	Orchestrator string
	Remote       string
	InstanceKey  string
	Refspec      string
}

// GitPullRequest is the use-case input for App.GitPull. Branch is the
// ref on the instance side to fetch into a remote-tracking ref on the
// operator's machine; when empty it defaults to the instance name,
// matching the convention that a sandbox's working branch shares its
// name.
type GitPullRequest struct {
	Instance     string
	Orchestrator string
	Remote       string
	InstanceKey  string
	Branch       string
}

// instanceSourceDir is the fixed in-container path codebox initialises
// and pushes into. One repo per sandbox.
const instanceSourceDir = "source"

// instanceRemoteName is the name codebox uses for the local git remote
// that points at a sandbox instance. The `codebox-` prefix keeps these
// auto-managed remotes from colliding with remotes the operator
// configured by hand (e.g. one named `origin` or `upstream`).
func instanceRemoteName(instance string) string {
	return "codebox-" + instance
}

// GitPush pushes a local ref into a sandbox instance:
//
//  1. Verify the container exists and discover its host-side sshd port.
//  2. Initialise ~/source on the instance the first time around
//     (idempotent: untouched if .git already exists).
//  3. Set or refresh the local git remote so its URL points at the
//     instance's current published port and ~/source path.
//  4. `git fetch <source_remote>` locally so the remote-tracking ref
//     reflects upstream before we push it onward — skipped when the
//     refspec names a local branch directly (no source remote).
//  5. `git push <instance> <source>:refs/heads/<target_branch>` where
//     `<source>` is `source_remote/source_branch` or the bare local
//     branch, over an ssh transport that threads `-i KEY` and
//     `-J Remote` through GIT_SSH_COMMAND.
//  6. `git checkout <target_branch>` inside the instance so the
//     worktree tracks the freshly pushed branch.
func (a *App) GitPush(ctx context.Context, stdout, stderr io.Writer, req GitPushRequest) error {
	if err := validateInstanceName(req.Instance); err != nil {
		return err
	}
	sourceRemote, sourceBranch, targetBranch, err := parsePushRefspec(req.Refspec)
	if err != nil {
		return err
	}
	pushSource := sourceBranch
	if sourceRemote != "" {
		pushSource = sourceRemote + "/" + sourceBranch
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

	local := a.runners("")
	keyPath := expandHome(req.InstanceKey, a.home)

	name := readGitConfig(ctx, local, "user.name")
	email := readGitConfig(ctx, local, "user.email")

	initCmd := buildInstanceSSHCommand(req.Remote, hostPort, keyPath,
		instanceInitScript(name, email))
	_, _ = fmt.Fprintf(stdout, "Ensuring ~/%s exists on instance...\n", instanceSourceDir)
	if err := local.Run(ctx, initCmd, nil, stdout, stderr); err != nil {
		return wrapRunErr("initialise instance source dir", err, nil)
	}

	remoteName := instanceRemoteName(req.Instance)
	if err := setLocalRemote(ctx, local, remoteName,
		instanceRemoteURL(hostPort)); err != nil {
		return err
	}

	if sourceRemote != "" {
		fetchCmd := fmt.Sprintf("git fetch %s", shquote(sourceRemote))
		_, _ = fmt.Fprintf(stdout, "Fetching %q locally...\n", sourceRemote)
		if err := local.Run(ctx, fetchCmd, nil, stdout, stderr); err != nil {
			return wrapRunErr("git fetch source remote", err, nil)
		}
	}

	pushRefspec := fmt.Sprintf("%s:refs/heads/%s", pushSource, targetBranch)
	pushCmd := buildGitTransportCommand("push", remoteName, pushRefspec,
		req.Remote, keyPath)
	writeGitBlock(stdout, pushCmd)
	if err := local.Run(ctx, pushCmd, nil, stdout, stderr); err != nil {
		return wrapRunErr("git push", err, nil)
	}

	checkoutInner := fmt.Sprintf("cd %s && git checkout %s",
		shquote(instanceSourceAbs()),
		shquote(targetBranch))
	checkoutCmd := buildInstanceSSHCommand(req.Remote, hostPort, keyPath, checkoutInner)
	_, _ = fmt.Fprintf(stdout, "Checking out %q on instance...\n", targetBranch)
	if err := local.Run(ctx, checkoutCmd, nil, stdout, stderr); err != nil {
		return wrapRunErr("git checkout on instance", err, nil)
	}

	_, _ = fmt.Fprintf(stdout,
		"Repository cloned to instance %q at ~/%s (branch %q).\n",
		req.Instance, instanceSourceDir, targetBranch)
	return nil
}

// parsePushRefspec breaks the refspec into its components. Two forms
// are accepted:
//
//   - `source_remote/source_branch:target_branch` — the part before
//     the first slash names a remote configured in the operator's
//     repo; the source_branch may itself contain slashes
//     (e.g. `origin/feature/x:work`).
//   - `local_branch:target_branch` — no slash before the colon. The
//     returned sourceRemote is empty and sourceBranch carries the
//     local branch name. The caller skips the local `git fetch` step.
func parsePushRefspec(s string) (sourceRemote, sourceBranch, targetBranch string, err error) {
	if s == "" {
		return "", "", "", errors.New(
			"refspec is required (use 'local_branch:target_branch' or" +
				" 'source_remote/source_branch:target_branch')")
	}
	src, dst, ok := strings.Cut(s, ":")
	if !ok || src == "" || dst == "" {
		return "", "", "", fmt.Errorf(
			"refspec %q must be in the form 'local_branch:target_branch'"+
				" or 'source_remote/source_branch:target_branch'", s)
	}
	if !strings.Contains(src, "/") {
		return "", src, dst, nil
	}
	rem, br, _ := strings.Cut(src, "/")
	if rem == "" || br == "" {
		return "", "", "", fmt.Errorf(
			"refspec %q must name a source remote (e.g. 'origin/main:work')"+
				" or a local branch (e.g. 'main:work')", s)
	}
	return rem, br, dst, nil
}

// GitPull fetches a branch from the sandbox instance back into the
// operator's local repository. The remote URL is refreshed first in
// case the instance was restarted and its published host port changed.
func (a *App) GitPull(ctx context.Context, stdout, stderr io.Writer, req GitPullRequest) error {
	if err := validateInstanceName(req.Instance); err != nil {
		return err
	}
	branch := req.Branch
	if branch == "" {
		// A bare `git pull INSTANCE` fetches the branch that shares the
		// instance's name — the same branch `workflow`/`git push` check
		// out at ~/source. The instance name was just validated, so the
		// derived branch needs no further format check.
		branch = req.Instance
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

	local := a.runners("")
	keyPath := expandHome(req.InstanceKey, a.home)
	remoteName := instanceRemoteName(req.Instance)
	if err := setLocalRemote(ctx, local, remoteName,
		instanceRemoteURL(hostPort)); err != nil {
		return err
	}

	fetchCmd := buildGitTransportCommand("fetch", remoteName, branch,
		req.Remote, keyPath)
	writeGitBlock(stdout, fetchCmd)
	if err := local.Run(ctx, fetchCmd, nil, stdout, stderr); err != nil {
		return wrapRunErr("git fetch", err, nil)
	}

	_, _ = fmt.Fprintf(stdout,
		"Fetched %q from instance %q.\nTo check it out locally:\n  git checkout %s/%s -b %s\n",
		branch, req.Instance, remoteName, branch, branch)
	return nil
}

// instanceRemoteURL builds the ssh-style git URL that git stores in
// `.git/config` for the instance remote. Git invokes ssh against this
// URL; the port (which can change across container restarts) is
// re-resolved on every push/pull so the URL is always current.
// The path component is the fixed in-container source directory.
func instanceRemoteURL(hostPort string) string {
	return fmt.Sprintf("ssh://%s@localhost:%s%s",
		instanceUser, hostPort, instanceSourceAbs())
}

// instanceSourceAbs returns the absolute in-container path codebox
// initialises and pushes into — `/home/user/source`. Used both as the
// path component of the ssh URL git pushes/fetches against and as the
// `cd` target of the post-push checkout ssh hop.
func instanceSourceAbs() string {
	return fmt.Sprintf("/home/%s/%s", instanceUser, instanceSourceDir)
}

// readGitConfig returns the trimmed stdout of `git config --get key`,
// or "" if the key is not set anywhere visible to the operator's git.
// An unset identity is a valid outcome — we simply don't write one
// into the instance-side repo.
func readGitConfig(ctx context.Context, local CommandRunner, key string) string {
	var out bytes.Buffer
	if err := local.Run(ctx,
		fmt.Sprintf("git config --get %s", shquote(key)),
		nil, &out, io.Discard,
	); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// instanceInitScript is the shell snippet run inside the instance the
// first time `codebox git push` is invoked. It is idempotent: if
// `~/source/.git` already exists, nothing is touched. The init layer
// pins `receive.denyCurrentBranch=updateInstead` so subsequent pushes
// to the currently checked-out branch update the working tree
// atomically (and refuse if it is dirty). name/email, when non-empty,
// are written into the per-repo config — only at init time, matching
// the contract.
func instanceInitScript(name, email string) string {
	dir := "~/" + instanceSourceDir
	var ident []string
	if name != "" {
		ident = append(ident, "git config user.name "+shquote(name))
	}
	if email != "" {
		ident = append(ident, "git config user.email "+shquote(email))
	}
	extra := ""
	if len(ident) > 0 {
		extra = " && " + strings.Join(ident, " && ")
	}
	return fmt.Sprintf(
		"if [ ! -d %s/.git ]; then mkdir -p %s && git init -q %s && cd %s && "+
			"git config receive.denyCurrentBranch updateInstead%s; fi",
		dir, dir, dir, dir, extra,
	)
}

// buildInstanceSSHCommand wraps `inner` in an ssh invocation targeting
// the in-container sshd. Mirrors exec's transport shape: -i KEY for
// the inner hop only, -J Remote when the orchestrator host is reached
// via a bastion.
func buildInstanceSSHCommand(remote, hostPort, instanceKey, inner string) string {
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
	parts = append(parts, shquote(inner))
	return strings.Join(parts, " ")
}

// buildGitTransportCommand assembles `GIT_SSH_COMMAND=... git <verb>
// <name> <arg>` where <verb> is push or fetch. The remote URL stored
// in .git/config does not encode `-i` / `-J`; those options live on
// GIT_SSH_COMMAND so they apply only when codebox invokes git.
func buildGitTransportCommand(verb, name, arg, remote, instanceKey string) string {
	ssh := buildGitInnerSSH(remote, instanceKey)
	return fmt.Sprintf("GIT_SSH_COMMAND=%s git %s %s %s",
		shquote(ssh), verb, shquote(name), shquote(arg))
}

// buildGitInnerSSH returns the ssh command-string git invokes when
// talking to the instance. No `-p` is included: the port is part of
// the URL git was handed by the operator's local config.
func buildGitInnerSSH(remote, instanceKey string) string {
	parts := []string{"ssh", "-o", "StrictHostKeyChecking=no"}
	if instanceKey != "" {
		parts = append(parts, "-i", shquote(instanceKey))
	}
	if remote != "" {
		parts = append(parts, "-J", shquote(remote))
	}
	return strings.Join(parts, " ")
}

// setLocalRemote idempotently points the named git remote at url. The
// `set-url ... || add` pattern handles both cases without an extra
// runner round-trip: set-url succeeds when the remote is present,
// otherwise add creates it. Errors from a non-git working directory
// reach the operator verbatim through the runner's stderr passthrough.
func setLocalRemote(ctx context.Context, local CommandRunner, name, url string) error {
	cmd := fmt.Sprintf(
		"git remote set-url %s %s 2>/dev/null || git remote add %s %s",
		shquote(name), shquote(url), shquote(name), shquote(url),
	)
	var errBuf bytes.Buffer
	if err := local.Run(ctx, cmd, nil, io.Discard, &errBuf); err != nil {
		return wrapRunErr("configure git remote", err, &errBuf)
	}
	return nil
}

// removeLocalRemote drops the named git remote from the operator's
// local repository if it is present. A failing `git remote get-url`
// covers all the cases where there is nothing to clean up — remote
// absent, current directory is not a git repo, git not on PATH — and
// is treated as a silent no-op. When the remote does exist, codebox
// prints a progress line and surfaces any failure from `git remote
// remove` to the operator.
func removeLocalRemote(ctx context.Context, w io.Writer, local CommandRunner, name string) error {
	if err := local.Run(ctx,
		fmt.Sprintf("git remote get-url %s", shquote(name)),
		nil, io.Discard, io.Discard,
	); err != nil {
		return nil
	}
	_, _ = fmt.Fprintf(w, "Removing local git remote %q...\n", name)
	var errBuf bytes.Buffer
	if err := local.Run(ctx,
		fmt.Sprintf("git remote remove %s", shquote(name)),
		nil, io.Discard, &errBuf,
	); err != nil {
		return wrapRunErr("remove git remote", err, &errBuf)
	}
	return nil
}

const (
	gitTopBar    = "──────── git ────────────────────────────────────────────────"
	gitBottomBar = "──────────────────────────────────────────────────────────────"
)

// writeGitBlock prints the git command bracketed by horizontal rules
// so it stands apart from git's own output, mirroring the Dockerfile
// and rsync blocks elsewhere.
func writeGitBlock(w io.Writer, cmd string) {
	_, _ = fmt.Fprintln(w, gitTopBar)
	_, _ = fmt.Fprintln(w, cmd)
	_, _ = fmt.Fprintln(w, gitBottomBar)
	_, _ = fmt.Fprintln(w)
}
