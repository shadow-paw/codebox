package app_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"codebox/internal/adapters/runner"
	"codebox/internal/app"
)

// createdAt renders a timestamp the way both podman and docker do for
// `{{.CreatedAt}}` (time.Time.String()). The use-case layer parses
// this format to compute ages.
func createdAt(t time.Time) string {
	return t.Format("2006-01-02 15:04:05.999999999 -0700 MST")
}

func TestList_Empty_PrintsMessage(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: ""},
	)

	var out bytes.Buffer
	err := a.List(context.Background(), &out, app.ListRequest{Orchestrator: "podman"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "No codebox instances found." {
		t.Errorf("empty-state message mismatch:\n got: %q\nwant: %q",
			got, "No codebox instances found.")
	}
	if got := len(fr.calls); got != 1 {
		t.Fatalf("expected single ps -a call, got %d", got)
	}
	if !strings.Contains(fr.calls[0].cmd,
		"podman ps -a --filter label=codebox=true --format") {
		t.Errorf("list query mismatch: %q", fr.calls[0].cmd)
	}
}

func TestList_RendersTable_Local(t *testing.T) {
	t.Parallel()
	now := time.Now()
	stdout := fmt.Sprintf(
		"demo|%s|0.0.0.0:33000->2222/tcp\nstale|%s|\n",
		createdAt(now.Add(-3*time.Hour-time.Minute)),
		createdAt(now.Add(-49*time.Hour)),
	)
	a, _ := newApp(t, &stubKeys{key: "k"}, reply{stdout: stdout})

	var out bytes.Buffer
	if err := a.List(context.Background(), &out, app.ListRequest{Orchestrator: "podman"}); err != nil {
		t.Fatalf("List: %v", err)
	}
	body := out.String()
	for _, want := range []string{
		"INSTANCE", "AGE", "SSH COMMAND",
		"demo", "3 hr",
		"ssh -o StrictHostKeyChecking=no user@localhost -p 33000",
		"stale", "2 day", "(stopped)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q:\n%s", want, body)
		}
	}
	// Remote-only flag must not leak into local ssh hint.
	if strings.Contains(body, "-J ") {
		t.Errorf("local ssh hint should not include -J:\n%s", body)
	}
}

func TestList_Remote_AddsJump(t *testing.T) {
	t.Parallel()
	now := time.Now()
	stdout := fmt.Sprintf("demo|%s|0.0.0.0:44000->2222/tcp\n",
		createdAt(now.Add(-2*time.Hour-time.Minute)))
	a, fr := newApp(t, &stubKeys{key: "k"}, reply{stdout: stdout})

	var out bytes.Buffer
	err := a.List(context.Background(), &out, app.ListRequest{
		Orchestrator: "podman",
		Remote:       "ops@bastion",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if fr.host != "ops@bastion" {
		t.Fatalf("runner factory should have received the remote host; got %q", fr.host)
	}
	want := "ssh -o StrictHostKeyChecking=no -J ops@bastion user@localhost -p 44000"
	if !strings.Contains(out.String(), want) {
		t.Errorf("remote ssh hint missing %q:\n%s", want, out.String())
	}
}

func TestList_TrailingNewlinesAndWhitespace(t *testing.T) {
	t.Parallel()
	now := time.Now()
	stdout := "\n\n" + fmt.Sprintf("demo|%s|0.0.0.0:33000->2222/tcp\n\n\n",
		createdAt(now.Add(-5*time.Minute)))
	a, _ := newApp(t, &stubKeys{key: "k"}, reply{stdout: stdout})

	var out bytes.Buffer
	if err := a.List(context.Background(), &out, app.ListRequest{Orchestrator: "podman"}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(out.String(), "demo") || !strings.Contains(out.String(), "5 min") {
		t.Errorf("expected demo / 5 min row, got:\n%s", out.String())
	}
}

func TestList_UnparseableCreatedAt_ShowsQuestionMark(t *testing.T) {
	t.Parallel()
	stdout := "demo|not-a-timestamp|0.0.0.0:33000->2222/tcp\n"
	a, _ := newApp(t, &stubKeys{key: "k"}, reply{stdout: stdout})

	var out bytes.Buffer
	if err := a.List(context.Background(), &out, app.ListRequest{Orchestrator: "podman"}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(out.String(), "?") {
		t.Errorf("unparseable timestamp should render as ?, got:\n%s", out.String())
	}
}

func TestList_SSHConnectErrorSurfacedDirectly(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{err: &runner.ConnectError{Host: "u@h", Err: errors.New("ssh: no auth")}},
	)
	err := a.List(context.Background(), &bytes.Buffer{}, app.ListRequest{
		Orchestrator: "podman", Remote: "u@h",
	})
	var ce *runner.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("List should pass ConnectError through; got %T %v", err, err)
	}
}

func TestList_EngineFailureSurfacesStderr(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stderr: "Error: cannot connect to podman socket\n", err: &exec.ExitError{}},
	)
	err := a.List(context.Background(), &bytes.Buffer{}, app.ListRequest{Orchestrator: "podman"})
	if err == nil {
		t.Fatal("expected error from list failure")
	}
	if !strings.Contains(err.Error(), "list instances") ||
		!strings.Contains(err.Error(), "cannot connect to podman socket") {
		t.Errorf("error should include op + stderr; got %v", err)
	}
}

func TestList_RejectsUnknownOrchestrator(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})
	err := a.List(context.Background(), &bytes.Buffer{}, app.ListRequest{Orchestrator: "containerd"})
	if err == nil || !strings.Contains(err.Error(), "unsupported orchestrator") {
		t.Fatalf("expected unsupported orchestrator error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked when orchestrator is invalid")
	}
}
