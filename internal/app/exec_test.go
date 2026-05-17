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

func TestExec_HappyPath_Local(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\nother\n"},   // ps -a — exists
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{stdout: "hello world\n"},   // ssh exec output
	)

	stdin := bytes.NewBufferString("piped-stdin")
	var stdout, stderr bytes.Buffer
	err := a.Exec(context.Background(), stdin, &stdout, &stderr, app.ExecRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		Command:      "echo",
		Args:         []string{"hello", "world"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
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

	wantSSH := "ssh -o StrictHostKeyChecking=no user@localhost -p 33000 " +
		`''\''echo'\'' '\''hello'\'' '\''world'\'''`
	if fr.calls[2].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[2].cmd, wantSSH)
	}
	if fr.calls[2].host != "" {
		t.Errorf("exec ssh should run on the local host (host=\"\"), got %q", fr.calls[2].host)
	}
	if fr.calls[2].stdin != "piped-stdin" {
		t.Errorf("stdin should be forwarded to ssh; got %q", fr.calls[2].stdin)
	}
	if stdout.String() != "hello world\n" {
		t.Errorf("stdout should be forwarded; got %q", stdout.String())
	}
}

func TestExec_RemoteAddsJumpAndLooksUpPortViaSSH(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:44000\n"},
		reply{},
	)

	err := a.Exec(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ExecRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Remote:       "ops@bastion",
			Command:      "ls",
		})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if fr.calls[1].host != "ops@bastion" {
		t.Errorf("port lookup should target the remote host, got %q", fr.calls[1].host)
	}
	if fr.calls[2].host != "" {
		t.Errorf("exec ssh should run locally, got host %q", fr.calls[2].host)
	}
	if !strings.Contains(fr.calls[2].cmd, "-J 'ops@bastion'") {
		t.Errorf("ssh command should include -J jump:\n got: %q", fr.calls[2].cmd)
	}
}

func TestExec_InstanceKeyExpandsHomeAndAddsDashI(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{},
	)

	err := a.Exec(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ExecRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			InstanceKey:  "~/.ssh/id_ed25519",
			Command:      "ls",
		})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(fr.calls[2].cmd, "-i '/home/op/.ssh/id_ed25519'") {
		t.Errorf("expected -i with expanded path; got %q", fr.calls[2].cmd)
	}
}

// TestExec_InstanceKeyOnlyOnInnerSSH guards the "do not use
// instance-key for sending command" requirement: the orchestrator-side
// lookups must never reference the key path. The container-bound ssh
// is the only place it should appear.
func TestExec_InstanceKeyOnlyOnInnerSSH(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{},
	)

	err := a.Exec(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ExecRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Remote:       "ops@bastion",
			InstanceKey:  "/keys/id_rsa",
			Command:      "ls",
		})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	for i, c := range fr.calls[:2] {
		if strings.Contains(c.cmd, "id_rsa") {
			t.Errorf("call[%d] %q must not reference the instance key", i, c.cmd)
		}
	}
	if !strings.Contains(fr.calls[2].cmd, "-i '/keys/id_rsa'") {
		t.Errorf("inner ssh should pass -i instance-key; got %q", fr.calls[2].cmd)
	}
}

// TestExec_QuotesArgumentsForRemoteShell ensures arguments containing
// spaces and shell metacharacters survive the ssh hop intact. The
// expected ssh command is asserted via a round-trip through `sh -c`
// (which is what the local runner uses), so the inner argv arriving on
// the container side matches the operator's input.
func TestExec_QuotesArgumentsForRemoteShell(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{},
	)

	err := a.Exec(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ExecRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Command:      "cat",
			Args:         []string{"my file.txt", "a'b"},
		})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	sshCmd := fr.calls[2].cmd
	// Parse what `sh -c <sshCmd>` would feed to ssh, then concatenate
	// the trailing remote-command args (ssh joins them with spaces) and
	// re-tokenise the result the way the remote login shell would —
	// that's the argv the inner program actually sees.
	argv := shellTokenize(t, sshCmd)
	dest := indexOf(argv, "user@localhost")
	if dest < 0 {
		t.Fatalf("ssh argv missing destination: %v", argv)
	}
	// Skip dest, -p, PORT
	remoteCmd := strings.Join(argv[dest+3:], " ")
	innerArgv := shellTokenize(t, remoteCmd)
	wantArgv := []string{"cat", "my file.txt", "a'b"}
	if !equalSlices(innerArgv, wantArgv) {
		t.Errorf("inner argv mismatch:\n got: %q\nwant: %q\n raw ssh cmd: %q",
			innerArgv, wantArgv, sshCmd)
	}
}

// shellTokenize asks /bin/sh to split s the way it would on `sh -c s`,
// returning one argv element per slice entry. NUL is used as the
// separator so embedded newlines survive intact.
func shellTokenize(t *testing.T, s string) []string {
	t.Helper()
	script := `set -- ` + s + `; for a in "$@"; do printf '%s\0' "$a"; done`
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("shell tokenize %q: %v", s, err)
	}
	trimmed := strings.TrimRight(string(out), "\x00")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\x00")
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestExec_NotFound(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "other\nanother\n"},
	)

	err := a.Exec(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ExecRequest{Instance: "demo", Orchestrator: "podman", Command: "ls"})
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

func TestExec_PortNotPublished(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: ""},
	)

	err := a.Exec(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ExecRequest{Instance: "demo", Orchestrator: "podman", Command: "ls"})
	if err == nil {
		t.Fatal("expected error when no host port is exposed")
	}
	if !strings.Contains(err.Error(), "not exposing port") {
		t.Errorf("error should explain missing port mapping, got %v", err)
	}
}

func TestExec_SSHConnectErrorOnRemoteLookupSurfaced(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{err: &runner.ConnectError{Host: "u@h", Err: errors.New("ssh: no auth")}},
	)
	err := a.Exec(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ExecRequest{Instance: "demo", Orchestrator: "podman", Remote: "u@h", Command: "ls"})
	var ce *runner.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("Exec should pass ConnectError through; got %T %v", err, err)
	}
}

func TestExec_InnerExitStatusPropagated(t *testing.T) {
	t.Parallel()
	exitErr := &exec.ExitError{}
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{err: exitErr},
	)
	err := a.Exec(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ExecRequest{Instance: "demo", Orchestrator: "podman", Command: "false"})
	if err == nil {
		t.Fatal("expected non-nil error when inner command exits non-zero")
	}
}

func TestExec_RejectsUnknownOrchestrator(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Exec(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ExecRequest{Instance: "demo", Orchestrator: "containerd", Command: "ls"})
	if err == nil || !strings.Contains(err.Error(), "unsupported orchestrator") {
		t.Fatalf("expected unsupported orchestrator error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked when orchestrator is invalid")
	}
}

func TestExec_RejectsInvalidInstanceName(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Exec(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ExecRequest{Instance: "bad name", Orchestrator: "podman", Command: "ls"})
	if err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("expected invalid character error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked for invalid name")
	}
}

func TestExec_RejectsEmptyCommand(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Exec(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ExecRequest{Instance: "demo", Orchestrator: "podman"})
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("expected command-required error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked when command is empty")
	}
}
