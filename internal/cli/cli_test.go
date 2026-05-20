package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codebox/internal/cli"
)

// withFakeHome sets HOME (and USERPROFILE on Windows) to a fresh temp
// directory containing a synthetic ~/.ssh and returns the directory.
// Callers may not invoke t.Parallel — t.Setenv forbids it.
func withFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func writePub(t *testing.T, home, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ".ssh", name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// runCLI invokes cli.Run with a fresh context and capture buffers. It
// fails the test if the exit code is non-zero so that individual test
// bodies stay focused on output assertions.
func runCLI(t *testing.T, args []string) (stdout, stderr string) {
	t.Helper()
	var so, se bytes.Buffer
	if code := cli.Run(context.Background(), args, &so, &se); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr=%s", code, se.String())
	}
	return so.String(), se.String()
}

func TestRun_NoArgs_ShowsBannerAndHelp(t *testing.T) {
	t.Parallel()
	stdout, _ := runCLI(t, nil)

	wants := []string{
		"https://github.com/shadow-paw/codebox",
		"Available Commands:",
		"create",
		"delete",
		"list",
		"shell",
		"exec",
		"pull",
		"push",
	}
	for _, want := range wants {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\n--- stdout ---\n%s", want, stdout)
		}
	}
}

func TestRun_Help_ShowsAllCommands(t *testing.T) {
	t.Parallel()
	stdout, _ := runCLI(t, []string{"--help"})
	for _, c := range []string{"create", "delete", "list", "shell", "exec", "pull", "push"} {
		if !strings.Contains(stdout, c) {
			t.Errorf("--help missing command %q", c)
		}
	}
}

// TestPullPush_HelpListsFlags exercises the cobra wiring for the
// pull/push commands without invoking their action layer. The full
// behaviour is covered by app-layer tests; this guards the flag
// surface visible to operators.
func TestPullPush_HelpListsFlags(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"pull": {"--orchestrator", "--remote", "--instance-key", "--instance-path", "--local-path"},
		"push": {"--orchestrator", "--remote", "--instance-key", "--local-path", "--instance-path"},
	}
	for cmd, wants := range cases {
		cmd, wants := cmd, wants
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			stdout, _ := runCLI(t, []string{cmd, "--help"})
			prev := -1
			for _, flag := range wants {
				pos := strings.Index(stdout, flag)
				if pos == -1 {
					t.Fatalf("%s --help missing flag %q\n%s", cmd, flag, stdout)
				}
				if pos <= prev {
					t.Fatalf("%s --help flag %q out of order\n%s", cmd, flag, stdout)
				}
				prev = pos
			}
		})
	}
}

// TestExec_SuppressesBanner ensures `codebox exec` produces no banner
// header. exec's stdout is meant to be piped into other tools, so a
// decorative banner would corrupt the stream. The exec invocation here
// fails before any ssh runs (instance lookup goes to a non-existent
// engine), so we assert only on stdout, which is the channel that
// matters for downstream consumers.
func TestExec_SuppressesBanner(t *testing.T) {
	t.Parallel()
	var so, se bytes.Buffer
	_ = cli.Run(context.Background(),
		[]string{"exec", "demo", "--orchestrator=podman", "--", "ls"},
		&so, &se)
	if strings.Contains(so.String(), "https://github.com/shadow-paw/codebox") {
		t.Errorf("stdout should not contain banner, got:\n%s", so.String())
	}
}

// TestExec_RejectsMissingInstance still prints the banner via the help
// path — only the actual command stream is banner-free.
func TestExec_HelpKeepsBanner(t *testing.T) {
	t.Parallel()
	stdout, _ := runCLI(t, []string{"--help"})
	if !strings.Contains(stdout, "https://github.com/shadow-paw/codebox") {
		t.Errorf("--help should keep the banner; got:\n%s", stdout)
	}
}

func TestCreate_HelpShowsSupportedSections(t *testing.T) {
	t.Parallel()
	stdout, _ := runCLI(t, []string{"create", "--help"})

	for _, want := range []string{
		"Orchestrators:",
		"OS:",
		"Languages:",
		"Agents:",
		"Tools:",
		"Network:",
		"podman, docker",
		"debian_12",
		"ubuntu_24",
		"redhat_10",
		"3.12, 3.13, 3.14",
		"24, 25, 26",
		"1.26.0",
		"Claude Code",
		"--claude-credentials",
		"OpenAI Codex CLI",
		"opencode",
		"PostgreSQL",
		"--https-proxy=URL",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("create --help missing %q", want)
		}
	}
}

// TestExec_RejectsMissingOrMisplacedDash guards the contract that exec
// requires '--' between INSTANCE and COMMAND. Without the separator,
// flags meant for the inner command would be consumed by codebox.
func TestExec_RejectsMissingOrMisplacedDash(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"no dash, command after instance":   {"exec", "demo", "ls", "-la"},
		"no dash, only instance":            {"exec", "demo"},
		"dash present but no command after": {"exec", "demo", "--"},
		"extra positional before dash":      {"exec", "demo", "ls", "--", "-la"},
	}
	for name, args := range cases {
		args := args
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var so, se bytes.Buffer
			if code := cli.Run(context.Background(), args, &so, &se); code == 0 {
				t.Fatalf("exit code = 0, want non-zero\nstdout=%s\nstderr=%s",
					so.String(), se.String())
			}
		})
	}
}

// TestCreate_FlagOrderMatchesSpec checks the flags appear in the help text
// in the same order they are documented in the spec. This guards the
// "Maintain flags ordering in help" requirement against accidental
// alphabetic re-sorting.
func TestCreate_FlagOrderMatchesSpec(t *testing.T) {
	t.Parallel()
	stdout, _ := runCLI(t, []string{"create", "--help"})

	// Scope the search to the Flags: block so flag names mentioned in the
	// Long description above do not perturb ordering.
	flagsStart := strings.Index(stdout, "Flags:")
	if flagsStart == -1 {
		t.Fatalf("create --help has no Flags: section\n%s", stdout)
	}
	flagsEnd := strings.Index(stdout[flagsStart:], "Orchestrators:")
	if flagsEnd == -1 {
		t.Fatalf("create --help has no Orchestrators: footer\n%s", stdout)
	}
	flagsBlock := stdout[flagsStart : flagsStart+flagsEnd]

	want := []string{
		"--orchestrator",
		"--remote",
		"--instance-key",
		"--rebuild",
		"--https-proxy",
		"--os",
		"--python",
		"--node",
		"--golang",
		"--dotnet",
		"--claude",
		"--claude-credentials",
		"--codex",
		"--opencode",
		"--podman",
		"--psql",
	}
	prev := -1
	for _, flag := range want {
		pos := strings.Index(flagsBlock, flag)
		if pos == -1 {
			t.Fatalf("flag %q missing from create --help Flags block\n%s", flag, flagsBlock)
		}
		if pos <= prev {
			t.Fatalf("flag %q appears before the previous flag (want spec order)\n%s",
				flag, flagsBlock)
		}
		prev = pos
	}
}

// TestCreate_AutoDetectAmbiguous covers the failure path: zero or
// multiple .pub files in ~/.ssh must surface a helpful error before
// any orchestrator command is attempted.
func TestCreate_AutoDetectAmbiguous(t *testing.T) {
	home := withFakeHome(t)
	writePub(t, home, "id_rsa.pub", "ssh-rsa AAAA one")
	writePub(t, home, "id_ed25519.pub", "ssh-ed25519 AAAA two")

	var so, se bytes.Buffer
	code := cli.Run(context.Background(), []string{"create", "demo"}, &so, &se)
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero when auto-detect is ambiguous")
	}
	if !strings.Contains(se.String(), "--instance-key") {
		t.Errorf("stderr should mention --instance-key, got:\n%s", se.String())
	}
}

// TestCreate_RejectsUnsupportedAgentFlags pins the contract that the
// agent/podman flags fail fast until their installers ship. The flag
// surface is preserved (they still parse) but invoking them errors
// out. `--claude` has shipped — it is exercised by its own tests —
// so it is not in this list.
func TestCreate_RejectsUnsupportedAgentFlags(t *testing.T) {
	for _, flag := range []string{"--codex", "--opencode", "--podman"} {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			home := withFakeHome(t)
			writePub(t, home, "id_ed25519.pub", "ssh-ed25519 AAAA k")

			var so, se bytes.Buffer
			code := cli.Run(context.Background(),
				[]string{"create", "demo", flag}, &so, &se)
			if code == 0 {
				t.Fatalf("exit = 0, want non-zero for %s", flag)
			}
			if !strings.Contains(se.String(), flag) || !strings.Contains(se.String(), "not yet supported") {
				t.Errorf("stderr should name %s as unsupported, got:\n%s", flag, se.String())
			}
		})
	}
}

// TestCreate_RejectsUnsupportedAgentFlags_NamesAll surfaces every
// unsupported flag in one error rather than one per invocation.
func TestCreate_RejectsUnsupportedAgentFlags_NamesAll(t *testing.T) {
	home := withFakeHome(t)
	writePub(t, home, "id_ed25519.pub", "ssh-ed25519 AAAA k")

	var so, se bytes.Buffer
	code := cli.Run(context.Background(),
		[]string{"create", "demo", "--codex", "--opencode"}, &so, &se)
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero when multiple unsupported flags are set")
	}
	for _, want := range []string{"--codex", "--opencode", "not yet supported"} {
		if !strings.Contains(se.String(), want) {
			t.Errorf("stderr should mention %q, got:\n%s", want, se.String())
		}
	}
}

// TestCreate_ClaudeCredentialsRequiresClaude pins the contract that
// --claude-credentials cannot be used without --claude — pushing
// credentials to an instance with no Claude install is meaningless and
// should fail before any orchestrator command runs.
func TestCreate_ClaudeCredentialsRequiresClaude(t *testing.T) {
	home := withFakeHome(t)
	writePub(t, home, "id_ed25519.pub", "ssh-ed25519 AAAA k")

	var so, se bytes.Buffer
	code := cli.Run(context.Background(),
		[]string{"create", "demo", "--claude-credentials"}, &so, &se)
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero when --claude-credentials is used without --claude")
	}
	for _, want := range []string{"--claude-credentials", "--claude"} {
		if !strings.Contains(se.String(), want) {
			t.Errorf("stderr should mention %q, got:\n%s", want, se.String())
		}
	}
}

// TestCreate_RejectsUnsupportedVersion exercises the image-layer enum
// check via the CLI surface — an unknown --python value surfaces as a
// non-zero exit with a helpful message before any orchestrator command.
func TestCreate_RejectsUnsupportedVersion(t *testing.T) {
	home := withFakeHome(t)
	writePub(t, home, "id_ed25519.pub", "ssh-ed25519 AAAA k")

	var so, se bytes.Buffer
	code := cli.Run(context.Background(),
		[]string{"create", "demo", "--python=3.10"}, &so, &se)
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero for unsupported python version")
	}
	for _, want := range []string{"unsupported python version", "3.12, 3.13, 3.14"} {
		if !strings.Contains(se.String(), want) {
			t.Errorf("stderr should mention %q, got:\n%s", want, se.String())
		}
	}
}

func TestCreate_RejectsUnknownOrchestrator(t *testing.T) {
	home := withFakeHome(t)
	writePub(t, home, "id_ed25519.pub", "ssh-ed25519 AAAA k")

	var so, se bytes.Buffer
	code := cli.Run(context.Background(),
		[]string{"create", "demo", "--orchestrator=containerd"}, &so, &se)
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero for bad orchestrator")
	}
	if !strings.Contains(se.String(), "unsupported orchestrator") {
		t.Errorf("stderr should explain unsupported orchestrator:\n%s", se.String())
	}
}

func TestCreate_RejectsUnknownOS(t *testing.T) {
	home := withFakeHome(t)
	writePub(t, home, "id_ed25519.pub", "ssh-ed25519 AAAA k")

	var so, se bytes.Buffer
	code := cli.Run(context.Background(),
		[]string{"create", "demo", "--os=freebsd_14"}, &so, &se)
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero for bad OS")
	}
	if !strings.Contains(se.String(), "unsupported os") {
		t.Errorf("stderr should explain unsupported os:\n%s", se.String())
	}
}
