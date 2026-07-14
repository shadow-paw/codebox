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

func TestPush_HappyPath_Local(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\nother\n"},   // ps -a — exists
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{},                          // rsync — succeeds
	)

	var stdout, stderr bytes.Buffer
	err := a.Push(context.Background(), &stdout, &stderr, app.PushRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		LocalPath:    "./payload",
		InstancePath: "/workspace/in",
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := len(fr.calls); got != 3 {
		t.Fatalf("expected 3 runner calls (ps -a, port, rsync), got %d: %+v", got, fr.calls)
	}
	if !strings.Contains(fr.calls[0].cmd, "podman ps -a --format") {
		t.Errorf("call[0] should be ps -a, got %q", fr.calls[0].cmd)
	}
	if !strings.Contains(fr.calls[1].cmd, "podman port 'demo' 2222") {
		t.Errorf("call[1] should be port lookup, got %q", fr.calls[1].cmd)
	}

	wantRsync := "rsync --verbose --archive --compress --update --progress " +
		"-e 'ssh -o StrictHostKeyChecking=no -p 33000' " +
		"'./payload' 'user@localhost:/workspace/in'"
	if fr.calls[2].cmd != wantRsync {
		t.Errorf("rsync command mismatch:\n got: %q\nwant: %q", fr.calls[2].cmd, wantRsync)
	}
	if fr.calls[2].host != "" {
		t.Errorf("rsync should run on the local host (host=\"\"), got %q", fr.calls[2].host)
	}

	// The rsync command must be echoed to stdout bracketed by separators.
	for _, want := range []string{
		"──────── rsync ─",
		wantRsync,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q\n%s", want, stdout.String())
		}
	}
	top := strings.Index(stdout.String(), "──────── rsync ")
	cmdAt := strings.Index(stdout.String(), wantRsync)
	if top < 0 || cmdAt < 0 || top >= cmdAt {
		t.Errorf("expected separator above rsync command:\n%s", stdout.String())
	}
}

func TestPush_RemoteAddsJumpAndLooksUpPortViaSSH(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:44000\n"},
		reply{},
	)

	err := a.Push(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.PushRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Remote:       "ops@bastion",
			LocalPath:    "./payload",
			InstancePath: "/in",
		})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if fr.calls[1].host != "ops@bastion" {
		t.Errorf("port lookup should target the remote host, got %q", fr.calls[1].host)
	}
	if fr.calls[2].host != "" {
		t.Errorf("rsync should run locally, got host %q", fr.calls[2].host)
	}
	if !strings.Contains(fr.calls[2].cmd, `-J '\''ops@bastion'\''`) {
		t.Errorf("rsync ssh transport should include -J ProxyJump:\n got: %q",
			fr.calls[2].cmd)
	}
}

// TestPush_InstanceKeyOnlyOnInnerSSH guards the contract that the key
// path is only embedded inside rsync's `-e ssh ...` value; the
// orchestrator-side lookups must never reference it.
func TestPush_InstanceKeyOnlyOnInnerSSH(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{},
	)

	err := a.Push(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.PushRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Remote:       "ops@bastion",
			InstanceKeys: []string{"/keys/id_rsa"},
			LocalPath:    "./payload",
			InstancePath: "/in",
		})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	for i, c := range fr.calls[:2] {
		if strings.Contains(c.cmd, "id_rsa") {
			t.Errorf("call[%d] %q must not reference the instance key", i, c.cmd)
		}
	}
	if !strings.Contains(fr.calls[2].cmd, `-i '\''/keys/id_rsa'\''`) {
		t.Errorf("rsync ssh transport should embed -i instance-key; got %q",
			fr.calls[2].cmd)
	}
}

func TestPush_InstanceKeyExpandsHome(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{},
	)

	err := a.Push(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.PushRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			InstanceKeys: []string{"~/.ssh/id_ed25519"},
			LocalPath:    "~/payload",
			InstancePath: "/in",
		})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !strings.Contains(fr.calls[2].cmd, `-i '\''/home/op/.ssh/id_ed25519'\''`) {
		t.Errorf("expected ~-expanded instance key inside ssh transport; got %q",
			fr.calls[2].cmd)
	}
	if !strings.Contains(fr.calls[2].cmd, "'/home/op/payload'") {
		t.Errorf("expected ~-expanded local path; got %q", fr.calls[2].cmd)
	}
}

func TestPush_NotFound(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "other\nanother\n"},
	)

	err := a.Push(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.PushRequest{
			Instance: "demo", Orchestrator: "podman",
			LocalPath: "./x", InstancePath: "/y",
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

func TestPush_PortNotPublished(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: ""},
	)

	err := a.Push(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.PushRequest{
			Instance: "demo", Orchestrator: "podman",
			LocalPath: "./x", InstancePath: "/y",
		})
	if err == nil {
		t.Fatal("expected error when no host port is exposed")
	}
	if !strings.Contains(err.Error(), "not exposing port") {
		t.Errorf("error should explain missing port mapping, got %v", err)
	}
}

func TestPush_SSHConnectErrorOnRemoteLookupSurfaced(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{err: &runner.ConnectError{Host: "u@h", Err: errors.New("ssh: no auth")}},
	)
	err := a.Push(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.PushRequest{
			Instance: "demo", Orchestrator: "podman", Remote: "u@h",
			LocalPath: "./x", InstancePath: "/y",
		})
	var ce *runner.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("Push should pass ConnectError through; got %T %v", err, err)
	}
}

func TestPush_RsyncExitStatusPropagated(t *testing.T) {
	t.Parallel()
	exitErr := &exec.ExitError{}
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{err: exitErr},
	)
	err := a.Push(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.PushRequest{
			Instance: "demo", Orchestrator: "podman",
			LocalPath: "./x", InstancePath: "/y",
		})
	if err == nil {
		t.Fatal("expected non-nil error when rsync exits non-zero")
	}
}

func TestPush_RejectsUnknownOrchestrator(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Push(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.PushRequest{
			Instance: "demo", Orchestrator: "containerd",
			LocalPath: "./x", InstancePath: "/y",
		})
	if err == nil || !strings.Contains(err.Error(), "unsupported orchestrator") {
		t.Fatalf("expected unsupported orchestrator error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked when orchestrator is invalid")
	}
}

func TestPush_RejectsInvalidInstanceName(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Push(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.PushRequest{
			Instance: "bad name", Orchestrator: "podman",
			LocalPath: "./x", InstancePath: "/y",
		})
	if err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("expected invalid character error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked for invalid name")
	}
}

func TestPush_RejectsMissingPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label, want string
		req         app.PushRequest
	}{
		{
			"missing local-path",
			"--local-path is required",
			app.PushRequest{Instance: "demo", Orchestrator: "podman", InstancePath: "/y"},
		},
		{
			"missing instance-path",
			"--instance-path is required",
			app.PushRequest{Instance: "demo", Orchestrator: "podman", LocalPath: "./x"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			a, fr := newApp(t, &stubKeys{key: "k"})
			err := a.Push(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
			if len(fr.calls) != 0 {
				t.Errorf("runner should not be invoked when paths are missing")
			}
		})
	}
}

func TestPull_HappyPath_Local(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{},
	)

	var stdout, stderr bytes.Buffer
	err := a.Pull(context.Background(), &stdout, &stderr, app.PullRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		InstancePath: "/workspace/out",
		LocalPath:    "./results",
	})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}

	wantRsync := "rsync --verbose --archive --compress --update --progress " +
		"-e 'ssh -o StrictHostKeyChecking=no -p 33000' " +
		"'user@localhost:/workspace/out' './results'"
	if fr.calls[2].cmd != wantRsync {
		t.Errorf("rsync command mismatch:\n got: %q\nwant: %q", fr.calls[2].cmd, wantRsync)
	}
}

func TestPull_RemoteWithKey(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:55000\n"},
		reply{},
	)

	err := a.Pull(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.PullRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Remote:       "ops@bastion",
			InstanceKeys: []string{"/keys/id_rsa"},
			InstancePath: "/workspace/out",
			LocalPath:    "./results",
		})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	wantRsync := "rsync --verbose --archive --compress --update --progress " +
		`-e 'ssh -o StrictHostKeyChecking=no -i '\''/keys/id_rsa'\'' ` +
		`-J '\''ops@bastion'\'' -p 55000' ` +
		"'user@localhost:/workspace/out' './results'"
	if fr.calls[2].cmd != wantRsync {
		t.Errorf("rsync command mismatch:\n got: %q\nwant: %q", fr.calls[2].cmd, wantRsync)
	}
}

func TestPull_NotFound(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "other\n"},
	)

	err := a.Pull(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.PullRequest{
			Instance: "demo", Orchestrator: "podman",
			InstancePath: "/y", LocalPath: "./x",
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

func TestPull_RejectsMissingPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label, want string
		req         app.PullRequest
	}{
		{
			"missing instance-path",
			"--instance-path is required",
			app.PullRequest{Instance: "demo", Orchestrator: "podman", LocalPath: "./x"},
		},
		{
			"missing local-path",
			"--local-path is required",
			app.PullRequest{Instance: "demo", Orchestrator: "podman", InstancePath: "/y"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			a, fr := newApp(t, &stubKeys{key: "k"})
			err := a.Pull(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
			if len(fr.calls) != 0 {
				t.Errorf("runner should not be invoked when paths are missing")
			}
		})
	}
}
