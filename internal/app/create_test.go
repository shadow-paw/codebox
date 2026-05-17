package app_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"codebox/internal/adapters/runner"
	"codebox/internal/app"
)

// stubKeys returns a fixed public key.
type stubKeys struct {
	key string
	err error
	got string
}

func (s *stubKeys) Resolve(keyPath string) (string, error) {
	s.got = keyPath
	return s.key, s.err
}

// recordedCall captures one CommandRunner.Run invocation.
type recordedCall struct {
	host  string
	cmd   string
	stdin string
}

// fakeRunner is a recording CommandRunner. Each Run consumes one entry
// from `replies` (or nothing, leaving stdout/err empty and a nil error
// when the slice is shorter than the number of calls).
type fakeRunner struct {
	host    string
	replies []reply
	calls   []recordedCall
	idx     int
}

type reply struct {
	stdout string
	stderr string
	err    error
}

func (f *fakeRunner) Run(_ context.Context, shellCmd string,
	stdin io.Reader, stdout, stderr io.Writer,
) error {
	in := ""
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		in = string(b)
	}
	f.calls = append(f.calls, recordedCall{host: f.host, cmd: shellCmd, stdin: in})

	var r reply
	if f.idx < len(f.replies) {
		r = f.replies[f.idx]
	}
	f.idx++
	if r.stdout != "" {
		_, _ = stdout.Write([]byte(r.stdout))
	}
	if r.stderr != "" {
		_, _ = stderr.Write([]byte(r.stderr))
	}
	return r.err
}

// newApp builds an app with the supplied stub key and a single
// fakeRunner reused for every host the use-case asks for. The
// returned *fakeRunner is the recorder; tests inspect its calls.
func newApp(t *testing.T, keys *stubKeys, replies ...reply) (*app.App, *fakeRunner) {
	t.Helper()
	r := &fakeRunner{replies: replies}
	a := app.NewWith("/home/op", keys, func(host string) app.CommandRunner {
		r.host = host
		return r
	})
	return a, r
}

func TestCreate_HappyPath_Local(t *testing.T) {
	t.Parallel()
	keys := &stubKeys{key: "ssh-ed25519 AAAA test"}
	a, fr := newApp(t, keys,
		reply{stdout: "other\n"},  // ps -a — no collision
		reply{},                   // build — succeeds, no output
		reply{stdout: "abc123\n"}, // run — container id
	)

	var out bytes.Buffer
	err := a.Create(context.Background(), &out, app.CreateRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		OS:           "debian_13",
		InstanceKey:  "~/.ssh/id_ed25519",
	})
	if err != nil {
		t.Fatalf("Create: %v\nout:\n%s", err, out.String())
	}

	if got := len(fr.calls); got != 3 {
		t.Fatalf("expected 3 runner calls, got %d: %+v", got, fr.calls)
	}
	if !strings.Contains(fr.calls[0].cmd, "podman ps -a") {
		t.Errorf("call[0] should be ps -a, got %q", fr.calls[0].cmd)
	}
	if !strings.Contains(fr.calls[1].cmd, "podman build") || !strings.Contains(fr.calls[1].cmd, "-f -") {
		t.Errorf("call[1] should be `podman build ... -f -`, got %q", fr.calls[1].cmd)
	}
	if !strings.HasPrefix(fr.calls[1].stdin, "# syntax=docker/dockerfile") {
		t.Errorf("call[1] stdin should be the Dockerfile, got first chars %q", firstLine(fr.calls[1].stdin))
	}
	if !strings.Contains(
		fr.calls[2].cmd,
		"podman run -d --name 'demo' --hostname 'demo' --label codebox=true --publish-all",
	) {
		t.Errorf("call[2] should run the container, got %q", fr.calls[2].cmd)
	}

	if !strings.Contains(out.String(), `Instance "demo" is ready`) {
		t.Errorf("success line missing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "  codebox shell demo --instance-key=~/.ssh/id_ed25519") {
		t.Errorf("success hint missing or wrong:\n%s", out.String())
	}
	for _, want := range []string{
		"──────── Dockerfile ─", // labelled top rule
		"──────────────────────────────────────", // closing rule fragment
		"# syntax=docker/dockerfile:1.7",
		"FROM docker.io/debian:13.4",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q\n%s", want, out.String())
		}
	}
	top := strings.Index(out.String(), "──────── Dockerfile ")
	from := strings.Index(out.String(), "FROM docker.io/debian:13.4")
	build := strings.Index(out.String(), `Building image "demo"`)
	if top >= from || from >= build {
		t.Errorf("expected separator above Dockerfile, then build line:\n%s", out.String())
	}
	// The closing rule should appear AFTER the Dockerfile content but
	// BEFORE the build line — i.e. the block is fully closed before
	// the engine's output starts.
	closeIdx := strings.LastIndex(out.String()[:build], "─────────────────────")
	if closeIdx <= from {
		t.Errorf("closing rule should sit between Dockerfile and build line:\n%s", out.String())
	}

	// ~ expansion should have happened before the resolver was called.
	if keys.got != "/home/op/.ssh/id_ed25519" {
		t.Errorf("resolver received %q, want expanded %q", keys.got, "/home/op/.ssh/id_ed25519")
	}
}

func TestCreate_HappyPath_RemoteUsesSSH(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: ""},
		reply{},
		reply{},
	)

	var out bytes.Buffer
	err := a.Create(context.Background(), &out, app.CreateRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		OS:           "debian_13",
		Remote:       "user@host",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if fr.host != "user@host" {
		t.Fatalf("runner factory should have been called with the remote host; got %q", fr.host)
	}
	if !strings.Contains(out.String(), "  codebox shell demo --remote=user@host") {
		t.Errorf("success hint should include --remote:\n%s", out.String())
	}
}

func TestCreate_RebuildPassesNoCache(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: ""}, reply{}, reply{},
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13", Rebuild: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(fr.calls[1].cmd, "--no-cache") {
		t.Errorf("--rebuild should pass --no-cache; build cmd: %q", fr.calls[1].cmd)
	}
}

func TestCreate_AlreadyExistsSurfacesDeleteHint(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\nother\n"},
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "docker", OS: "debian_13", Remote: "u@h",
	})
	if err == nil {
		t.Fatal("expected error when instance exists")
	}
	want := `instance "demo" already exists; stop and delete it first:` +
		"\n  codebox delete demo --orchestrator=docker --remote=u@h"
	if err.Error() != want {
		t.Errorf("error mismatch:\n got: %q\nwant: %q", err.Error(), want)
	}
}

func TestCreate_RejectsUnknownOrchestrator(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "containerd", OS: "debian_13",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported orchestrator") {
		t.Fatalf("expected unsupported orchestrator error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked when orchestrator is invalid")
	}
}

func TestCreate_RejectsUnknownOS(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "plan9",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported os") {
		t.Fatalf("expected unsupported os error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked when OS is invalid")
	}
}

func TestCreate_PropagatesKeyResolverError(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	a, _ := newApp(t, &stubKeys{err: want})

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want wrap of %v", err, want)
	}
}

func TestCreate_BuildFailureReportedAndRunSkipped(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: ""},
		reply{err: &exec.ExitError{}},
	)

	var out bytes.Buffer
	err := a.Create(context.Background(), &out, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
	})
	if err == nil {
		t.Fatal("expected error from build failure")
	}
	if !strings.Contains(err.Error(), "image build") {
		t.Errorf("error should mention image build, got %v", err)
	}
	if len(fr.calls) != 2 {
		t.Errorf("run should NOT be called when build fails; got %d calls", len(fr.calls))
	}
}

func TestCreate_SSHConnectErrorSurfacedDirectly(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{err: &runner.ConnectError{Host: "u@h", Err: errors.New("ssh: no auth")}},
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13", Remote: "u@h",
	})
	var ce *runner.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("Create should pass ConnectError through; got %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "u@h") {
		t.Errorf("ConnectError should name the host; got %q", err.Error())
	}
}

func TestCreate_RunFailureUsesStderr(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: ""},
		reply{},
		reply{stderr: "Error: name already in use\n", err: &exec.ExitError{}},
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
	})
	if err == nil {
		t.Fatal("expected error from run failure")
	}
	if !strings.Contains(err.Error(), "start container") || !strings.Contains(err.Error(), "name already in use") {
		t.Errorf("run error should include op + stderr; got %v", err)
	}
}

func TestCreate_RejectsInvalidInstanceName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label, instance, wantSub string
	}{
		{"empty", "", "required"},
		{"space", "bad name", "invalid character"},
		{"dot", "bad.name", "invalid character"},
		{"slash", "bad/name", "invalid character"},
		{"multibyte rune", "héllo", "invalid character"},
		{"too long (33 chars)", "abcdefghijklmnopqrstuvwxyz0123456", "too long"},
		{"exclamation", "demo!", "invalid character"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			a, fr := newApp(t, &stubKeys{key: "k"})
			err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
				Instance:     tc.instance,
				Orchestrator: "podman",
				OS:           "debian_13",
			})
			if err == nil {
				t.Fatalf("expected error for %q", tc.instance)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q should contain %q", err.Error(), tc.wantSub)
			}
			if len(fr.calls) != 0 {
				t.Errorf("runner should not be invoked for invalid name; got %d calls", len(fr.calls))
			}
		})
	}
}

func TestCreate_AcceptsValidInstanceName(t *testing.T) {
	t.Parallel()
	cases := []string{
		"a",
		"demo",
		"x_y-Z9",
		"abcdefghijklmnopqrstuvwxyz012345", // exactly the 32-char cap
		"_leading",
		"-leading",
	}
	for _, name := range cases {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a, _ := newApp(t,
				&stubKeys{key: "k"},
				reply{stdout: "other\n"},
				reply{},
				reply{},
			)
			err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
				Instance:     name,
				Orchestrator: "podman",
				OS:           "debian_13",
			})
			if err != nil {
				t.Fatalf("Create rejected valid name %q: %v", name, err)
			}
		})
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i != -1 {
		return s[:i]
	}
	return s
}
