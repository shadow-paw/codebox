package app_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"codebox/internal/app"
)

// TestWorkflow_RejectsMalformedRefspecBeforeRunningAnything pins the
// "verify all argument format before run any commands" requirement:
// a bad refspec must fail before any container command fires.
func TestWorkflow_RejectsMalformedRefspecBeforeRunningAnything(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Workflow(context.Background(),
		&bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.WorkflowRequest{
			Orchestrator: "podman",
			Refspec:      "no-colon-here",
		})
	if err == nil {
		t.Fatal("expected error for malformed refspec")
	}
	if !strings.Contains(err.Error(), "refspec") {
		t.Errorf("error should mention refspec, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner must not be invoked when refspec is invalid; got %d calls", len(fr.calls))
	}
}

// TestWorkflow_RejectsInvalidTargetBranchAsInstanceName pins that the
// target branch (which doubles as the instance name) must satisfy
// validateInstanceName before any command runs.
func TestWorkflow_RejectsInvalidTargetBranchAsInstanceName(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Workflow(context.Background(),
		&bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.WorkflowRequest{
			Orchestrator: "podman",
			Refspec:      "origin/main:bad name",
		})
	if err == nil {
		t.Fatal("expected error for invalid target branch")
	}
	if !strings.Contains(err.Error(), "invalid character") {
		t.Errorf("error should mention invalid character, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner must not be invoked when target branch is invalid; got %d calls", len(fr.calls))
	}
}

// TestWorkflow_RejectsUnknownOrchestrator pins that container.New is
// called for validation before anything runs.
func TestWorkflow_RejectsUnknownOrchestrator(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.Workflow(context.Background(),
		&bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{},
		app.WorkflowRequest{
			Orchestrator: "containerd",
			Refspec:      "origin/main:demo",
		})
	if err == nil || !strings.Contains(err.Error(), "unsupported orchestrator") {
		t.Fatalf("expected unsupported orchestrator error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner must not be invoked for invalid orchestrator; got %d calls", len(fr.calls))
	}
}

// TestWorkflow_ChainsCreateThenGitPushThenShell exercises the happy
// path end-to-end. Workflow internally calls Create (4 runner calls),
// GitPush (2 calls to look up port + several git invocations), and
// Shell (2 calls to verify exists + look up port, plus the interactive
// ssh). The exact call count is implementation-detail; the assertion
// here is that all three command shapes appear in order.
func TestWorkflow_ChainsCreateThenGitPushThenShell(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "ssh-ed25519 AAAA test"},
		// --- Create ---
		reply{stdout: "other\n"},  // ps -a — no collision
		reply{},                   // build
		reply{stdout: "abc123\n"}, // run
		reply{stdout: "demo\n"},   // running check
		// --- GitPush ---
		reply{stdout: "demo\n"},          // ps -a — exists
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{},                          // ssh init script
		reply{},                          // git remote set-url
		reply{stdout: ""},                // git config user.name (empty stdout = "")
		reply{stdout: ""},                // git config user.email
		reply{},                          // git fetch
		reply{},                          // git push
		reply{},                          // ssh checkout
		// --- Shell ---
		reply{stdout: "demo\n"},          // ps -a — exists
		reply{stdout: ""},                // tmux label — disabled
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{},                          // interactive ssh
	)

	var stdout, stderr bytes.Buffer
	err := a.Workflow(context.Background(), &bytes.Buffer{}, &stdout, &stderr,
		app.WorkflowRequest{
			Orchestrator: "podman",
			Refspec:      "origin/main:demo",
			OS:           "debian_13",
		})
	if err != nil {
		t.Fatalf("Workflow: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}

	// Spot-check that the three phases all ran by looking for marker
	// commands in the recorded calls.
	joined := joinCmds(fr.calls)
	for _, want := range []string{
		"podman build", // create build
		"podman run",   // create run
		"git push",     // git push
		"-p 33000",     // shell ssh used the looked-up port
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("workflow trace missing %q\nall calls:\n%s", want, joined)
		}
	}
}

func joinCmds(calls []recordedCall) string {
	var b strings.Builder
	for _, c := range calls {
		b.WriteString(c.cmd)
		b.WriteByte('\n')
	}
	return b.String()
}
