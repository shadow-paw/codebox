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

func TestSubcommands_ParseAsStubs(t *testing.T) {
	t.Parallel()

	cases := [][]string{
		{"delete", "demo", "--orchestrator=docker", "--remote=u@h"},
		{"list", "--orchestrator=podman"},
		{
			"shell", "demo",
			"--orchestrator=podman", "--remote=u@h", "--instance-key=k",
			"--port=8000:3000", "--port=8001:3001",
		},
		{
			"exec", "demo",
			"--orchestrator=podman", "--remote=u@h", "--instance-key=k",
			"--", "ls", "-la",
		},
		{
			"pull", "demo",
			"--instance-key=k", "--instance-path=/etc/hostname", "--local-path=.",
		},
		{
			"push", "demo",
			"--instance-key=k", "--local-path=./README.md", "--instance-path=/tmp",
		},
	}
	for _, args := range cases {
		args := args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			var so, se bytes.Buffer
			if code := cli.Run(context.Background(), args, &so, &se); code != 0 {
				t.Fatalf("exit code = %d, want 0\nstderr=%s", code, se.String())
			}
		})
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
		"podman, docker",
		"debian_12",
		"ubuntu_24",
		"redhat_10",
		"3.12, 3.13, 3.14",
		"24, 25, 26",
		"1.26.0",
		"Claude Code",
		"OpenAI Codex CLI",
		"opencode",
		"PostgreSQL",
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
		"--os",
		"--python",
		"--node",
		"--golang",
		"--dotnet",
		"--claude",
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
