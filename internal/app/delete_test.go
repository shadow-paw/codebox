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

	if got := len(fr.calls); got != 6 {
		t.Fatalf("expected 6 runner calls (ps-a, ps, stop, rm, untag, git get-url), got %d: %+v", got, fr.calls)
	}
	wantSubstrings := []string{
		"podman ps -a --format",
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
	if got := len(fr.calls); got != 5 {
		t.Fatalf("expected 5 runner calls (no stop), got %d: %+v", got, fr.calls)
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
		reply{stdout: "demo\n"},       // ps -a
		reply{},                       // ps — not running
		reply{},                       // rm
		reply{},                       // untag
		reply{err: &exec.ExitError{}}, // git remote get-url — absent
	)

	err := a.Delete(context.Background(), &bytes.Buffer{}, app.DeleteRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		Remote:       "user@host",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Orchestrator calls go to the remote host; the trailing git
	// remote cleanup is always local, regardless of --remote.
	for i, c := range fr.calls[:4] {
		if c.host != "user@host" {
			t.Errorf("call[%d] should target the orchestrator host, got %q", i, c.host)
		}
	}
	if fr.calls[4].host != "" {
		t.Errorf("git remote cleanup must run locally, got host %q", fr.calls[4].host)
	}
}

func TestDelete_RemovesLocalGitRemote(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},                                 // ps -a
		reply{},                                                 // ps — not running
		reply{},                                                 // rm
		reply{},                                                 // untag
		reply{stdout: "ssh://user@localhost:33000/home/user/source\n"}, // git remote get-url — present
		reply{},                                                 // git remote remove
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
		t.Fatalf("expected 6 runner calls (ps-a, ps, rm, untag, get-url, remove), got %d: %+v",
			got, fr.calls)
	}
	if fr.calls[4].cmd != "git remote get-url 'codebox-demo'" {
		t.Errorf("call[4] should be git remote get-url, got %q", fr.calls[4].cmd)
	}
	if fr.calls[5].cmd != "git remote remove 'codebox-demo'" {
		t.Errorf("call[5] should be git remote remove, got %q", fr.calls[5].cmd)
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
