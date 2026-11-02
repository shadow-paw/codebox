package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"codebox/internal/container"
)

// CopyStep is one builder.additional-copy entry. From is a file,
// directory, or glob pattern on the operator's machine; a leading "~"
// is the operator's home, and a relative path is resolved against the
// current directory. To is the destination directory or pathname inside
// the instance; a relative path (or one starting "~/") is under the
// in-container user's home. Exclude lists rsync exclude patterns, each
// passed as its own --exclude: a pattern without "/" matches a name at
// any depth, a leading "/" anchors it to the root of the copy, a
// trailing "/" matches directories only, and "**" spans directories.
type CopyStep struct {
	From    string
	To      string
	Exclude []string
}

// resolvedCopy is a CopyStep after source globbing and destination
// normalisation, ready to become a single rsync invocation.
type resolvedCopy struct {
	step CopyStep
	srcs []string
	dst  string
}

// resolveCopySteps resolves every step's sources and destination. It
// runs before the image build, so a malformed entry or a source that
// matches nothing is reported without wasting a build.
func resolveCopySteps(steps []CopyStep, home string) ([]resolvedCopy, error) {
	out := make([]resolvedCopy, 0, len(steps))
	for _, s := range steps {
		if s.From == "" {
			return nil, fmt.Errorf("builder.additional-copy: entry with to %q is missing from", s.To)
		}
		if s.To == "" {
			return nil, fmt.Errorf("builder.additional-copy: entry with from %q is missing to", s.From)
		}
		srcs, err := resolveCopySources(s.From, home)
		if err != nil {
			return nil, err
		}
		out = append(out, resolvedCopy{step: s, srcs: srcs, dst: resolveCopyDest(s.To)})
	}
	return out, nil
}

// resolveCopySources expands a leading "~" in from and globs it
// (filepath.Match syntax: *, ?, [...]). A trailing slash is kept on
// every match so rsync copies the directory's contents rather than the
// directory itself — the same meaning it has for `codebox file push`.
// A pattern that matches nothing is an error.
func resolveCopySources(from, home string) ([]string, error) {
	p := expandHome(from, home)
	contents := strings.HasSuffix(p, "/")
	pattern := strings.TrimRight(p, "/")
	if pattern == "" {
		pattern = "/"
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("builder.additional-copy: bad pattern %q: %w", from, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("builder.additional-copy: %q matches no files", from)
	}
	if contents {
		for i, m := range matches {
			if !strings.HasSuffix(m, "/") {
				matches[i] = m + "/"
			}
		}
	}
	return matches, nil
}

// resolveCopyDest maps a leading "~" in an in-container destination to
// the instance user's home (rsync does not expand it on the receiving
// side). Anything else is passed through verbatim, exactly like `codebox
// file push --instance-path`: rsync decides from the sources and any
// trailing slash whether to is a directory to copy into or a pathname.
func resolveCopyDest(to string) string {
	userHome := "/home/" + instanceUser
	switch {
	case to == "~":
		to = userHome
	case strings.HasPrefix(to, "~/"):
		to = userHome + "/" + to[2:]
	}
	return to
}

// pushAdditionalCopies runs one rsync per builder.additional-copy step,
// in order, into the freshly-started instance. The transfer is the
// `codebox file push` rsync plus --mkpath so missing destination
// directories are created, and one --exclude per exclude pattern. Like the credential pushes, each transfer is
// retried once in case the in-container sshd is not ready yet.
func (a *App) pushAdditionalCopies(
	ctx context.Context,
	w io.Writer,
	rnr CommandRunner,
	eng *container.Engine,
	req CreateRequest,
	copies []resolvedCopy,
) error {
	hostPort, err := lookupHostPort(ctx, rnr, eng, req.Instance)
	if err != nil {
		return err
	}
	keys := expandHomeAll(req.InstanceKeys, a.home)
	for _, c := range copies {
		dst := fmt.Sprintf("%s@localhost:%s", instanceUser, c.dst)
		rsyncCmd := buildCopyRsyncCommand(req.Remote, hostPort, keys, c.step.Exclude, c.srcs, dst)
		label := fmt.Sprintf("Copying %s to %s", c.step.From, c.step.To)
		if err := a.runRsyncWithRetry(ctx, w, label, rsyncCmd); err != nil {
			return fmt.Errorf("builder.additional-copy %q: %w", c.step.From, err)
		}
	}
	return nil
}
