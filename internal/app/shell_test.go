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
		reply{stdout: ""},                // tmux label — unset (disabled)
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
	if got := len(fr.calls); got != 4 {
		t.Fatalf("expected 4 runner calls (ps -a, tmux label, port, ssh), got %d: %+v", got, fr.calls)
	}
	if !strings.Contains(fr.calls[0].cmd, "podman ps -a --format") {
		t.Errorf("call[0] should be ps -a, got %q", fr.calls[0].cmd)
	}
	if !strings.Contains(fr.calls[1].cmd, `podman inspect 'demo' --format`) {
		t.Errorf("call[1] should be the tmux label lookup, got %q", fr.calls[1].cmd)
	}
	if !strings.Contains(fr.calls[2].cmd, "podman port 'demo' 2222") {
		t.Errorf("call[2] should be port lookup, got %q", fr.calls[2].cmd)
	}

	wantSSH := "ssh -t -o StrictHostKeyChecking=no -p 33000 user@localhost " +
		"'cd ~/source 2>/dev/null; exec ${SHELL:-/bin/sh} -l'"
	if fr.calls[3].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[3].cmd, wantSSH)
	}
	// The interactive ssh runs locally, never tunnelled through --remote.
	if fr.calls[3].host != "" {
		t.Errorf("interactive ssh should run on the local host (host=\"\"), got %q",
			fr.calls[3].host)
	}
}

// TestShell_TmuxLabelLaunchesTmux pins that when the instance carries
// the tmux=true label the interactive ssh launches tmux with a
// horizontal split, rooted at ~/source.
func TestShell_TmuxLabelLaunchesTmux(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},          // ps -a — exists
		reply{stdout: "true\n"},          // tmux label — enabled
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{},                          // local ssh exec
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "podman"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	wantSSH := "ssh -t -o StrictHostKeyChecking=no -p 33000 user@localhost " +
		`'cd ~/source 2>/dev/null; tmux attach -t main 2>/dev/null || exec tmux new-session -s main \; split-window -h'`
	if fr.calls[3].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[3].cmd, wantSSH)
	}
}

// TestShell_TmuxWithAgentRunsAgentOnRight pins that when the instance
// carries both tmux=true and an agent label, the right-hand tmux pane
// runs that agent through a login shell (so its install dir is on PATH).
func TestShell_TmuxWithAgentRunsAgentOnRight(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},           // ps -a — exists
		reply{stdout: "true\ntrue\n\n\n"}, // labels: tmux=true, claude=true, codex/opencode unset
		reply{stdout: "0.0.0.0:33000\n"},  // port lookup
		reply{},                           // local ssh exec
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "podman"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	wantSSH := "ssh -t -o StrictHostKeyChecking=no -p 33000 user@localhost " +
		`'cd ~/source 2>/dev/null; tmux attach -t main 2>/dev/null || exec tmux new-session -s main \; split-window -h "$SHELL -lc claude"'`
	if fr.calls[3].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[3].cmd, wantSSH)
	}
}

// TestShell_AgentLabelNotTrueIgnored pins that an agent label whose
// value is anything other than "true" does not enable the agent — the
// right pane falls back to a plain split.
func TestShell_AgentLabelNotTrueIgnored(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "true\ngarbage\n\n\n"}, // claude label present but not "true"
		reply{stdout: "0.0.0.0:33000\n"},
		reply{},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "podman"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	wantSSH := "ssh -t -o StrictHostKeyChecking=no -p 33000 user@localhost " +
		`'cd ~/source 2>/dev/null; tmux attach -t main 2>/dev/null || exec tmux new-session -s main \; split-window -h'`
	if fr.calls[3].cmd != wantSSH {
		t.Errorf("non-true agent label should be ignored:\n got: %q\nwant: %q", fr.calls[3].cmd, wantSSH)
	}
}

// TestShell_OpencodeAgentRunsOnRight pins that a non-claude agent label
// (here opencode), when it is the only agent installed, runs in the
// right-hand tmux pane — the label key doubles as the command.
func TestShell_OpencodeAgentRunsOnRight(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "true\n\n\ntrue\n"}, // tmux=true, only opencode set
		reply{stdout: "0.0.0.0:33000\n"},
		reply{},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "podman"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	wantSSH := "ssh -t -o StrictHostKeyChecking=no -p 33000 user@localhost " +
		`'cd ~/source 2>/dev/null; tmux attach -t main 2>/dev/null || exec tmux new-session -s main \; split-window -h "$SHELL -lc opencode"'`
	if fr.calls[3].cmd != wantSSH {
		t.Errorf("opencode should run on the right:\n got: %q\nwant: %q", fr.calls[3].cmd, wantSSH)
	}
}

// TestShell_CodexAgentRunsOnRight pins that the codex agent label, when
// it is the only agent installed, runs in the right-hand tmux pane.
func TestShell_CodexAgentRunsOnRight(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "true\n\ntrue\n\n"}, // tmux=true, only codex set
		reply{stdout: "0.0.0.0:33000\n"},
		reply{},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "podman"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	wantSSH := "ssh -t -o StrictHostKeyChecking=no -p 33000 user@localhost " +
		`'cd ~/source 2>/dev/null; tmux attach -t main 2>/dev/null || exec tmux new-session -s main \; split-window -h "$SHELL -lc codex"'`
	if fr.calls[3].cmd != wantSSH {
		t.Errorf("codex should run on the right:\n got: %q\nwant: %q", fr.calls[3].cmd, wantSSH)
	}
}

// TestShell_MultipleAgentsRunNeither pins that when more than one agent
// label is set, codebox cannot choose for the operator, so it launches
// tmux with two plain panes and runs no agent.
func TestShell_MultipleAgentsRunNeither(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "true\ntrue\ntrue\ntrue\n"}, // tmux + all three agents
		reply{stdout: "0.0.0.0:33000\n"},
		reply{},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{Instance: "demo", Orchestrator: "podman"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	wantSSH := "ssh -t -o StrictHostKeyChecking=no -p 33000 user@localhost " +
		`'cd ~/source 2>/dev/null; tmux attach -t main 2>/dev/null || exec tmux new-session -s main \; split-window -h'`
	if fr.calls[3].cmd != wantSSH {
		t.Errorf("multiple agents should launch neither:\n got: %q\nwant: %q", fr.calls[3].cmd, wantSSH)
	}
}

func TestShell_RemoteAddsJumpAndLooksUpPortViaSSH(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: ""}, // tmux label — disabled
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
	if fr.calls[2].host != "ops@bastion" {
		t.Errorf("port lookup should target the remote host, got %q", fr.calls[2].host)
	}
	// …but the interactive ssh always runs locally with -J.
	if fr.calls[3].host != "" {
		t.Errorf("interactive ssh should run locally, got host %q", fr.calls[3].host)
	}
	wantSSH := "ssh -t -o StrictHostKeyChecking=no -J 'ops@bastion' -p 44000 user@localhost " +
		"'cd ~/source 2>/dev/null; exec ${SHELL:-/bin/sh} -l'"
	if fr.calls[3].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[3].cmd, wantSSH)
	}
}

func TestShell_InstanceKeyExpandsHomeAndAddsDashI(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: ""}, // tmux label — disabled
		reply{stdout: "[::]:33000\n"},
		reply{},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			InstanceKeys: []string{"~/.ssh/id_ed25519"},
		})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	wantSSH := "ssh -t -o StrictHostKeyChecking=no -i '/home/op/.ssh/id_ed25519' -p 33000 user@localhost " +
		"'cd ~/source 2>/dev/null; exec ${SHELL:-/bin/sh} -l'"
	if fr.calls[3].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[3].cmd, wantSSH)
	}
}

// TestShell_MultipleInstanceKeysAddDashIEach pins that every
// --instance-key becomes its own `-i` on the inner ssh, so whichever
// machine's private key is present locally can authenticate.
func TestShell_MultipleInstanceKeysAddDashIEach(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: ""}, // tmux label — disabled
		reply{stdout: "[::]:33000\n"},
		reply{},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			InstanceKeys: []string{"~/.ssh/id_ed25519", "/keys/desktop"},
		})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	got := fr.calls[3].cmd
	if !strings.Contains(got, "-i '/home/op/.ssh/id_ed25519' -i '/keys/desktop'") {
		t.Errorf("ssh command should carry one -i per key:\n got: %q", got)
	}
}

func TestShell_PortForwardsAddedAsLocalhostL(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: ""}, // tmux label — disabled
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
	got := fr.calls[3].cmd
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
		reply{stdout: ""}, // tmux label — disabled
		reply{stdout: "0.0.0.0:55000\n"},
		reply{},
	)

	err := a.Shell(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.ShellRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Remote:       "ops@bastion",
			InstanceKeys: []string{"/keys/id_rsa"},
			Ports:        []string{"8000:3000"},
		})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	wantSSH := "ssh -t -o StrictHostKeyChecking=no " +
		"-i '/keys/id_rsa' " +
		"-L '8000:localhost:3000' " +
		"-J 'ops@bastion' " +
		"-p 55000 " +
		"user@localhost " +
		"'cd ~/source 2>/dev/null; exec ${SHELL:-/bin/sh} -l'"
	if fr.calls[3].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[3].cmd, wantSSH)
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
		reply{stdout: ""}, // tmux label — disabled
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
		reply{stdout: ""}, // tmux label — disabled
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
		reply{stdout: ""}, // tmux label — disabled
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
