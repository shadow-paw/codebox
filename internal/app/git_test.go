package app_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"codebox/internal/adapters/runner"
	"codebox/internal/app"
)

// TestGitPush_HappyPath_Local pins the 9-call runner sequence and the
// shape of every command emitted by GitPush against a local
// orchestrator with a configured operator git identity.
func TestGitPush_HappyPath_Local(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\nother\n"},           // 0  ps -a — exists
		reply{stdout: "0.0.0.0:33000\n"},         // 1  port lookup
		reply{stdout: "Alice\n"},                 // 2  git config user.name
		reply{stdout: "alice@example.com\n"},     // 3  git config user.email
		reply{},                                  // 4  ssh init
		reply{},                                  // 5  set-url || add
		reply{},                                  // 6  git fetch source_remote
		reply{stdout: "To ssh://...\n"},          // 7  git push
		reply{stdout: "Switched to branch...\n"}, // 8  ssh checkout
	)

	var stdout, stderr bytes.Buffer
	err := a.GitPush(context.Background(), &stdout, &stderr, app.GitPushRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		Refspec:      "origin/main:issue-1234",
	})
	if err != nil {
		t.Fatalf("GitPush: %v", err)
	}
	if got := len(fr.calls); got != 9 {
		t.Fatalf("expected 9 runner calls, got %d:\n%+v", got, fr.calls)
	}

	if !strings.Contains(fr.calls[0].cmd, "podman ps -a --format") {
		t.Errorf("call[0] should be ps -a, got %q", fr.calls[0].cmd)
	}
	if !strings.Contains(fr.calls[1].cmd, "podman port 'demo' 2222") {
		t.Errorf("call[1] should be port lookup, got %q", fr.calls[1].cmd)
	}
	if fr.calls[2].cmd != "git config --get 'user.name'" {
		t.Errorf("call[2] should read user.name, got %q", fr.calls[2].cmd)
	}
	if fr.calls[3].cmd != "git config --get 'user.email'" {
		t.Errorf("call[3] should read user.email, got %q", fr.calls[3].cmd)
	}

	initCmd := fr.calls[4].cmd
	for _, want := range []string{
		"ssh -o StrictHostKeyChecking=no user@localhost -p 33000",
		"if [ ! -d ~/source/.git ]; then",
		"mkdir -p ~/source && git init -q ~/source",
		"git config receive.denyCurrentBranch updateInstead",
		`git config user.name '\''Alice'\''`,
		`git config user.email '\''alice@example.com'\''`,
	} {
		if !strings.Contains(initCmd, want) {
			t.Errorf("init ssh command missing %q\n got: %q", want, initCmd)
		}
	}

	wantRemote := "git remote set-url 'codebox-demo' 'ssh://user@localhost:33000/home/user/source'" +
		" 2>/dev/null || git remote add 'codebox-demo' 'ssh://user@localhost:33000/home/user/source'"
	if fr.calls[5].cmd != wantRemote {
		t.Errorf("remote setup mismatch:\n got: %q\nwant: %q", fr.calls[5].cmd, wantRemote)
	}

	if fr.calls[6].cmd != "git fetch 'origin'" {
		t.Errorf("call[6] should be local git fetch of source remote, got %q",
			fr.calls[6].cmd)
	}

	wantPush := "GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' git push 'codebox-demo' " +
		"'origin/main:refs/heads/issue-1234'"
	if fr.calls[7].cmd != wantPush {
		t.Errorf("git push mismatch:\n got: %q\nwant: %q", fr.calls[7].cmd, wantPush)
	}

	checkoutCmd := fr.calls[8].cmd
	for _, want := range []string{
		"ssh -o StrictHostKeyChecking=no user@localhost -p 33000",
		`cd '\''/home/user/source'\'' && git checkout '\''issue-1234'\''`,
	} {
		if !strings.Contains(checkoutCmd, want) {
			t.Errorf("checkout ssh command missing %q\n got: %q", want, checkoutCmd)
		}
	}

	if !strings.Contains(stdout.String(), "──────── git ") {
		t.Errorf("stdout missing git block separator:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(),
		`Repository cloned to instance "demo" at ~/source (branch "issue-1234").`) {
		t.Errorf("stdout missing success line:\n%s", stdout.String())
	}
}

// TestGitPush_Remote covers the bastion case: the orchestrator lookups
// run via ssh to --remote, the inner ssh's go through `-J Remote`.
func TestGitPush_Remote(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:44000\n"},
		reply{stdout: ""}, // no user.name on this operator
		reply{stdout: ""}, // no user.email either
		reply{},           // ssh init
		reply{},           // set-url || add
		reply{},           // git fetch source_remote
		reply{},           // git push
		reply{},           // ssh checkout
	)

	err := a.GitPush(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.GitPushRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Remote:       "ops@bastion",
			InstanceKey:  "/keys/id_rsa",
			Refspec:      "origin/main:issue-1234",
		})
	if err != nil {
		t.Fatalf("GitPush: %v", err)
	}
	if fr.calls[0].host != "ops@bastion" || fr.calls[1].host != "ops@bastion" {
		t.Errorf("orchestrator lookups must run via --remote (got hosts %q, %q)",
			fr.calls[0].host, fr.calls[1].host)
	}
	for i := 2; i < len(fr.calls); i++ {
		if fr.calls[i].host != "" {
			t.Errorf("call[%d] should be local, got host %q", i, fr.calls[i].host)
		}
	}

	// Instance key is only embedded on the inner ssh hops, never on
	// the orchestrator-bound lookups or the local-only operations.
	for i, c := range fr.calls[:2] {
		if strings.Contains(c.cmd, "id_rsa") {
			t.Errorf("call[%d] %q must not reference the instance key", i, c.cmd)
		}
	}
	// Init / checkout ssh hops thread -i and -J.
	for _, idx := range []int{4, 8} {
		if !strings.Contains(fr.calls[idx].cmd, `-i '/keys/id_rsa'`) {
			t.Errorf("call[%d] should embed -i instance-key:\n%s",
				idx, fr.calls[idx].cmd)
		}
		if !strings.Contains(fr.calls[idx].cmd, `-J 'ops@bastion'`) {
			t.Errorf("call[%d] should embed -J ops@bastion:\n%s",
				idx, fr.calls[idx].cmd)
		}
	}
	// Push command threads GIT_SSH_COMMAND with the same `-i`/`-J`.
	wantSSH := `GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no -i '\''/keys/id_rsa'\'' -J '\''ops@bastion'\'''`
	if !strings.Contains(fr.calls[7].cmd, wantSSH) {
		t.Errorf("push command should thread -i/-J through GIT_SSH_COMMAND:\n got: %q",
			fr.calls[7].cmd)
	}

	// With no operator identity, the init script must not reference
	// user.name / user.email at all.
	if strings.Contains(fr.calls[4].cmd, "user.name") {
		t.Errorf("init script should skip user.name when operator has none:\n%s",
			fr.calls[4].cmd)
	}
	if strings.Contains(fr.calls[4].cmd, "user.email") {
		t.Errorf("init script should skip user.email when operator has none:\n%s",
			fr.calls[4].cmd)
	}
}

func TestGitPush_RejectsBadRefspec(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"empty refspec": "",
		"missing colon": "origin/main",
		"missing slash": "main:work",
		"empty src":     ":work",
		"empty dst":     "origin/main:",
		"empty remote":  "/main:work",
		"empty branch":  "origin/:work",
	}
	for label, spec := range cases {
		label, spec := label, spec
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			a, fr := newApp(t, &stubKeys{key: "k"})
			err := a.GitPush(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
				app.GitPushRequest{
					Instance: "demo", Orchestrator: "podman", Refspec: spec,
				})
			if err == nil {
				t.Fatalf("expected error for refspec %q", spec)
			}
			if len(fr.calls) != 0 {
				t.Errorf("runner should not be invoked for invalid refspec, got %d calls",
					len(fr.calls))
			}
		})
	}
}

// TestGitPush_NestedSourceBranch ensures a source_branch containing
// slashes (e.g. `origin/feature/x:work`) is split correctly: only the
// first slash separates the source remote from the source branch.
func TestGitPush_NestedSourceBranch(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{stdout: ""},
		reply{stdout: ""},
		reply{},
		reply{},
		reply{},
		reply{},
		reply{},
	)
	err := a.GitPush(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.GitPushRequest{
			Instance: "demo", Orchestrator: "podman",
			Refspec: "origin/feature/x:work",
		})
	if err != nil {
		t.Fatalf("GitPush: %v", err)
	}
	if fr.calls[6].cmd != "git fetch 'origin'" {
		t.Errorf("local fetch should target 'origin', got %q", fr.calls[6].cmd)
	}
	wantPush := "GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' " +
		"git push 'codebox-demo' 'origin/feature/x:refs/heads/work'"
	if fr.calls[7].cmd != wantPush {
		t.Errorf("push refspec mismatch:\n got: %q\nwant: %q", fr.calls[7].cmd, wantPush)
	}
}

func TestGitPush_RejectsUnknownOrchestrator(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})
	err := a.GitPush(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.GitPushRequest{
			Instance: "demo", Orchestrator: "containerd",
			Refspec: "origin/main:work",
		})
	if err == nil || !strings.Contains(err.Error(), "unsupported orchestrator") {
		t.Fatalf("expected unsupported orchestrator error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked for bad orchestrator")
	}
}

func TestGitPush_NotFound(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "other\n"},
	)
	err := a.GitPush(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.GitPushRequest{
			Instance: "demo", Orchestrator: "podman",
			Refspec: "origin/main:work",
		})
	if err == nil || !strings.Contains(err.Error(), `instance "demo" not found`) {
		t.Fatalf("expected not-found error, got %v", err)
	}
	if len(fr.calls) != 1 {
		t.Errorf("only ps -a should run when instance is missing, got %d calls",
			len(fr.calls))
	}
}

func TestGitPush_PortNotPublished(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: ""},
	)
	err := a.GitPush(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.GitPushRequest{
			Instance: "demo", Orchestrator: "podman",
			Refspec: "origin/main:work",
		})
	if err == nil || !strings.Contains(err.Error(), "not exposing port") {
		t.Fatalf("expected missing-port error, got %v", err)
	}
}

func TestGitPush_SSHConnectErrorSurfaced(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{err: &runner.ConnectError{Host: "u@h", Err: errors.New("ssh: no auth")}},
	)
	err := a.GitPush(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.GitPushRequest{
			Instance: "demo", Orchestrator: "podman", Remote: "u@h",
			Refspec: "origin/main:work",
		})
	var ce *runner.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("GitPush should propagate ConnectError; got %T %v", err, err)
	}
}

func TestGitPull_HappyPath_Local(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},           // ps -a
		reply{stdout: "0.0.0.0:33000\n"},  // port
		reply{},                           // set-url || add
		reply{stdout: "From ssh://...\n"}, // git fetch
	)

	var stdout, stderr bytes.Buffer
	err := a.GitPull(context.Background(), &stdout, &stderr, app.GitPullRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		Branch:       "issue-1234",
	})
	if err != nil {
		t.Fatalf("GitPull: %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Fatalf("expected 4 runner calls, got %d:\n%+v", got, fr.calls)
	}

	wantRemote := "git remote set-url 'codebox-demo' 'ssh://user@localhost:33000/home/user/source'" +
		" 2>/dev/null || git remote add 'codebox-demo' 'ssh://user@localhost:33000/home/user/source'"
	if fr.calls[2].cmd != wantRemote {
		t.Errorf("remote setup mismatch:\n got: %q\nwant: %q", fr.calls[2].cmd, wantRemote)
	}

	wantFetch := "GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' git fetch 'codebox-demo' 'issue-1234'"
	if fr.calls[3].cmd != wantFetch {
		t.Errorf("git fetch mismatch:\n got: %q\nwant: %q", fr.calls[3].cmd, wantFetch)
	}

	if !strings.Contains(stdout.String(), "──────── git ") {
		t.Errorf("stdout missing git block separator:\n%s", stdout.String())
	}
	// Checkout hint named after the instance remote and branch.
	for _, want := range []string{
		`Fetched "issue-1234" from instance "demo".`,
		"git checkout codebox-demo/issue-1234 -b issue-1234",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q\n%s", want, stdout.String())
		}
	}
}

func TestGitPull_Remote(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:55000\n"},
		reply{},
		reply{},
	)
	err := a.GitPull(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.GitPullRequest{
			Instance:     "demo",
			Orchestrator: "podman",
			Remote:       "ops@bastion",
			InstanceKey:  "/keys/id_rsa",
			Branch:       "work",
		})
	if err != nil {
		t.Fatalf("GitPull: %v", err)
	}
	if fr.calls[0].host != "ops@bastion" || fr.calls[1].host != "ops@bastion" {
		t.Errorf("orchestrator lookups must run via --remote")
	}
	wantFetch := `GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no -i '\''/keys/id_rsa'\'' -J '\''ops@bastion'\''' ` +
		"git fetch 'codebox-demo' 'work'"
	if fr.calls[3].cmd != wantFetch {
		t.Errorf("fetch command mismatch:\n got: %q\nwant: %q", fr.calls[3].cmd, wantFetch)
	}
}

func TestGitPull_RejectsMissingBranch(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})
	err := a.GitPull(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.GitPullRequest{Instance: "demo", Orchestrator: "podman"})
	if err == nil || !strings.Contains(err.Error(), "branch is required") {
		t.Fatalf("expected missing-branch error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked without a branch")
	}
}

func TestGitPull_NotFound(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "other\n"},
	)
	err := a.GitPull(context.Background(), &bytes.Buffer{}, &bytes.Buffer{},
		app.GitPullRequest{
			Instance: "demo", Orchestrator: "podman", Branch: "work",
		})
	if err == nil || !strings.Contains(err.Error(), `instance "demo" not found`) {
		t.Fatalf("expected not-found error, got %v", err)
	}
}
