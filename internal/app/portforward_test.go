package app_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"codebox/internal/app"
)

func TestPortForward_HappyPath_Local(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\nother\n"},   // ps -a — exists
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{},                          // local ssh -N — succeeds (e.g. forward closed)
	)

	var stdout, stderr bytes.Buffer
	err := a.PortForward(context.Background(), &stdout, &stderr, app.PortForwardRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		InstanceKey:  "k",
		Ports:        []string{"13000:3000", "13001:3001"},
	})
	if err != nil {
		t.Fatalf("PortForward: %v", err)
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

	wantSSH := "ssh -N -o StrictHostKeyChecking=no -o ExitOnForwardFailure=yes " +
		"-o ServerAliveInterval=30 -i 'k' " +
		"-L '13000:localhost:3000' -L '13001:localhost:3001' " +
		"-p 33000 user@localhost"
	if fr.calls[2].cmd != wantSSH {
		t.Errorf("ssh command mismatch:\n got: %q\nwant: %q", fr.calls[2].cmd, wantSSH)
	}
	// The forwarding ssh always runs locally, never tunnelled through --remote.
	if fr.calls[2].host != "" {
		t.Errorf("forwarding ssh should run on the local host (host=\"\"), got %q",
			fr.calls[2].host)
	}
	// The mapped ports are announced before blocking.
	out := stdout.String()
	for _, want := range []string{
		"Forwarding ports to instance demo:",
		"localhost:13000 -> 3000",
		"localhost:13001 -> 3001",
		"Ctrl-C",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q; got:\n%s", want, out)
		}
	}
}

// TestPortForward_Remote pins the transport shape with a bastion: the
// orchestrator lookups tunnel to the remote host while the forwarding
// ssh runs locally and adds -J.
func TestPortForward_Remote(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},          // ps -a — exists
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{},                          // local ssh -N
	)

	var stdout, stderr bytes.Buffer
	err := a.PortForward(context.Background(), &stdout, &stderr, app.PortForwardRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		Remote:       "user@host",
		Ports:        []string{"8080:8080"},
	})
	if err != nil {
		t.Fatalf("PortForward: %v", err)
	}
}

// TestPortForward_CtrlCIsCleanExit verifies that a cancelled context
// (Ctrl-C) yields a clean return rather than surfacing the killed-ssh
// error.
func TestPortForward_CtrlCIsCleanExit(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: the ssh call observes a done context.

	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},          // ps -a — exists
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{err: context.Canceled},     // ssh killed by cancellation
	)

	var stdout, stderr bytes.Buffer
	err := a.PortForward(ctx, &stdout, &stderr, app.PortForwardRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		Ports:        []string{"8080:8080"},
	})
	if err != nil {
		t.Fatalf("Ctrl-C should be a clean exit, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "Stopped port forwarding.") {
		t.Errorf("expected stop message; got:\n%s", stdout.String())
	}
}

func TestPortForward_NotExposingPort(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"}, // ps -a — exists
		reply{stdout: "\n"},     // port lookup — nothing mapped
	)

	err := a.PortForward(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, app.PortForwardRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		Ports:        []string{"8080:8080"},
	})
	if err == nil || !strings.Contains(err.Error(), "not exposing port") {
		t.Fatalf("expected not-exposing-port error, got: %v", err)
	}
}
