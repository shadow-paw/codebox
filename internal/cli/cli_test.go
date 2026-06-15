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
		"completion",
		"create",
		"delete",
		"list",
		"shell",
		"exec",
		"file",
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
	for _, c := range []string{"completion", "create", "delete", "list", "shell", "exec", "file"} {
		if !strings.Contains(stdout, c) {
			t.Errorf("--help missing command %q", c)
		}
	}
}

// TestRun_RootHelpFormsAreIdentical pins the contract that the three
// root-help entrypoints — bare invocation, `help`, and `--help` —
// render the same output. The banner, command list, and Flags section
// must all line up so operators see one stable help page regardless of
// how they ask for it.
func TestRun_RootHelpFormsAreIdentical(t *testing.T) {
	t.Parallel()
	bare, _ := runCLI(t, nil)
	helpCmd, _ := runCLI(t, []string{"help"})
	helpFlag, _ := runCLI(t, []string{"--help"})
	if bare != helpCmd {
		t.Errorf("`codebox` and `codebox help` differ:\n%s", diffFirstLines(bare, helpCmd))
	}
	if bare != helpFlag {
		t.Errorf("`codebox` and `codebox --help` differ:\n%s", diffFirstLines(bare, helpFlag))
	}
}

// TestRun_SubcommandHelpFormsAreIdentical pins the same contract for
// every subcommand: `codebox <cmd> --help` and `codebox help <cmd>`
// render identically (banner + the subcommand's own help).
func TestRun_SubcommandHelpFormsAreIdentical(t *testing.T) {
	t.Parallel()
	cmds := []string{
		"create",
		"delete",
		"list",
		"shell",
		"port-forward",
		"vscode",
		"exec",
		"file",
		"git",
		"mount",
		"umount",
		"workflow",
		"completion",
	}
	for _, c := range cmds {
		c := c
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			viaFlag, _ := runCLI(t, []string{c, "--help"})
			viaHelp, _ := runCLI(t, []string{"help", c})
			if viaFlag != viaHelp {
				t.Errorf("`codebox %s --help` and `codebox help %s` differ:\n%s",
					c, c, diffFirstLines(viaFlag, viaHelp))
			}
			if !strings.Contains(viaFlag, "https://github.com/shadow-paw/codebox") {
				t.Errorf("help for %q should keep the banner; got:\n%s", c, viaFlag)
			}
		})
	}
}

// diffFirstLines returns a small head-to-head excerpt of two strings
// for use in error messages — full diffs blow up the test log.
func diffFirstLines(a, b string) string {
	const n = 30
	la, lb := strings.SplitN(a, "\n", n+1), strings.SplitN(b, "\n", n+1)
	limit := len(la)
	if len(lb) < limit {
		limit = len(lb)
	}
	var sb strings.Builder
	for i := 0; i < limit && i < n; i++ {
		if la[i] != lb[i] {
			sb.WriteString("- ")
			sb.WriteString(la[i])
			sb.WriteByte('\n')
			sb.WriteString("+ ")
			sb.WriteString(lb[i])
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// TestFilePushPull_HelpListsFlags exercises the cobra wiring for the
// `file push`/`file pull` commands without invoking their action layer.
// The full behaviour is covered by app-layer tests; this guards the flag
// surface visible to operators: the command-specific flags appear in
// the Flags: block in declared order, and the inherited
// --orchestrator, --remote, --instance-key fall under Global Flags:.
func TestFilePushPull_HelpListsFlags(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ local, global []string }{
		"file pull": {
			local:  []string{"--instance-path", "--local-path"},
			global: []string{"--instance-key", "--orchestrator", "--remote"},
		},
		"file push": {
			local:  []string{"--local-path", "--instance-path"},
			global: []string{"--instance-key", "--orchestrator", "--remote"},
		},
	}
	for cmd, want := range cases {
		cmd, want := cmd, want
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			args := append(strings.Split(cmd, " "), "--help")
			stdout, _ := runCLI(t, args)
			assertOrderedSections(t, stdout, want.local, want.global)
		})
	}
}

// TestFile_HelpListsPushAndPull guards the cobra wiring for the `file`
// parent command and its two children, mirroring the `git` parent.
func TestFile_HelpListsPushAndPull(t *testing.T) {
	t.Parallel()
	stdout, _ := runCLI(t, []string{"file", "--help"})
	for _, want := range []string{"push", "pull"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("file --help missing subcommand %q\n%s", want, stdout)
		}
	}
}

// TestGit_HelpListsPushAndPull guards the cobra wiring for the
// `git` parent command and its two children. The behaviour is covered
// at the app layer; this guard ensures the surface stays visible.
func TestGit_HelpListsPushAndPull(t *testing.T) {
	t.Parallel()
	stdout, _ := runCLI(t, []string{"git", "--help"})
	for _, want := range []string{"push", "pull"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("git --help missing subcommand %q\n%s", want, stdout)
		}
	}
}

// TestGit_RejectsNonGitCwd guards the pre-check: push and pull
// both refuse to run from a directory that has no `.git` directory.
// The check happens at the CLI layer so the operator never reaches
// the orchestrator with a half-set-up local repo.
func TestGit_RejectsNonGitCwd(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"push", []string{"git", "push", "demo", "origin/main:work"}},
		{"pull", []string{"git", "pull", "demo", "work"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			var so, se bytes.Buffer
			code := cli.Run(context.Background(), tc.args, &so, &se)
			if code == 0 {
				t.Fatalf("exit = 0, want non-zero when cwd is not a git repo\nstderr=%s",
					se.String())
			}
			if !strings.Contains(se.String(), "not a git repository") {
				t.Errorf("stderr should mention 'not a git repository', got:\n%s",
					se.String())
			}
		})
	}
}

// TestGitPushPull_HelpListsFlags pins the flag surface each git
// subcommand exposes. The three common flags are inherited from the
// root command and surface under "Global Flags:".
func TestGitPushPull_HelpListsFlags(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"git push", "git pull"} {
		cmd := cmd
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			args := append(strings.Split(cmd, " "), "--help")
			stdout, _ := runCLI(t, args)
			assertOrderedSections(t, stdout, nil,
				[]string{"--instance-key", "--orchestrator", "--remote"})
		})
	}
}

// assertOrderedSections checks that the local flags appear in order
// under "Flags:" and the global flags appear in order under "Global
// Flags:". Either slice may be empty (e.g. commands with no local
// flags). The "Global Flags:" header always sits after the "Flags:"
// header, so finding it confirms the section boundary.
func assertOrderedSections(t *testing.T, help string, local, global []string) {
	t.Helper()

	globalStart := strings.Index(help, "Global Flags:")
	if globalStart == -1 && len(global) > 0 {
		t.Fatalf("help has no Global Flags: section\n%s", help)
	}

	if len(local) > 0 {
		flagsStart := strings.Index(help, "Flags:")
		if flagsStart == -1 {
			t.Fatalf("help has no Flags: section\n%s", help)
		}
		end := len(help)
		if globalStart > flagsStart {
			end = globalStart
		}
		block := help[flagsStart:end]
		assertOrdered(t, "Flags", block, local)
	}

	if len(global) > 0 {
		assertOrdered(t, "Global Flags", help[globalStart:], global)
	}
}

func assertOrdered(t *testing.T, section, block string, want []string) {
	t.Helper()
	prev := -1
	for _, flag := range want {
		pos := strings.Index(block, flag)
		if pos == -1 {
			t.Fatalf("%s block missing flag %q\n%s", section, flag, block)
		}
		if pos <= prev {
			t.Fatalf("%s block: flag %q out of order\n%s", section, flag, block)
		}
		prev = pos
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

// TestCompletion_SuppressesBanner pins the same contract for shell
// completion script generation: `codebox completion bash` (and the
// other shells) emits an evaluable shell script, so the banner must
// not be prepended.
func TestCompletion_SuppressesBanner(t *testing.T) {
	t.Parallel()
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		shell := shell
		t.Run(shell, func(t *testing.T) {
			t.Parallel()
			stdout, _ := runCLI(t, []string{"completion", shell})
			if strings.Contains(stdout, "https://github.com/shadow-paw/codebox") {
				t.Errorf("completion %s stdout should not contain banner, got first chars:\n%s",
					shell, firstChars(stdout, 200))
			}
			if stdout == "" {
				t.Errorf("completion %s should emit a script", shell)
			}
		})
	}
}

// TestCompleteRuntime_SuppressesBanner guards the hidden __complete
// command cobra fires at tab-completion time. Its stdout is parsed by
// the shell — a banner would break the parse.
func TestCompleteRuntime_SuppressesBanner(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"__complete", "__completeNoDesc"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var so, se bytes.Buffer
			_ = cli.Run(context.Background(), []string{name, "shell", ""}, &so, &se)
			if strings.Contains(so.String(), "https://github.com/shadow-paw/codebox") {
				t.Errorf("%s stdout should not contain banner, got:\n%s", name, so.String())
			}
		})
	}
}

// TestCompletion_HelpKeepsBanner pins that asking for help on a
// banner-suppressed command still keeps the banner — help is for
// humans, banners belong on every help path.
func TestCompletion_HelpKeepsBanner(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"completion", "--help"},
		{"completion", "bash", "--help"},
		{"exec", "--help"},
		{"exec", "-h"},
	} {
		args := args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			stdout, _ := runCLI(t, args)
			if !strings.Contains(stdout, "https://github.com/shadow-paw/codebox") {
				t.Errorf("%v should keep the banner; got:\n%s", args, stdout)
			}
		})
	}
}

func firstChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// TestRootFlags_AcceptedBeforeOrAfterCommand pins the contract that
// --orchestrator, --remote and --instance-key are root-level
// persistent flags: cobra accepts them in any position before `--`,
// including before the subcommand name. The test fires `delete` with
// a bogus orchestrator placed first; the value must reach the
// container layer (engine lookup) regardless of position so the
// resulting error mentions "unsupported orchestrator".
func TestRootFlags_AcceptedBeforeOrAfterCommand(t *testing.T) {
	t.Parallel()
	placements := map[string][]string{
		"before command": {"--orchestrator=containerd", "delete", "demo"},
		"after command":  {"delete", "demo", "--orchestrator=containerd"},
		"after instance": {"delete", "--orchestrator=containerd", "demo"},
	}
	for name, args := range placements {
		args := args
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var so, se bytes.Buffer
			code := cli.Run(context.Background(), args, &so, &se)
			if code == 0 {
				t.Fatalf("exit = 0, want non-zero\nstderr=%s", se.String())
			}
			if !strings.Contains(se.String(), "unsupported orchestrator") {
				t.Errorf("stderr should mention 'unsupported orchestrator', got:\n%s",
					se.String())
			}
		})
	}
}

// TestComplete_HonoursRemoteBeforeCommand pins that shell completion
// honours --remote when the operator placed it before the subcommand
// name. completeInstances reads from cmd.Flags(), which inherits
// persistent flags from the root, so the placement should not change
// the lookup target. With no orchestrator reachable the lookup yields
// no candidates, but the directive emitted (`:4`) and the absence of
// a banner are the contract under test.
func TestComplete_HonoursRemoteBeforeCommand(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"__complete", "--remote=ops@bastion", "shell", ""},
		{"__complete", "shell", "--remote=ops@bastion", ""},
	} {
		args := args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			var so, se bytes.Buffer
			_ = cli.Run(context.Background(), args, &so, &se)
			if !strings.Contains(so.String(), ":4") {
				t.Errorf("completion should surface directive :4 (NoFileComp); stdout=%q stderr=%q",
					so.String(), se.String())
			}
			if strings.Contains(so.String(), "https://github.com/shadow-paw/codebox") {
				t.Errorf("completion stdout should not contain banner; got:\n%s", so.String())
			}
		})
	}
}

// TestComplete_FlagValueCandidates pins the fixed-enum value
// completion wired onto --orchestrator, --os, and the language
// version flags. The cobra __complete protocol writes one candidate
// per line on stdout, then the directive integer (:4 = NoFileComp),
// then a debug trailer. We assert each expected candidate appears,
// and that NoFileComp is set so the shell does not fall back to file
// completion.
func TestComplete_FlagValueCandidates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "--orchestrator at root",
			args: []string{"__complete", "--orchestrator", ""},
			want: []string{"podman", "docker"},
		},
		{
			name: "--orchestrator through a subcommand",
			args: []string{"__complete", "shell", "--orchestrator", ""},
			want: []string{"podman", "docker"},
		},
		{
			name: "create --os",
			args: []string{"__complete", "create", "demo", "--os", ""},
			want: []string{"debian_12", "debian_13", "ubuntu_24", "ubuntu_26", "redhat_10"},
		},
		{
			name: "create --python",
			args: []string{"__complete", "create", "demo", "--python", ""},
			want: []string{"3.12", "3.13", "3.14"},
		},
		{
			name: "create --node",
			args: []string{"__complete", "create", "demo", "--node", ""},
			want: []string{"24", "25", "26"},
		},
		{
			name: "create --golang",
			args: []string{"__complete", "create", "demo", "--golang", ""},
			want: []string{"1.26.0"},
		},
		{
			name: "create --dotnet",
			args: []string{"__complete", "create", "demo", "--dotnet", ""},
			want: []string{"8", "10"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var so, se bytes.Buffer
			_ = cli.Run(context.Background(), tc.args, &so, &se)
			out := so.String()
			for _, want := range tc.want {
				if !strings.Contains(out, want+"\n") {
					t.Errorf("completion missing candidate %q\nstdout=%q\nstderr=%q",
						want, out, se.String())
				}
			}
			if !strings.Contains(out, ":4") {
				t.Errorf("completion should surface directive :4 (NoFileComp); stdout=%q stderr=%q",
					out, se.String())
			}
		})
	}
}

// TestComplete_WiredOnInstanceCommands sanity-checks that every
// subcommand whose first positional is INSTANCE is wired up to the
// completion helper. Without a real engine the lookup yields no
// candidates, but the directive emitted to stdout must be
// `ShellCompDirectiveNoFileComp` (encoded as `:4`) so file completion
// does not leak in as a fallback. Cobra writes the directive integer
// to stdout (`:<N>`) and a debug trailer to stderr; the script reads
// only stdout, so we assert on stdout. Commands that take no INSTANCE
// (`list`, `create`, `completion`) are not asserted on here.
func TestComplete_WiredOnInstanceCommands(t *testing.T) {
	t.Parallel()
	cmds := [][]string{
		{"delete"},
		{"shell"},
		{"vscode"},
		{"exec"},
		{"file", "pull"},
		{"file", "push"},
		{"git", "push"},
		{"git", "pull"},
		{"mount"},
		{"umount"},
	}
	for _, c := range cmds {
		c := c
		t.Run(strings.Join(c, " "), func(t *testing.T) {
			t.Parallel()
			args := append([]string{"__complete"}, c...)
			args = append(args, "")
			var so, se bytes.Buffer
			_ = cli.Run(context.Background(), args, &so, &se)
			out := so.String()
			if !strings.Contains(out, ":4") {
				t.Errorf("`%s` completion should surface directive :4 (NoFileComp); stdout=%q stderr=%q",
					strings.Join(args, " "), out, se.String())
			}
			if strings.Contains(out, "https://github.com/shadow-paw/codebox") {
				t.Errorf("`%s` completion stdout should not contain banner; got:\n%s",
					strings.Join(args, " "), out)
			}
		})
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
		"--codex-credentials",
		"opencode",
		"--opencode-credentials",
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
//
// --orchestrator, --remote and --instance-key are persistent root
// flags; they appear under "Global Flags:" (alphabetised by cobra)
// rather than in the local Flags: block. The asserted order below
// covers the create-specific flags in their declared order.
func TestCreate_FlagOrderMatchesSpec(t *testing.T) {
	t.Parallel()
	stdout, _ := runCLI(t, []string{"create", "--help"})

	// Scope the local Flags: search so flag names mentioned in the
	// Long description above do not perturb ordering.
	flagsStart := strings.Index(stdout, "Flags:")
	if flagsStart == -1 {
		t.Fatalf("create --help has no Flags: section\n%s", stdout)
	}
	globalStart := strings.Index(stdout, "Global Flags:")
	if globalStart == -1 {
		t.Fatalf("create --help has no Global Flags: section\n%s", stdout)
	}
	flagsBlock := stdout[flagsStart:globalStart]

	wantLocal := []string{
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
		"--codex-credentials",
		"--opencode",
		"--opencode-credentials",
		"--podman",
		"--psql",
		"--tmux",
	}
	assertOrdered(t, "Flags", flagsBlock, wantLocal)

	footerStart := strings.Index(stdout, "Orchestrators:")
	if footerStart == -1 || footerStart <= globalStart {
		t.Fatalf("create --help should have Orchestrators: footer after Global Flags:\n%s", stdout)
	}
	wantGlobal := []string{"--instance-key", "--orchestrator", "--remote"}
	assertOrdered(t, "Global Flags", stdout[globalStart:footerStart], wantGlobal)
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

// TestCreate_AgentFlagsAreAllSupported pins that every agent flag now
// ships an installer: enabling them must not fail fast with a "not yet
// supported" rejection. The command still errors later (no orchestrator
// in the test environment), but never at the flag-validation gate. This
// guards against accidentally re-introducing the rejection that used to
// cover --codex and --opencode.
func TestCreate_AgentFlagsAreAllSupported(t *testing.T) {
	home := withFakeHome(t)
	writePub(t, home, "id_ed25519.pub", "ssh-ed25519 AAAA k")

	var so, se bytes.Buffer
	cli.Run(context.Background(),
		[]string{"create", "demo", "--claude", "--codex", "--opencode"}, &so, &se)
	if strings.Contains(se.String(), "not yet supported") {
		t.Errorf("no agent flag should be rejected as unsupported, got:\n%s", se.String())
	}
}

// TestCreate_AgentCredentialsIgnoredWithoutAgent pins the contract that a
// *-credentials flag without its agent is silently ignored, not an error:
// the command must pass the flag-validation gate (it still fails later in
// the test environment because there is no orchestrator, but never with a
// "requires" message). This covers --claude-credentials, which previously
// errored, alongside the codex/opencode variants.
func TestCreate_AgentCredentialsIgnoredWithoutAgent(t *testing.T) {
	for _, flag := range []string{
		"--claude-credentials",
		"--codex-credentials",
		"--opencode-credentials",
	} {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			home := withFakeHome(t)
			writePub(t, home, "id_ed25519.pub", "ssh-ed25519 AAAA k")

			var so, se bytes.Buffer
			cli.Run(context.Background(), []string{"create", "demo", flag}, &so, &se)
			if strings.Contains(se.String(), "requires") {
				t.Errorf("%s without its agent should be ignored, not rejected; got:\n%s",
					flag, se.String())
			}
		})
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
