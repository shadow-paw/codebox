package app_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	return newAppWithHome(t, "/home/op", keys, replies...)
}

// newAppWithHome is newApp with an explicit home directory, so tests
// that need ~ expansion to land on a real path (e.g. credentials
// transfer) can point at a t.TempDir().
func newAppWithHome(t *testing.T, home string, keys *stubKeys, replies ...reply) (*app.App, *fakeRunner) {
	t.Helper()
	r := &fakeRunner{replies: replies}
	a := app.NewWith(home, keys, func(host string) app.CommandRunner {
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
		reply{stdout: "demo\n"},   // ps — running check sees the container
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

	if got := len(fr.calls); got != 4 {
		t.Fatalf("expected 4 runner calls, got %d: %+v", got, fr.calls)
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
		"podman run -d --restart=unless-stopped --name 'demo' --hostname 'demo' --label codebox=true --publish-all",
	) {
		t.Errorf("call[2] should run the container, got %q", fr.calls[2].cmd)
	}
	if fr.calls[3].cmd != "podman ps --format '{{.Names}}'" {
		t.Errorf("call[3] should be the running-state probe, got %q", fr.calls[3].cmd)
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
		reply{stdout: "demo\n"}, // ps — running check
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
		reply{stdout: "demo\n"}, // ps — running check
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

// TestCreate_PodmanStartsWithDeviceFlagsAndMigrates pins that --podman
// starts the container with the device/capability/security-opt flags
// the nested rootless Podman needs, then runs `podman system migrate`
// inside the instance once it is up.
func TestCreate_PodmanStartsWithDeviceFlagsAndMigrates(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: ""}, reply{}, reply{},
		reply{stdout: "demo\n"}, // ps — running check
		reply{},                 // exec podman system migrate
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13", Podman: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, want := range []string{
		"--device /dev/fuse", "--device /dev/net/tun",
		"--cap-add=sys_admin", "--cap-add=net_admin", "--cap-add=mknod",
		"--security-opt label=disable", "--security-opt unmask=ALL",
	} {
		if !strings.Contains(fr.calls[2].cmd, want) {
			t.Errorf("--podman run cmd missing %q; got: %q", want, fr.calls[2].cmd)
		}
	}
	if got := len(fr.calls); got != 5 {
		t.Fatalf("expected 5 runner calls with --podman, got %d: %+v", got, fr.calls)
	}
	if !strings.Contains(fr.calls[4].cmd, "exec --user user") ||
		!strings.Contains(fr.calls[4].cmd, "podman system migrate") {
		t.Errorf("call[4] should run `podman system migrate` inside the instance, got %q", fr.calls[4].cmd)
	}
}

// TestCreate_NoPodmanSkipsDeviceFlagsAndMigrate keeps the device flags
// off the run command and skips the migrate step when --podman is unset.
func TestCreate_NoPodmanSkipsDeviceFlagsAndMigrate(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: ""}, reply{}, reply{},
		reply{stdout: "demo\n"}, // ps — running check
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if strings.Contains(fr.calls[2].cmd, "--device") || strings.Contains(fr.calls[2].cmd, "--cap-add") {
		t.Errorf("run without --podman must not add device/cap flags; run cmd: %q", fr.calls[2].cmd)
	}
	if got := len(fr.calls); got != 4 {
		t.Fatalf("expected 4 runner calls without --podman, got %d: %+v", got, fr.calls)
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
				reply{stdout: name + "\n"}, // ps — running check
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

// TestCreate_HTTPSProxyExportsInProfile threads --https-proxy through
// the use case and confirms the value lands in the in-container
// user's login profile via the Dockerfile piped into the build
// runner. The proxy must NOT become a build-time ENV directive.
func TestCreate_HTTPSProxyExportsInProfile(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "other\n"},
		reply{},
		reply{},
		reply{stdout: "demo\n"}, // ps — running check
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		HTTPSProxy: "http://proxy.corp:3128",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := `echo 'export HTTPS_PROXY="http://proxy.corp:3128"' >> /home/user/.profile`
	if !strings.Contains(fr.calls[1].stdin, want) {
		t.Fatalf("build stdin missing %q\n%s", want, fr.calls[1].stdin)
	}
	if strings.Contains(fr.calls[1].stdin, "ENV HTTPS_PROXY") {
		t.Fatalf("HTTPS_PROXY must not leak into an ENV directive:\n%s", fr.calls[1].stdin)
	}
}

// TestCreate_ClaudeCredentialsRsyncsAfterRun pins the
// --claude-credentials flow: after the container starts, the
// use-case layer looks up the host port and runs a credentials rsync
// (locally) that targets /home/user/.claude/.credentials.json with
// --mkpath + --chmod=F0600 so the file lands with the right perms in
// a directory that may not yet exist.
func TestCreate_ClaudeCredentialsRsyncsAfterRun(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", ".credentials.json"), `{"token":"x"}`)

	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},         // ps -a — no collision
		reply{},                          // build
		reply{stdout: "abc123\n"},        // run — container id
		reply{stdout: "demo\n"},          // ps — running check
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{},                          // rsync
	)

	var out bytes.Buffer
	err := a.Create(context.Background(), &out, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Claude:            true,
		ClaudeCredentials: true,
	})
	if err != nil {
		t.Fatalf("Create: %v\nout:\n%s", err, out.String())
	}

	if got := len(fr.calls); got != 6 {
		t.Fatalf("expected 6 runner calls (ps -a, build, run, ps, port, rsync), got %d:\n%+v",
			got, fr.calls)
	}
	if !strings.Contains(fr.calls[4].cmd, "podman port 'demo' 2222") {
		t.Errorf("call[4] should be port lookup, got %q", fr.calls[4].cmd)
	}
	rsync := fr.calls[5].cmd
	for _, want := range []string{
		"rsync ",
		"--mkpath",
		"--chmod=F0600",
		"-p 33000",
		filepath.Join(home, ".claude", ".credentials.json"),
		"user@localhost:/home/user/.claude/.credentials.json",
	} {
		if !strings.Contains(rsync, want) {
			t.Errorf("rsync command missing %q:\n%s", want, rsync)
		}
	}
	if fr.calls[5].host != "" {
		t.Errorf("rsync should run on the local host (host=\"\"), got %q", fr.calls[5].host)
	}
	if !strings.Contains(out.String(), "Pushing Claude credentials") {
		t.Errorf("expected a status line about pushing credentials:\n%s", out.String())
	}
}

// TestCreate_ClaudeCredentialsMissingFileFailsEarly surfaces the
// credentials file-not-found case before any orchestrator command
// runs: we'd rather fail fast than build a multi-GB image and start
// a container the operator can't use.
func TestCreate_ClaudeCredentialsMissingFileFailsEarly(t *testing.T) {
	t.Parallel()
	home := t.TempDir() // intentionally no ~/.claude/.credentials.json
	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"})

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Claude:            true,
		ClaudeCredentials: true,
	})
	if err == nil {
		t.Fatal("expected error when credentials file is absent")
	}
	if !strings.Contains(err.Error(), "--claude-credentials") {
		t.Errorf("error should name the flag, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("no orchestrator command should run when credentials file is missing; got %d calls",
			len(fr.calls))
	}
}

// TestCreate_ClaudeCredentialsIgnoredWithoutClaude pins that the
// credentials flag is silently ignored when --claude is not set: even
// with the source file present, there is no fail-fast stat error and no
// push (so the run completes with just the four base calls). The same
// gating covers --codex-credentials / --opencode-credentials.
func TestCreate_ClaudeCredentialsIgnoredWithoutClaude(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", ".credentials.json"), `{"token":"x"}`)

	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},
		reply{},
		reply{stdout: "abc123\n"},
		reply{stdout: "demo\n"}, // ps — running check
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		ClaudeCredentials: true, // but Claude not set → ignored
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Errorf("--claude-credentials without --claude must not push; got %d calls: %+v", got, fr.calls)
	}
}

// TestCreate_ClaudeCredentialsRemoteAddsJump pins the ProxyJump wiring
// on the credentials rsync: when --remote is set, the inner ssh
// transport carries `-J user@host` so the local rsync connects to the
// container's published port through the bastion.
func TestCreate_ClaudeCredentialsRemoteAddsJump(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", ".credentials.json"), `{"token":"x"}`)

	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},
		reply{},
		reply{},
		reply{stdout: "demo\n"}, // ps — running check
		reply{stdout: "0.0.0.0:44000\n"},
		reply{},
	)
	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Remote:            "ops@bastion",
		Claude:            true,
		ClaudeCredentials: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if fr.calls[4].host != "ops@bastion" {
		t.Errorf("port lookup should hit the remote, got host %q", fr.calls[4].host)
	}
	if fr.calls[5].host != "" {
		t.Errorf("rsync should run locally, got host %q", fr.calls[5].host)
	}
	if !strings.Contains(fr.calls[5].cmd, `-J '\''ops@bastion'\''`) {
		t.Errorf("credentials rsync ssh transport should include -J:\n%s", fr.calls[5].cmd)
	}
}

// TestCreate_ClaudeCredentialsRetriesOnceOnConnectFailure pins the
// recovery path for "rsync failed because the in-container sshd isn't
// ready yet": after the first rsync fails, the use-case layer waits
// briefly and retries the *exact same* command once. The wait is
// driven by a tunable package variable so tests don't pay the real
// wall-clock cost.
func TestCreate_ClaudeCredentialsRetriesOnceOnConnectFailure(t *testing.T) {
	// No t.Parallel: this test mutates the package-level retry delay
	// via SetClaudeCredentialsRetryDelayForTest. Running it in parallel
	// with TestCreate_ClaudeCredentialsBothAttemptsFailSurfacesError
	// (which mutates the same global) races on that variable.
	restore := app.SetClaudeCredentialsRetryDelayForTest(0)
	t.Cleanup(restore)

	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", ".credentials.json"), `{"token":"x"}`)

	rsyncErr := &exec.ExitError{}
	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},         // ps -a
		reply{},                          // build
		reply{stdout: "abc123\n"},        // run
		reply{stdout: "demo\n"},          // ps — running check
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{err: rsyncErr, stderr: "Connection refused\n"}, // rsync — fails
		reply{}, // rsync retry — succeeds
	)

	var out bytes.Buffer
	err := a.Create(context.Background(), &out, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Claude:            true,
		ClaudeCredentials: true,
	})
	if err != nil {
		t.Fatalf("Create: %v\nout:\n%s", err, out.String())
	}
	if got := len(fr.calls); got != 7 {
		t.Fatalf("expected 7 runner calls (ps -a, build, run, ps, port, rsync, rsync-retry), got %d:\n%+v",
			got, fr.calls)
	}
	if fr.calls[5].cmd != fr.calls[6].cmd {
		t.Errorf("retry should re-issue the exact same rsync command:\n first: %q\nsecond: %q",
			fr.calls[5].cmd, fr.calls[6].cmd)
	}
	if !strings.Contains(out.String(), "retrying once") {
		t.Errorf("expected a retry-explanation line in stdout:\n%s", out.String())
	}
}

// TestCreate_ClaudeCredentialsBothAttemptsFailSurfacesError pins the
// give-up path: if the retry fails too, the second error is what the
// caller sees and no extra attempts are made.
func TestCreate_ClaudeCredentialsBothAttemptsFailSurfacesError(t *testing.T) {
	// No t.Parallel: see TestCreate_ClaudeCredentialsRetriesOnceOnConnectFailure
	// — both tests mutate the same package-level retry delay.
	restore := app.SetClaudeCredentialsRetryDelayForTest(0)
	t.Cleanup(restore)

	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", ".credentials.json"), `{"token":"x"}`)

	first := &exec.ExitError{}
	second := &exec.ExitError{}
	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},
		reply{},
		reply{stdout: "abc123\n"},
		reply{stdout: "demo\n"}, // ps — running check
		reply{stdout: "0.0.0.0:33000\n"},
		reply{err: first, stderr: "Connection refused\n"},
		reply{err: second, stderr: "Connection refused\n"},
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Claude:            true,
		ClaudeCredentials: true,
	})
	if err == nil {
		t.Fatal("expected error when both rsync attempts fail")
	}
	if got := len(fr.calls); got != 7 {
		t.Errorf("expected exactly one retry (7 total calls), got %d:\n%+v", got, fr.calls)
	}
}

// TestCreate_ClaudeFlagDoesNotImplyCredentialsRsync keeps --claude and
// --claude-credentials decoupled: setting only --claude must not push
// any credentials.
func TestCreate_ClaudeFlagDoesNotImplyCredentialsRsync(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"},
		reply{stdout: "other\n"},
		reply{},
		reply{},
		reply{stdout: "demo\n"}, // ps — running check
	)
	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Claude: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Errorf("--claude alone should not trigger a port lookup or rsync; got %d calls", got)
	}
	if !strings.Contains(fr.calls[1].stdin, "https://claude.ai/install.sh") {
		t.Errorf("--claude should embed the Claude installer in the Dockerfile:\n%s",
			fr.calls[1].stdin)
	}
}

// TestCreate_OpencodeLabelsContainerAndEmbedsInstaller pins that
// --opencode both stamps the `opencode=true` metadata label on the
// container (so `codebox shell` can run it in a tmux pane) and embeds the
// opencode installer in the generated Dockerfile.
func TestCreate_OpencodeLabelsContainerAndEmbedsInstaller(t *testing.T) {
	t.Parallel()
	home := t.TempDir() // no ~/.config/opencode/opencode.json — push is skipped
	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},
		reply{},
		reply{stdout: "abc123\n"},
		reply{stdout: "demo\n"}, // ps — running check
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Opencode: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Fatalf("--opencode with no config file should not push; got %d calls: %+v", got, fr.calls)
	}
	if !strings.Contains(fr.calls[2].cmd, "--label opencode=true") {
		t.Errorf("run cmd should carry the opencode=true label, got %q", fr.calls[2].cmd)
	}
	if !strings.Contains(fr.calls[1].stdin, "https://opencode.ai/install") {
		t.Errorf("--opencode should embed the opencode installer in the Dockerfile:\n%s",
			fr.calls[1].stdin)
	}
}

// TestCreate_OpencodePushesConfigWhenPresent pins the post-run config
// copy: when ~/.config/opencode/opencode.json exists on the operator's
// machine, Create rsyncs it into /home/user/.config/opencode/ inside the
// instance (the same --mkpath/--chmod single-file push as credentials).
func TestCreate_OpencodePushesConfigWhenPresent(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".config", "opencode", "opencode.json"), `{"theme":"x"}`)

	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},         // ps -a
		reply{},                          // build
		reply{stdout: "abc123\n"},        // run
		reply{stdout: "demo\n"},          // ps — running check
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{},                          // rsync
	)

	var out bytes.Buffer
	err := a.Create(context.Background(), &out, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Opencode: true,
	})
	if err != nil {
		t.Fatalf("Create: %v\nout:\n%s", err, out.String())
	}
	if got := len(fr.calls); got != 6 {
		t.Fatalf("expected 6 runner calls (ps -a, build, run, ps, port, rsync), got %d:\n%+v",
			got, fr.calls)
	}
	rsync := fr.calls[5].cmd
	for _, want := range []string{
		"rsync ",
		"--mkpath",
		"--chmod=F0600",
		"-p 33000",
		filepath.Join(home, ".config", "opencode", "opencode.json"),
		"user@localhost:/home/user/.config/opencode/opencode.json",
	} {
		if !strings.Contains(rsync, want) {
			t.Errorf("rsync command missing %q:\n%s", want, rsync)
		}
	}
	if fr.calls[5].host != "" {
		t.Errorf("rsync should run on the local host (host=\"\"), got %q", fr.calls[5].host)
	}
	if !strings.Contains(out.String(), "Pushing opencode config") {
		t.Errorf("expected a status line about pushing opencode config:\n%s", out.String())
	}
}

// TestCreate_OpencodeSkipsConfigWhenAbsent pins the best-effort contract:
// the opencode config is optional, so a missing source file is silently
// skipped — no port lookup, no rsync — rather than failing the create.
func TestCreate_OpencodeSkipsConfigWhenAbsent(t *testing.T) {
	t.Parallel()
	home := t.TempDir() // intentionally no ~/.config/opencode/opencode.json
	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},
		reply{},
		reply{stdout: "abc123\n"},
		reply{stdout: "demo\n"}, // ps — running check
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Opencode: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Errorf("missing opencode config must not trigger a port lookup or rsync; got %d calls", got)
	}
}

// TestCreate_CodexLabelsContainerAndEmbedsInstaller pins that --codex
// both stamps the `codex=true` metadata label on the container and embeds
// the codex installer in the generated Dockerfile.
func TestCreate_CodexLabelsContainerAndEmbedsInstaller(t *testing.T) {
	t.Parallel()
	home := t.TempDir() // no ~/.codex/config.toml — push is skipped
	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},
		reply{},
		reply{stdout: "abc123\n"},
		reply{stdout: "demo\n"}, // ps — running check
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Codex: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Fatalf("--codex with no config file should not push; got %d calls: %+v", got, fr.calls)
	}
	if !strings.Contains(fr.calls[2].cmd, "--label codex=true") {
		t.Errorf("run cmd should carry the codex=true label, got %q", fr.calls[2].cmd)
	}
	if !strings.Contains(fr.calls[1].stdin, "https://chatgpt.com/codex/install.sh") {
		t.Errorf("--codex should embed the codex installer in the Dockerfile:\n%s",
			fr.calls[1].stdin)
	}
}

// TestCreate_CodexPushesConfigWhenPresent pins the post-run config copy:
// when ~/.codex/config.toml exists on the operator's machine, Create
// rsyncs it into /home/user/.codex/ inside the instance (the same
// --mkpath/--chmod single-file push as the other agent configs).
func TestCreate_CodexPushesConfigWhenPresent(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".codex", "config.toml"), "model = \"x\"\n")

	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},         // ps -a
		reply{},                          // build
		reply{stdout: "abc123\n"},        // run
		reply{stdout: "demo\n"},          // ps — running check
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{},                          // rsync
	)

	var out bytes.Buffer
	err := a.Create(context.Background(), &out, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Codex: true,
	})
	if err != nil {
		t.Fatalf("Create: %v\nout:\n%s", err, out.String())
	}
	if got := len(fr.calls); got != 6 {
		t.Fatalf("expected 6 runner calls (ps -a, build, run, ps, port, rsync), got %d:\n%+v",
			got, fr.calls)
	}
	rsync := fr.calls[5].cmd
	for _, want := range []string{
		"rsync ",
		"--mkpath",
		"--chmod=F0600",
		"-p 33000",
		filepath.Join(home, ".codex", "config.toml"),
		"user@localhost:/home/user/.codex/config.toml",
	} {
		if !strings.Contains(rsync, want) {
			t.Errorf("rsync command missing %q:\n%s", want, rsync)
		}
	}
	if fr.calls[5].host != "" {
		t.Errorf("rsync should run on the local host (host=\"\"), got %q", fr.calls[5].host)
	}
	if !strings.Contains(out.String(), "Pushing Codex config") {
		t.Errorf("expected a status line about pushing Codex config:\n%s", out.String())
	}
}

// TestCreate_CodexSkipsConfigWhenAbsent pins the best-effort contract:
// the codex config is optional, so a missing source file is silently
// skipped — no port lookup, no rsync — rather than failing the create.
func TestCreate_CodexSkipsConfigWhenAbsent(t *testing.T) {
	t.Parallel()
	home := t.TempDir() // intentionally no ~/.codex/config.toml
	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},
		reply{},
		reply{stdout: "abc123\n"},
		reply{stdout: "demo\n"}, // ps — running check
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Codex: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Errorf("missing codex config must not trigger a port lookup or rsync; got %d calls", got)
	}
}

// TestCreate_CodexCredentialsPushesWhenPresent pins the opt-in
// credentials copy: with --codex --codex-credentials and ~/.codex/auth.json
// present (but no config.toml, to isolate the auth push), Create rsyncs
// the auth file into /home/user/.codex/auth.json.
func TestCreate_CodexCredentialsPushesWhenPresent(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".codex", "auth.json"), `{"token":"x"}`)

	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},         // ps -a
		reply{},                          // build
		reply{stdout: "abc123\n"},        // run
		reply{stdout: "demo\n"},          // ps — running check
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup (credentials push)
		reply{},                          // rsync
	)

	var out bytes.Buffer
	err := a.Create(context.Background(), &out, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Codex: true, CodexCredentials: true,
	})
	if err != nil {
		t.Fatalf("Create: %v\nout:\n%s", err, out.String())
	}
	if got := len(fr.calls); got != 6 {
		t.Fatalf("expected 6 runner calls (config skipped, creds pushed), got %d:\n%+v",
			got, fr.calls)
	}
	rsync := fr.calls[5].cmd
	for _, want := range []string{
		"--mkpath",
		"--chmod=F0600",
		filepath.Join(home, ".codex", "auth.json"),
		"user@localhost:/home/user/.codex/auth.json",
	} {
		if !strings.Contains(rsync, want) {
			t.Errorf("rsync command missing %q:\n%s", want, rsync)
		}
	}
	if !strings.Contains(out.String(), "Pushing Codex credentials") {
		t.Errorf("expected a status line about pushing Codex credentials:\n%s", out.String())
	}
}

// TestCreate_CodexCredentialsSkipWhenAbsent pins the best-effort
// contract: --codex-credentials with no ~/.codex/auth.json on the host is
// silently skipped — no port lookup, no rsync.
func TestCreate_CodexCredentialsSkipWhenAbsent(t *testing.T) {
	t.Parallel()
	home := t.TempDir() // no ~/.codex/auth.json (and no config.toml)
	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},
		reply{},
		reply{stdout: "abc123\n"},
		reply{stdout: "demo\n"}, // ps — running check
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Codex: true, CodexCredentials: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Errorf("missing codex auth must not trigger a port lookup or rsync; got %d calls", got)
	}
}

// TestCreate_OpencodeCredentialsPushesWhenPresent pins the opt-in
// credentials copy: with --opencode --opencode-credentials and
// ~/.local/share/opencode/auth.json present (but no opencode.json, to
// isolate the auth push), Create rsyncs the auth file into
// /home/user/.local/share/opencode/auth.json.
func TestCreate_OpencodeCredentialsPushesWhenPresent(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".local", "share", "opencode", "auth.json"), `{"token":"x"}`)

	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},         // ps -a
		reply{},                          // build
		reply{stdout: "abc123\n"},        // run
		reply{stdout: "demo\n"},          // ps — running check
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup (credentials push)
		reply{},                          // rsync
	)

	var out bytes.Buffer
	err := a.Create(context.Background(), &out, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Opencode: true, OpencodeCredentials: true,
	})
	if err != nil {
		t.Fatalf("Create: %v\nout:\n%s", err, out.String())
	}
	if got := len(fr.calls); got != 6 {
		t.Fatalf("expected 6 runner calls (config skipped, creds pushed), got %d:\n%+v",
			got, fr.calls)
	}
	rsync := fr.calls[5].cmd
	for _, want := range []string{
		"--mkpath",
		"--chmod=F0600",
		filepath.Join(home, ".local", "share", "opencode", "auth.json"),
		"user@localhost:/home/user/.local/share/opencode/auth.json",
	} {
		if !strings.Contains(rsync, want) {
			t.Errorf("rsync command missing %q:\n%s", want, rsync)
		}
	}
	if !strings.Contains(out.String(), "Pushing opencode credentials") {
		t.Errorf("expected a status line about pushing opencode credentials:\n%s", out.String())
	}
}

// TestCreate_OpencodeCredentialsSkipWhenAbsent pins the best-effort
// contract: --opencode-credentials with no auth.json on the host is
// silently skipped — no port lookup, no rsync.
func TestCreate_OpencodeCredentialsSkipWhenAbsent(t *testing.T) {
	t.Parallel()
	home := t.TempDir() // no ~/.local/share/opencode/auth.json (and no config)
	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},
		reply{},
		reply{stdout: "abc123\n"},
		reply{stdout: "demo\n"}, // ps — running check
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		Opencode: true, OpencodeCredentials: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Errorf("missing opencode auth must not trigger a port lookup or rsync; got %d calls", got)
	}
}

// TestCreate_StartCheckRetriesUntilRunning pins the post-run probe:
// `<engine> run -d` returns as soon as the engine has accepted the
// request, but the container may not yet appear in the running list.
// We retry on the backoff schedule and treat the eventual presence
// of the instance name as success.
func TestCreate_StartCheckRetriesUntilRunning(t *testing.T) {
	// No t.Parallel: this test mutates the package-level backoff slice
	// via SetStartCheckBackoffForTest. Running it in parallel with the
	// other start-check tests (which mutate the same global) races.
	restore := app.SetStartCheckBackoffForTest([]time.Duration{0, 0, 0})
	t.Cleanup(restore)

	a, fr := newApp(t, &stubKeys{key: "k"},
		reply{stdout: "other\n"},  // ps -a
		reply{},                   // build
		reply{stdout: "abc123\n"}, // run
		reply{stdout: "other\n"},  // ps — not yet running
		reply{stdout: "demo\n"},   // ps — running
	)

	var out bytes.Buffer
	err := a.Create(context.Background(), &out, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
	})
	if err != nil {
		t.Fatalf("Create: %v\nout:\n%s", err, out.String())
	}
	if got := len(fr.calls); got != 5 {
		t.Fatalf("expected 5 calls (ps -a, build, run, ps, ps-retry), got %d:\n%+v",
			got, fr.calls)
	}
	if !strings.Contains(out.String(), `Container "demo" is not running yet`) {
		t.Errorf("expected a wait-and-retry status line:\n%s", out.String())
	}
}

// TestCreate_StartCheckFailsAfterAllRetries pins the give-up path:
// after the initial check plus three retries (4 total attempts), if
// the container is still not in the running list, Create returns an
// error naming the instance and the attempt count.
func TestCreate_StartCheckFailsAfterAllRetries(t *testing.T) {
	// No t.Parallel: see TestCreate_StartCheckRetriesUntilRunning.
	restore := app.SetStartCheckBackoffForTest([]time.Duration{0, 0, 0})
	t.Cleanup(restore)

	a, fr := newApp(t, &stubKeys{key: "k"},
		reply{stdout: "other\n"},  // ps -a
		reply{},                   // build
		reply{stdout: "abc123\n"}, // run
		reply{stdout: "other\n"},  // ps — never lists demo
		reply{stdout: "other\n"},
		reply{stdout: "other\n"},
		reply{stdout: "other\n"},
	)

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
	})
	if err == nil {
		t.Fatal("expected error when container never enters the running list")
	}
	if !strings.Contains(err.Error(), `instance "demo" did not start after 4 attempts`) {
		t.Errorf("error should name instance and attempt count, got %v", err)
	}
	if got := len(fr.calls); got != 7 {
		t.Errorf("expected 4 ps probes after run (7 total calls), got %d:\n%+v",
			got, fr.calls)
	}
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
