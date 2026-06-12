package app_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codebox/internal/adapters/runner"
	"codebox/internal/app"
)

func TestDelete_HappyPath_StopRemoveUntag(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\nother\n"},                     // ps -a — exists
		reply{stdout: ""},                                  // cat /proc/mounts — no codebox mounts
		reply{stdout: "demo\n"},                            // ps — running
		reply{stdout: "demo\n"},                            // stop — podman echoes name
		reply{stdout: "demo\n"},                            // rm — podman echoes name
		reply{stdout: "untagged: localhost/demo:latest\n"}, // untag chatter
		reply{err: &exec.ExitError{}},                      // git remote get-url — absent
	)

	var out bytes.Buffer
	err := a.Delete(context.Background(), &out, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("Delete: %v\nout:\n%s", err, out.String())
	}

	if got := len(fr.calls); got != 7 {
		t.Fatalf("expected 7 runner calls (ps-a, mounts, ps, stop, rm, untag, git get-url), got %d: %+v", got, fr.calls)
	}
	wantSubstrings := []string{
		"podman ps -a --format",
		"cat /proc/mounts",
		"podman ps --format",
		"podman stop 'demo'",
		"podman rm 'demo'",
		"podman untag 'demo'",
		"git remote get-url 'codebox-demo'",
	}
	for i, want := range wantSubstrings {
		if !strings.Contains(fr.calls[i].cmd, want) {
			t.Errorf("call[%d] should contain %q, got %q", i, want, fr.calls[i].cmd)
		}
	}
	if strings.Contains(out.String(), "Removing local git remote") {
		t.Errorf("absent remote should not produce a removal progress line:\n%s", out.String())
	}

	if !strings.Contains(out.String(), `Stopping container "demo"`) {
		t.Errorf("expected stopping progress line, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `Deleting container "demo"`) {
		t.Errorf("expected deleting progress line, got:\n%s", out.String())
	}
	// Engine stdout (bare "demo" lines, "untagged: ...") must not leak to
	// the operator-facing writer.
	for _, leak := range []string{"\ndemo\n", "untagged:"} {
		if strings.Contains(out.String(), leak) {
			t.Errorf("engine stdout leaked into user output (found %q):\n%s", leak, out.String())
		}
	}
}

func TestDelete_AlreadyStopped_SkipsStop(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},       // ps -a — exists
		reply{stdout: ""},             // cat /proc/mounts
		reply{stdout: "\n"},           // ps — nothing running
		reply{stdout: "demo\n"},       // rm
		reply{},                       // untag
		reply{err: &exec.ExitError{}}, // git remote get-url — absent
	)

	var out bytes.Buffer
	err := a.Delete(context.Background(), &out, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := len(fr.calls); got != 6 {
		t.Fatalf("expected 6 runner calls (no stop), got %d: %+v", got, fr.calls)
	}
	if strings.Contains(out.String(), "Stopping") {
		t.Errorf("Stopping progress line should be omitted for a stopped container:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `Deleting container "demo"`) {
		t.Errorf("expected deleting progress line, got:\n%s", out.String())
	}
	if !strings.Contains(fr.calls[3].cmd, "podman rm 'demo'") {
		t.Errorf("call[3] should be rm, got %q", fr.calls[3].cmd)
	}
	if !strings.Contains(fr.calls[4].cmd, "podman untag 'demo'") {
		t.Errorf("call[4] should be untag, got %q", fr.calls[4].cmd)
	}
}

func TestDelete_NotFound(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "other\nanother\n"}, // ps -a — no match
	)

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err == nil {
		t.Fatal("expected error when instance is absent")
	}
	if !strings.Contains(err.Error(), `instance "demo" not found`) {
		t.Errorf("error should say not found, got %v", err)
	}
	if got := len(fr.calls); got != 1 {
		t.Errorf("only ps -a should run when instance is missing, got %d calls", got)
	}
}

func TestDelete_RemoteUsesSSH(t *testing.T) {
	t.Parallel()
	// Track every host the factory is invoked with so we can confirm
	// the orchestrator-bound calls reach the remote and the
	// /proc/mounts read + git remote cleanup stay local. The shared
	// fakeRunner in newApp only retains the most recent factory host,
	// which is not enough here because Delete switches runners mid-flow.
	var hosts []string
	r := &fakeRunner{replies: []reply{
		{stdout: "demo\n"},       // ps -a
		{stdout: ""},             // cat /proc/mounts
		{},                       // ps — not running
		{},                       // rm
		{},                       // untag
		{err: &exec.ExitError{}}, // git remote get-url — absent
	}}
	a := app.NewWith("/home/op", &stubKeys{key: "k"}, func(host string) app.CommandRunner {
		hosts = append(hosts, host)
		r.host = host
		return r
	})

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		Remote:       "user@host",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var sawRemote, sawLocal bool
	for _, h := range hosts {
		switch h {
		case "user@host":
			sawRemote = true
		case "":
			sawLocal = true
		default:
			t.Errorf("unexpected factory host %q", h)
		}
	}
	if !sawRemote {
		t.Errorf("orchestrator runner should have been requested with the remote host; hosts=%v", hosts)
	}
	if !sawLocal {
		t.Errorf(
			"a local runner should have been requested for the mount-table read and git remote cleanup; hosts=%v",
			hosts,
		)
	}
}

func TestDelete_RemovesLocalGitRemote(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"}, // ps -a
		reply{stdout: ""},       // cat /proc/mounts
		reply{},                 // ps — not running
		reply{},                 // rm
		reply{},                 // untag
		reply{stdout: "ssh://user@localhost:33000/home/user/source\n"}, // git remote get-url — present
		reply{}, // git remote remove
	)

	var out bytes.Buffer
	err := a.Delete(context.Background(), &out, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := len(fr.calls); got != 7 {
		t.Fatalf("expected 7 runner calls (ps-a, mounts, ps, rm, untag, get-url, remove), got %d: %+v",
			got, fr.calls)
	}
	if fr.calls[5].cmd != "git remote get-url 'codebox-demo'" {
		t.Errorf("call[5] should be git remote get-url, got %q", fr.calls[5].cmd)
	}
	if fr.calls[6].cmd != "git remote remove 'codebox-demo'" {
		t.Errorf("call[6] should be git remote remove, got %q", fr.calls[6].cmd)
	}
	if !strings.Contains(out.String(), `Removing local git remote "codebox-demo"`) {
		t.Errorf("expected git remote removal progress line, got:\n%s", out.String())
	}
}

func TestDelete_GitRemoteRemoveFailureSurfaced(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: ""}, // cat /proc/mounts
		reply{},
		reply{},
		reply{},
		reply{stdout: "ssh://...\n"}, // get-url — present
		reply{stderr: "error: could not remove config section\n", err: &exec.ExitError{}},
	)

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err == nil {
		t.Fatal("expected error when git remote remove fails")
	}
	if !strings.Contains(err.Error(), "remove git remote") ||
		!strings.Contains(err.Error(), "could not remove config section") {
		t.Errorf("error should include op + stderr; got %v", err)
	}
}

func TestDelete_StopFailureSurfacedAndRemoveSkipped(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"}, // ps -a
		reply{stdout: ""},       // cat /proc/mounts
		reply{stdout: "demo\n"}, // ps — running
		reply{stderr: "Error: container in use by something\n", err: &exec.ExitError{}}, // stop fails
	)

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err == nil {
		t.Fatal("expected error when stop fails")
	}
	if !strings.Contains(err.Error(), "stop container") || !strings.Contains(err.Error(), "container in use") {
		t.Errorf("stop error should include op + stderr; got %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Errorf("rm/untag must not run when stop fails; got %d calls", got)
	}
}

func TestDelete_RemoveFailureSurfacedAndUntagSkipped(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"}, // ps -a
		reply{stdout: ""},       // cat /proc/mounts
		reply{},                 // ps — not running
		reply{stderr: "Error: container is in use\n", err: &exec.ExitError{}}, // rm fails
	)

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err == nil {
		t.Fatal("expected error when rm fails")
	}
	if !strings.Contains(err.Error(), "remove container") || !strings.Contains(err.Error(), "in use") {
		t.Errorf("remove error should include op + stderr; got %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Errorf("untag must not run when rm fails; got %d calls", got)
	}
}

func TestDelete_UntagImageNotKnown_ProceedsToRemote(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"}, // ps -a
		reply{stdout: ""},       // cat /proc/mounts
		reply{},                 // ps — not running
		reply{},                 // rm
		reply{stderr: "Error: image not known\n", err: &exec.ExitError{}}, // untag — image already gone
		reply{err: &exec.ExitError{}},                                     // git remote get-url — absent
	)

	var out bytes.Buffer
	err := a.Delete(context.Background(), &out, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("a missing image should be tolerated, got: %v", err)
	}
	if got := len(fr.calls); got != 6 {
		t.Fatalf("expected 6 calls (untag failure must not abort before git remote), got %d: %+v", got, fr.calls)
	}
	if !strings.Contains(fr.calls[5].cmd, "git remote get-url 'codebox-demo'") {
		t.Errorf("call[5] should attempt git remote cleanup, got %q", fr.calls[5].cmd)
	}
	if !strings.Contains(out.String(), "already gone") {
		t.Errorf("expected a skip-untag note, got:\n%s", out.String())
	}
}

func TestDelete_UntagFailureSurfaced(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"}, // ps -a
		reply{stdout: ""},       // cat /proc/mounts
		reply{},                 // ps — not running
		reply{},                 // rm
		reply{stderr: "Error: image is in use by a container\n", err: &exec.ExitError{}}, // untag fails
	)

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err == nil {
		t.Fatal("expected error when untag fails")
	}
	if !strings.Contains(err.Error(), "untag image") || !strings.Contains(err.Error(), "in use") {
		t.Errorf("untag error should include op + stderr; got %v", err)
	}
	// A genuine untag failure must abort before touching the git remote.
	if got := len(fr.calls); got != 5 {
		t.Fatalf("expected 5 calls (abort at untag), got %d: %+v", got, fr.calls)
	}
}

func TestDelete_SSHConnectErrorSurfacedDirectly(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{err: &runner.ConnectError{Host: "u@h", Err: errors.New("ssh: no auth")}},
	)

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		Remote:       "u@h",
	})
	var ce *runner.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("Delete should pass ConnectError through; got %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "u@h") {
		t.Errorf("ConnectError should name the host; got %q", err.Error())
	}
}

func TestDelete_RejectsUnknownOrchestrator(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "containerd",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported orchestrator") {
		t.Fatalf("expected unsupported orchestrator error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked when orchestrator is invalid")
	}
}

// TestDelete_AutoUnmountsActiveSshfsMount pins the contract that
// `codebox delete` finds and tears down any sshfs mounts whose source
// column is `codebox-<instance>` before stopping the container. The
// fusermount call happens between the mount-table read and the
// running-check so the operator never ends up with a mount pointing at
// a defunct sshd.
func TestDelete_AutoUnmountsActiveSshfsMount(t *testing.T) {
	t.Parallel()
	procMounts := "codebox-demo /home/op/work/.codebox/demo fuse.sshfs " +
		"rw,nosuid,nodev,relatime,user_id=1000,group_id=1000 0 0\n"
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},       // ps -a — exists
		reply{stdout: procMounts},     // cat /proc/mounts — one codebox mount
		reply{},                       // fusermount -u
		reply{stdout: ""},             // ps — not running
		reply{},                       // rm
		reply{},                       // untag
		reply{err: &exec.ExitError{}}, // git remote get-url — absent
	)

	var out bytes.Buffer
	err := a.Delete(context.Background(), &out, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := len(fr.calls); got != 7 {
		t.Fatalf("expected 7 calls (ps-a, mounts, fusermount, ps, rm, untag, git get-url), got %d: %+v",
			got, fr.calls)
	}
	if !strings.Contains(fr.calls[2].cmd, "fusermount -u '/home/op/work/.codebox/demo'") {
		t.Errorf("call[2] should be fusermount -u for the codebox-demo mount, got %q",
			fr.calls[2].cmd)
	}
	if !strings.Contains(out.String(), "Unmounting /home/op/work/.codebox/demo") {
		t.Errorf("operator should see the unmount progress line, got:\n%s", out.String())
	}
}

// TestDelete_AutoUnmountRemovesEmptyMountDir pins that the same
// "remove if empty" cleanup applied by `codebox umount` runs for
// every mount the delete-driven teardown tears down. A populated
// mount target is left in place; an empty one disappears.
func TestDelete_AutoUnmountRemovesEmptyMountDir(t *testing.T) {
	cwd := t.TempDir()
	emptyTarget := filepath.Join(cwd, "empty")
	populatedTarget := filepath.Join(cwd, "populated")
	for _, d := range []string{emptyTarget, populatedTarget} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(populatedTarget, "leftover.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write leftover: %v", err)
	}
	procMounts := "codebox-demo " + emptyTarget + " fuse.sshfs rw 0 0\n" +
		"codebox-demo " + populatedTarget + " fuse.sshfs rw 0 0\n"
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: procMounts},
		reply{}, // fusermount empty
		reply{}, // fusermount populated
		reply{stdout: ""},
		reply{},
		reply{},
		reply{err: &exec.ExitError{}}, // git remote get-url — absent
	)

	var out bytes.Buffer
	err := a.Delete(context.Background(), &out, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, statErr := os.Stat(emptyTarget); !os.IsNotExist(statErr) {
		t.Errorf("empty mount target should be removed; stat err = %v", statErr)
	}
	if _, statErr := os.Stat(populatedTarget); statErr != nil {
		t.Errorf("populated mount target must be left in place; stat err = %v", statErr)
	}
	if !strings.Contains(out.String(), "Removed empty "+emptyTarget) {
		t.Errorf("operator should see Removed line for empty dir, got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Removed empty "+populatedTarget) {
		t.Errorf("operator should not see Removed line for populated dir, got:\n%s", out.String())
	}
}

// TestDelete_IgnoresUnrelatedMounts guards that mounts whose source is
// not `codebox-<instance>` are never touched, even when they share the
// instance's name elsewhere in the line.
func TestDelete_IgnoresUnrelatedMounts(t *testing.T) {
	t.Parallel()
	procMounts := "tmpfs /tmp tmpfs rw 0 0\n" +
		"codebox-other /home/op/.codebox/other fuse.sshfs rw 0 0\n" +
		"/dev/sda1 /mnt/demo ext4 rw 0 0\n"
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},       // ps -a
		reply{stdout: procMounts},     // cat /proc/mounts
		reply{},                       // ps — not running
		reply{},                       // rm
		reply{},                       // untag
		reply{err: &exec.ExitError{}}, // git remote get-url — absent
	)

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := len(fr.calls); got != 6 {
		t.Fatalf("expected 6 calls (no fusermount), got %d: %+v", got, fr.calls)
	}
	for i, c := range fr.calls {
		if strings.Contains(c.cmd, "fusermount") {
			t.Errorf("call[%d] should not be fusermount; got %q", i, c.cmd)
		}
	}
}

func TestDelete_RejectsInvalidInstanceName(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "bad name",
		Orchestrator: "podman",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("expected invalid character error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked for invalid name")
	}
}
