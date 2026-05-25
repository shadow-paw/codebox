package app_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codebox/internal/app"
)

func TestMount_HappyPath_Local(t *testing.T) {
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "/usr/bin/sshfs\n"}, // command -v sshfs
		reply{stdout: ""},                 // cat /proc/mounts — not yet mounted
		reply{stdout: "demo\n"},           // ps -a — exists
		reply{stdout: "0.0.0.0:33000\n"},  // port lookup
		reply{},                           // ssh mkdir ~/source on instance
		reply{},                           // sshfs
	)

	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	err := a.Mount(context.Background(), &stdout, &stderr, app.MountRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	wantDir := filepath.Join(cwd, ".codebox", "demo")
	if _, err := os.Stat(wantDir); err != nil {
		t.Errorf("default mount dir should have been created at %q: %v", wantDir, err)
	}

	if got := len(fr.calls); got != 6 {
		t.Fatalf("expected 6 runner calls, got %d: %+v", got, fr.calls)
	}
	if !strings.Contains(fr.calls[0].cmd, "command -v sshfs") {
		t.Errorf("call[0] should probe sshfs, got %q", fr.calls[0].cmd)
	}
	if !strings.Contains(fr.calls[1].cmd, "cat /proc/mounts") {
		t.Errorf("call[1] should read /proc/mounts, got %q", fr.calls[1].cmd)
	}
	if !strings.Contains(fr.calls[4].cmd, "mkdir -p ~/source") {
		t.Errorf("call[4] should create ~/source on the instance, got %q", fr.calls[4].cmd)
	}
	if !strings.Contains(fr.calls[5].cmd, "sshfs ") ||
		!strings.Contains(fr.calls[5].cmd, "'user@localhost:/home/user/source'") ||
		!strings.Contains(fr.calls[5].cmd, "-p 33000") ||
		!strings.Contains(fr.calls[5].cmd, "fsname='codebox-demo'") {
		t.Errorf("call[5] should be a sshfs mount with port/fsname, got %q", fr.calls[5].cmd)
	}
	if !strings.Contains(stdout.String(), "──────── sshfs ") {
		t.Errorf("operator should see the sshfs block, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Mounted instance \"demo\"") {
		t.Errorf("operator should see the success line, got:\n%s", stdout.String())
	}
}

func TestMount_DefaultsToDotCodeboxUnderCwd(t *testing.T) {
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "/usr/bin/sshfs\n"},
		reply{stdout: ""},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:1\n"},
		reply{},
		reply{},
	)
	cwd := t.TempDir()
	t.Chdir(cwd)
	err := a.Mount(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.MountRequest{Instance: "demo", Orchestrator: "podman"})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	want := filepath.Join(cwd, ".codebox", "demo")
	if !strings.Contains(fr.calls[5].cmd, "'"+want+"'") {
		t.Errorf("sshfs command should mount at default %q, got %q", want, fr.calls[5].cmd)
	}
}

func TestMount_ExplicitLocalDirHonoured(t *testing.T) {
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "/usr/bin/sshfs\n"},
		reply{stdout: ""},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:1\n"},
		reply{},
		reply{},
	)
	cwd := t.TempDir()
	t.Chdir(cwd)
	dir := filepath.Join(cwd, "mnt-here")
	err := a.Mount(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.MountRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			LocalDir:     dir,
		})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if !strings.Contains(fr.calls[5].cmd, "'"+dir+"'") {
		t.Errorf("sshfs command should mount at supplied path %q, got %q", dir, fr.calls[5].cmd)
	}
}

func TestMount_RemoteAddsProxyJumpAndKey(t *testing.T) {
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "/usr/bin/sshfs\n"},
		reply{stdout: ""},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:55000\n"},
		reply{},
		reply{},
	)
	t.Chdir(t.TempDir())
	err := a.Mount(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.MountRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Remote:       "ops@bastion",
			InstanceKey:  "/keys/id_rsa",
		})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	sshfs := fr.calls[5].cmd
	if !strings.Contains(sshfs, "IdentityFile='/keys/id_rsa'") {
		t.Errorf("sshfs should embed IdentityFile, got %q", sshfs)
	}
	if !strings.Contains(sshfs, "ProxyJump='ops@bastion'") {
		t.Errorf("sshfs should embed ProxyJump, got %q", sshfs)
	}
}

func TestMount_RejectsWhenSshfsMissing(t *testing.T) {
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{err: &exec.ExitError{}}, // command -v sshfs — fails
	)
	t.Chdir(t.TempDir())
	err := a.Mount(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.MountRequest{Instance: "demo", Orchestrator: "podman"})
	if err == nil || !strings.Contains(err.Error(), "sshfs is not installed") {
		t.Fatalf("expected sshfs-missing error, got %v", err)
	}
	if len(fr.calls) != 1 {
		t.Errorf("no further work should happen when sshfs is missing; got %d calls", len(fr.calls))
	}
}

func TestMount_RejectsWhenAlreadyMounted(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	mountTarget := filepath.Join(cwd, ".codebox", "demo")
	procMounts := "codebox-demo " + mountTarget + " fuse.sshfs rw 0 0\n"
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "/usr/bin/sshfs\n"},
		reply{stdout: procMounts},
	)
	err := a.Mount(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.MountRequest{Instance: "demo", Orchestrator: "podman"})
	if err == nil || !strings.Contains(err.Error(), "already a mount point") {
		t.Fatalf("expected already-mounted error, got %v", err)
	}
	if len(fr.calls) != 2 {
		t.Errorf("no orchestrator work should happen when already mounted; got %d calls", len(fr.calls))
	}
}

func TestMount_RejectsWhenInstanceMissing(t *testing.T) {
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "/usr/bin/sshfs\n"},
		reply{stdout: ""},
		reply{stdout: "other\n"}, // ps -a — no demo
	)
	t.Chdir(t.TempDir())
	err := a.Mount(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.MountRequest{Instance: "demo", Orchestrator: "podman"})
	if err == nil || !strings.Contains(err.Error(), `instance "demo" not found`) {
		t.Fatalf("expected not-found error, got %v", err)
	}
	if got := len(fr.calls); got != 3 {
		t.Errorf("no further work after existence check; got %d calls", got)
	}
}

func TestMount_RejectsWhenInstanceNotRunning(t *testing.T) {
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "/usr/bin/sshfs\n"},
		reply{stdout: ""},
		reply{stdout: "demo\n"},
		reply{stdout: ""}, // port — empty, container stopped
	)
	t.Chdir(t.TempDir())
	err := a.Mount(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.MountRequest{Instance: "demo", Orchestrator: "podman"})
	if err == nil || !strings.Contains(err.Error(), "not exposing port") {
		t.Fatalf("expected not-running error, got %v", err)
	}
}

func TestMount_PermissionDeniedSurfaced(t *testing.T) {
	a, _ := newApp(
		t,
		&stubKeys{key: "k"},
		reply{stdout: "/usr/bin/sshfs\n"},
		reply{stdout: ""},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:1\n"},
		reply{}, // ssh mkdir succeeds
		reply{
			stderr: "read: Connection reset by peer\nPermission denied (publickey).\n",
			err:    &exec.ExitError{},
		}, // sshfs
	)
	t.Chdir(t.TempDir())
	err := a.Mount(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.MountRequest{Instance: "demo", Orchestrator: "podman"})
	if err == nil {
		t.Fatal("expected error from sshfs failure")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error should call out permission denied; got %v", err)
	}
}

func TestUnmount_HappyPath(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	mountTarget := filepath.Join(cwd, ".codebox", "demo")
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{}, // fusermount -u
	)
	var stdout bytes.Buffer
	err := a.Unmount(context.Background(), &stdout, &bytes.Buffer{},
		app.UnmountRequest{Instance: "demo", Orchestrator: "podman"})
	if err != nil {
		t.Fatalf("Unmount: %v", err)
	}
	if got := len(fr.calls); got != 1 {
		t.Fatalf("expected only the fusermount call, got %d: %+v", got, fr.calls)
	}
	if !strings.Contains(fr.calls[0].cmd, "fusermount -u '"+mountTarget+"'") {
		t.Errorf("expected fusermount -u call, got %q", fr.calls[0].cmd)
	}
	if !strings.Contains(stdout.String(), "Unmounted") {
		t.Errorf("operator should see the success line, got:\n%s", stdout.String())
	}
}

func TestUnmount_RemovesEmptyMountDir(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	mountTarget := filepath.Join(cwd, ".codebox", "demo")
	if err := os.MkdirAll(mountTarget, 0o755); err != nil {
		t.Fatalf("mkdir mount target: %v", err)
	}
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{}, // fusermount -u
	)
	var stdout bytes.Buffer
	err := a.Unmount(context.Background(), &stdout, &bytes.Buffer{},
		app.UnmountRequest{Instance: "demo", Orchestrator: "podman"})
	if err != nil {
		t.Fatalf("Unmount: %v", err)
	}
	if _, statErr := os.Stat(mountTarget); !os.IsNotExist(statErr) {
		t.Errorf("expected mount target to be removed, stat err = %v", statErr)
	}
	if !strings.Contains(stdout.String(), "Removed empty "+mountTarget) {
		t.Errorf("operator should see the removed line, got:\n%s", stdout.String())
	}
}

func TestUnmount_LeavesNonEmptyMountDir(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	mountTarget := filepath.Join(cwd, ".codebox", "demo")
	if err := os.MkdirAll(mountTarget, 0o755); err != nil {
		t.Fatalf("mkdir mount target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mountTarget, "leftover.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write leftover: %v", err)
	}
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{}, // fusermount -u
	)
	var stdout bytes.Buffer
	err := a.Unmount(context.Background(), &stdout, &bytes.Buffer{},
		app.UnmountRequest{Instance: "demo", Orchestrator: "podman"})
	if err != nil {
		t.Fatalf("Unmount: %v", err)
	}
	if _, statErr := os.Stat(mountTarget); statErr != nil {
		t.Errorf("non-empty mount target must be left in place; stat err = %v", statErr)
	}
	if strings.Contains(stdout.String(), "Removed empty") {
		t.Errorf("operator should not see a Removed line for non-empty dir, got:\n%s", stdout.String())
	}
}

// TestUnmount_SurfacesFusermountError pins that without a pre-check
// against /proc/mounts, fusermount's own stderr is what reaches the
// operator when the path is not actually a mount.
func TestUnmount_SurfacesFusermountError(t *testing.T) {
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{
			stderr: "fusermount: entry for /tmp/foo not found in /etc/mtab\n",
			err:    &exec.ExitError{},
		},
	)
	t.Chdir(t.TempDir())
	err := a.Unmount(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.UnmountRequest{Instance: "demo", Orchestrator: "podman"})
	if err == nil || !strings.Contains(err.Error(), "not found in /etc/mtab") {
		t.Fatalf("expected fusermount stderr to surface, got %v", err)
	}
	if len(fr.calls) != 1 {
		t.Errorf("expected only the fusermount call, got %d", len(fr.calls))
	}
	if !strings.Contains(fr.calls[0].cmd, "fusermount -u") {
		t.Errorf("call[0] should be fusermount -u, got %q", fr.calls[0].cmd)
	}
}

func TestMount_RejectsUnknownOrchestrator(t *testing.T) {
	a, fr := newApp(t, &stubKeys{key: "k"})
	t.Chdir(t.TempDir())
	err := a.Mount(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.MountRequest{Instance: "demo", Orchestrator: "containerd"})
	if err == nil || !strings.Contains(err.Error(), "unsupported orchestrator") {
		t.Fatalf("expected unsupported orchestrator error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked when orchestrator is invalid")
	}
}

func TestMount_RejectsInvalidInstanceName(t *testing.T) {
	a, fr := newApp(t, &stubKeys{key: "k"})
	t.Chdir(t.TempDir())
	err := a.Mount(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.MountRequest{Instance: "bad name", Orchestrator: "podman"})
	if err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("expected invalid character error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked for invalid name")
	}
}
