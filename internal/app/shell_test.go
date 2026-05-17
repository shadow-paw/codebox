package app_test

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"codebox/internal/adapters/runner"
	"codebox/internal/app"
)

func TestShell_HappyPath_Local(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\nother\n"},   // ps -a — exists
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{},                          // local ssh exec — succeeds
	)

	var stdin bytes.Buffer
	var stdout, stderr bytes.Buffer
	err := a.Shell(context.Background(), &stdin, &stdout, &stderr, app.ShellRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if got := len(fr.calls); got != 3 {
		t.Fatalf("expected 3 runner calls (ps -a, port, ssh), got %d: %+v", got, fr.calls)
	}
	if !strings.Contains(fr.calls[0].cmd, "podman ps -a --format") {
		t.Errorf("call[0] should be ps -a, got %q", fr.calls[0].cmd)
	}
	if !strings.Contains(fr.calls[1].cmd, "podman port 'demo' 2222") {
		t.Errorf("call[1] should be port lookup, got %q", fr.calls[1].cmd)
	}

	wantSSH := "ssh -o StrictHostKeyChecking=no user@localhost -p 33000"
	if fr.calls[2].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[2].cmd, wantSSH)
	}
	// The interactive ssh runs locally, never tunnelled through --remote.
	if fr.calls[2].host != "" {
		t.Errorf("interactive ssh should run on the local host (host=\"\"), got %q",
			fr.calls[2].host)
	}
}

func TestShell_RemoteAddsJumpAndLooksUpPortViaSSH(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:44000\n"},
		reply{},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Remote:       "ops@bastion",
		})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	// Port lookup must hit the remote host…
	if fr.calls[1].host != "ops@bastion" {
		t.Errorf("port lookup should target the remote host, got %q", fr.calls[1].host)
	}
	// …but the interactive ssh always runs locally with -J.
	if fr.calls[2].host != "" {
		t.Errorf("interactive ssh should run locally, got host %q", fr.calls[2].host)
	}
	wantSSH := "ssh -o StrictHostKeyChecking=no -J 'ops@bastion' user@localhost -p 44000"
	if fr.calls[2].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[2].cmd, wantSSH)
	}
}

func TestShell_InstanceKeyExpandsHomeAndAddsDashI(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "[::]:33000\n"},
		reply{},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			InstanceKey:  "~/.ssh/id_ed25519",
		})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	wantSSH := "ssh -o StrictHostKeyChecking=no -i '/home/op/.ssh/id_ed25519' user@localhost -p 33000"
	if fr.calls[2].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[2].cmd, wantSSH)
	}
}

func TestShell_PortForwardsAddedAsLocalhostL(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Ports:        []string{"8000:3000", "8001:3001"},
		})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	got := fr.calls[2].cmd
	for _, want := range []string{
		"-L '8000:localhost:3000'",
		"-L '8001:localhost:3001'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ssh command missing %q\n got: %q", want, got)
		}
	}
	// Forwards must come before the destination so `-L` is read as an
	// option, not a positional argument.
	for _, fwd := range []string{"-L '8000:localhost:3000'", "-L '8001:localhost:3001'"} {
		if strings.Index(got, fwd) > strings.Index(got, "user@localhost") {
			t.Errorf("forward %q must appear before destination\n got: %q", fwd, got)
		}
	}
}

func TestShell_CombinesKeyJumpAndForwards(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:55000\n"},
		reply{},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Remote:       "ops@bastion",
			InstanceKey:  "/keys/id_rsa",
			Ports:        []string{"8000:3000"},
		})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	wantSSH := "ssh -o StrictHostKeyChecking=no " +
		"-i '/keys/id_rsa' " +
		"-L '8000:localhost:3000' " +
		"-J 'ops@bastion' " +
		"user@localhost -p 55000"
	if fr.calls[2].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[2].cmd, wantSSH)
	}
}

func TestShell_NotFound(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "other\nanother\n"},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "podman"})
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

func TestShell_PortNotPublished(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: ""}, // port lookup empty (container stopped)
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "podman"})
	if err == nil {
		t.Fatal("expected error when no host port is exposed")
	}
	if !strings.Contains(err.Error(), "not exposing port") {
		t.Errorf("error should explain missing port mapping, got %v", err)
	}
}

func TestShell_PortLookupFailureSurfacesStderr(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stderr: "Error: no such container\n", err: &exec.ExitError{}},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "podman"})
	if err == nil {
		t.Fatal("expected error when port lookup fails")
	}
	if !strings.Contains(err.Error(), "look up host port") ||
		!strings.Contains(err.Error(), "no such container") {
		t.Errorf("error should include op + stderr; got %v", err)
	}
}

func TestShell_SSHConnectErrorOnRemoteLookupSurfaced(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{err: &runner.ConnectError{Host: "u@h", Err: errors.New("ssh: no auth")}},
	)
	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "podman", Remote: "u@h"})
	var ce *runner.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("Shell should pass ConnectError through; got %T %v", err, err)
	}
}

func TestShell_InteractiveExitStatusPropagated(t *testing.T) {
	t.Parallel()
	exitErr := &exec.ExitError{}
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{err: exitErr}, // user logs out with non-zero status
	)
	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "podman"})
	if err == nil {
		t.Fatal("expected non-nil error when ssh exits non-zero")
	}
}

func TestShell_RejectsUnknownOrchestrator(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "containerd"})
	if err == nil || !strings.Contains(err.Error(), "unsupported orchestrator") {
		t.Fatalf("expected unsupported orchestrator error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked when orchestrator is invalid")
	}
}

func TestShell_RejectsInvalidInstanceName(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "bad name", Orchestrator: "podman"})
	if err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("expected invalid character error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked for invalid name")
	}
}
