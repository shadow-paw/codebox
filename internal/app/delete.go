package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"codebox/internal/container"
)

// DeleteRequest is the use-case input for App.Delete. Fields mirror
// the `codebox delete` flags.
type DeleteRequest struct {
	Instance     string
	Orchestrator string
	Remote       string
}

// Delete tears down a sandbox instance: it confirms the container
// exists, stops it if it is still running, removes the container, and
// untags the image codebox built for it. Engine stdout (which
// otherwise echoes the container/image name) is captured to internal
// buffers so the operator only sees the human-readable progress lines
// codebox prints.
func (a *App) Delete(ctx context.Context, w io.Writer, req DeleteRequest) error {
	if err := validateInstanceName(req.Instance); err != nil {
		return err
	}
	eng, err := container.New(req.Orchestrator)
	if err != nil {
		return err
	}
	rnr := a.runners(req.Remote)

	if err := requireExists(ctx, rnr, eng, req.Instance); err != nil {
		return err
	}

	if err := a.unmountInstanceMounts(ctx, w, req.Instance); err != nil {
		return err
	}

	running, err := isRunning(ctx, rnr, eng, req.Instance)
	if err != nil {
		return err
	}
	if running {
		_, _ = fmt.Fprintf(w, "Stopping container %q...\n", req.Instance)
		var stopOut, stopErr bytes.Buffer
		if err := rnr.Run(ctx, eng.Stop(req.Instance), nil, &stopOut, &stopErr); err != nil {
			return wrapRunErr("stop container", err, &stopErr)
		}
	}

	_, _ = fmt.Fprintf(w, "Deleting container %q...\n", req.Instance)
	var rmOut, rmErr bytes.Buffer
	if err := rnr.Run(ctx, eng.Remove(req.Instance), nil, &rmOut, &rmErr); err != nil {
		return wrapRunErr("remove container", err, &rmErr)
	}

	var untagOut, untagErr bytes.Buffer
	if err := rnr.Run(ctx, eng.Untag(req.Instance), nil, &untagOut, &untagErr); err != nil {
		// A missing image means there is nothing left to untag — the
		// desired end state is already reached, so warn and proceed
		// rather than aborting before the git remote is cleaned up.
		if isImageNotKnown(&untagErr) {
			_, _ = fmt.Fprintf(w, "Image for %q already gone; skipping untag.\n", req.Instance)
		} else {
			return wrapRunErr("untag image", err, &untagErr)
		}
	}

	return removeLocalRemote(ctx, w, a.runners(""), instanceRemoteName(req.Instance))
}

// isImageNotKnown reports whether an untag failure is the benign case
// where the image is already absent. Podman prints "image not known"
// (and docker's rmi prints "No such image") when there is nothing to
// remove; both mean the untag goal is already satisfied.
func isImageNotKnown(stderr *bytes.Buffer) bool {
	msg := strings.ToLower(stderr.String())
	return strings.Contains(msg, "image not known") ||
		strings.Contains(msg, "no such image")
}

// requireExists fails with a "not found" error if the named container
// is not present on the target host.
func requireExists(ctx context.Context, rnr CommandRunner, eng *container.Engine, instance string) error {
	var out, errBuf bytes.Buffer
	if err := rnr.Run(ctx, eng.ListAllNames(), nil, &out, &errBuf); err != nil {
		return wrapRunErr("list containers", err, &errBuf)
	}
	if !nameInList(out.String(), instance) {
		return fmt.Errorf("instance %q not found", instance)
	}
	return nil
}

// isRunning reports whether the named container is currently running.
func isRunning(ctx context.Context, rnr CommandRunner, eng *container.Engine, instance string) (bool, error) {
	var out, errBuf bytes.Buffer
	if err := rnr.Run(ctx, eng.ListRunningNames(), nil, &out, &errBuf); err != nil {
		return false, wrapRunErr("list running containers", err, &errBuf)
	}
	return nameInList(out.String(), instance), nil
}
