package runner_test

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"codebox/internal/adapters/runner"
)

func TestExecArgs_LocalUsesShDashC(t *testing.T) {
	t.Parallel()
	name, args := runner.Local().ExecArgs("echo hi")
	if name != "sh" || len(args) != 2 || args[0] != "-c" || args[1] != "echo hi" {
		t.Fatalf("Local exec args = %q %q", name, args)
	}
}

func TestExecArgs_SSHWrapsHostAndCommand(t *testing.T) {
	t.Parallel()
	name, args := runner.SSH("user@host").ExecArgs(`podman ps -a`)
	if name != "ssh" || len(args) != 2 || args[0] != "user@host" || args[1] != "podman ps -a" {
		t.Fatalf("SSH exec args = %q %q", name, args)
	}
}

// TestRun_Local_StdoutCapturedAndExitOK exercises the local exec path
// end-to-end via a builtin shell echo.
func TestRun_Local_StdoutCapturedAndExitOK(t *testing.T) {
	t.Parallel()
	var so, se bytes.Buffer
	err := runner.Local().Run(context.Background(), "echo hello", nil, &so, &se)
	if err != nil {
		t.Fatalf("Run: %v\nstderr=%s", err, se.String())
	}
	if got := strings.TrimSpace(so.String()); got != "hello" {
		t.Errorf("stdout = %q, want %q", got, "hello")
	}
}

// TestRun_Local_StdinPipedThrough verifies the stdin pipe is wired
// through — the Dockerfile flow relies on this.
func TestRun_Local_StdinPipedThrough(t *testing.T) {
	t.Parallel()
	var so bytes.Buffer
	err := runner.Local().Run(context.Background(), "cat", strings.NewReader("payload"), &so, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if so.String() != "payload" {
		t.Fatalf("stdin not piped through; stdout = %q", so.String())
	}
}

func TestRun_Local_NonZeroExitReturnsExitError(t *testing.T) {
	t.Parallel()
	err := runner.Local().Run(context.Background(), "exit 3", nil, nil, nil)
	if err == nil {
		t.Fatal("Run should error when shell exits non-zero")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 3 {
		t.Errorf("exit code = %d, want 3", exitErr.ExitCode())
	}
}

// TestRun_LocalDoesNotProduceConnectError ensures the 255 detection
// only kicks in for the SSH variant — a local script exiting 255 is
// just a regular exit error.
func TestRun_LocalDoesNotProduceConnectError(t *testing.T) {
	t.Parallel()
	err := runner.Local().Run(context.Background(), "exit 255", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var ce *runner.ConnectError
	if errors.As(err, &ce) {
		t.Fatalf("local exit 255 must NOT be a ConnectError: %v", err)
	}
}

// TestConnectError_UnwrapToExitErr lets callers reach the underlying
// exec.ExitError via errors.As for inspection or test assertions.
func TestConnectError_UnwrapToExitErr(t *testing.T) {
	t.Parallel()
	inner := &exec.ExitError{}
	ce := &runner.ConnectError{Host: "h", Err: inner}
	if !errors.Is(ce, inner) {
		t.Fatal("errors.Is should reach inner ExitError")
	}
	if !strings.Contains(ce.Error(), "h") {
		t.Errorf("Error() should include host; got %q", ce.Error())
	}
}
