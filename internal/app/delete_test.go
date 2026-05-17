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

func TestDelete_HappyPath_StopRemoveUntag(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\nother\n"},                     // ps -a — exists
		reply{stdout: "demo\n"},                            // ps — running
		reply{stdout: "demo\n"},                            // stop — podman echoes name
		reply{stdout: "demo\n"},                            // rm — podman echoes name
		reply{stdout: "untagged: localhost/demo:latest\n"}, // untag chatter
	)

	var out bytes.Buffer
	err := a.Delete(context.Background(), &out, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("Delete: %v\nout:\n%s", err, out.String())
	}

	if got := len(fr.calls); got != 5 {
		t.Fatalf("expected 5 runner calls (ps-a, ps, stop, rm, untag), got %d: %+v", got, fr.calls)
	}
	wantSubstrings := []string{
		"podman ps -a --format",
		"podman ps --format",
		"podman stop 'demo'",
		"podman rm 'demo'",
		"podman untag 'demo'",
	}
	for i, want := range wantSubstrings {
		if !strings.Contains(fr.calls[i].cmd, want) {
			t.Errorf("call[%d] should contain %q, got %q", i, want, fr.calls[i].cmd)
		}
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
		reply{stdout: "demo\n"}, // ps -a — exists
		reply{stdout: "\n"},     // ps — nothing running
		reply{stdout: "demo\n"}, // rm
		reply{},                 // untag
	)

	var out bytes.Buffer
	err := a.Delete(context.Background(), &out, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Fatalf("expected 4 runner calls (no stop), got %d: %+v", got, fr.calls)
	}
	if strings.Contains(out.String(), "Stopping") {
		t.Errorf("Stopping progress line should be omitted for a stopped container:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `Deleting container "demo"`) {
		t.Errorf("expected deleting progress line, got:\n%s", out.String())
	}
	if !strings.Contains(fr.calls[2].cmd, "podman rm 'demo'") {
		t.Errorf("call[2] should be rm, got %q", fr.calls[2].cmd)
	}
	if !strings.Contains(fr.calls[3].cmd, "podman untag 'demo'") {
		t.Errorf("call[3] should be untag, got %q", fr.calls[3].cmd)
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
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"}, // ps -a
		reply{},                 // ps — not running
		reply{},                 // rm
		reply{},                 // untag
	)

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		Remote:       "user@host",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if fr.host != "user@host" {
		t.Fatalf("runner factory should have been called with the remote host; got %q", fr.host)
	}
}

func TestDelete_StopFailureSurfacedAndRemoveSkipped(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"}, // ps -a
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
	if got := len(fr.calls); got != 3 {
		t.Errorf("rm/untag must not run when stop fails; got %d calls", got)
	}
}

func TestDelete_RemoveFailureSurfacedAndUntagSkipped(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"}, // ps -a
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
	if got := len(fr.calls); got != 3 {
		t.Errorf("untag must not run when rm fails; got %d calls", got)
	}
}

func TestDelete_UntagFailureSurfaced(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"}, // ps -a
		reply{},                 // ps — not running
		reply{},                 // rm
		reply{stderr: "Error: image not known\n", err: &exec.ExitError{}}, // untag fails
	)

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err == nil {
		t.Fatal("expected error when untag fails")
	}
	if !strings.Contains(err.Error(), "untag image") || !strings.Contains(err.Error(), "not known") {
		t.Errorf("untag error should include op + stderr; got %v", err)
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
