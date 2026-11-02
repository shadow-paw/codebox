package app_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"codebox/internal/app"
)

// TestCreate_AdditionalCopyRsyncsAfterRun pins the builder.additional-copy
// flow: once the container is running, each step becomes one local rsync
// in the `file push` shape plus --mkpath, with globbed sources and the
// destination (a directory or a pathname) passed through verbatim.
func TestCreate_AdditionalCopyRsyncsAfterRun(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", "skills", "a", "SKILL.md"), "x")
	mustWriteFile(t, filepath.Join(home, "notes", "one.md"), "1")
	mustWriteFile(t, filepath.Join(home, "notes", "two.md"), "2")

	a, fr := newAppWithHome(t, home, &stubKeys{key: "k"},
		reply{stdout: "other\n"},         // ps -a — no collision
		reply{},                          // build
		reply{stdout: "abc123\n"},        // run — container id
		reply{stdout: "demo\n"},          // ps — running check
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup (once for all steps)
		reply{},                          // rsync step 1
		reply{},                          // rsync step 2
		reply{},                          // rsync step 3
	)

	var out bytes.Buffer
	err := a.Create(context.Background(), &out, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		AdditionalCopy: []app.CopyStep{
			{From: "~/.claude/skills/", To: ".claude/skills/", Exclude: []string{"synced/", "find-skills/"}},
			{From: "~/notes/*.md", To: "~/docs/"},
			{From: "~/notes/one.md", To: "notes/renamed.md"},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v\nout:\n%s", err, out.String())
	}
	if got := len(fr.calls); got != 8 {
		t.Fatalf("expected 8 runner calls (ps -a, build, run, ps, port, 3x rsync), got %d:\n%+v",
			got, fr.calls)
	}

	wantSSH := "-e 'ssh -o StrictHostKeyChecking=no -p 33000'"
	want1 := "rsync --verbose --archive --compress --update --progress --mkpath --copy-unsafe-links " +
		"'--exclude=synced/' '--exclude=find-skills/' " + wantSSH + " '" +
		filepath.Join(home, ".claude", "skills") + "/' 'user@localhost:.claude/skills/'"
	if fr.calls[5].cmd != want1 {
		t.Errorf("rsync step 1:\n got: %s\nwant: %s", fr.calls[5].cmd, want1)
	}
	want2 := "rsync --verbose --archive --compress --update --progress --mkpath --copy-unsafe-links " + wantSSH + " '" +
		filepath.Join(home, "notes", "one.md") + "' '" + filepath.Join(home, "notes", "two.md") +
		"' 'user@localhost:/home/user/docs/'"
	if fr.calls[6].cmd != want2 {
		t.Errorf("rsync step 2:\n got: %s\nwant: %s", fr.calls[6].cmd, want2)
	}
	want3 := "rsync --verbose --archive --compress --update --progress --mkpath --copy-unsafe-links " + wantSSH + " '" +
		filepath.Join(home, "notes", "one.md") + "' 'user@localhost:notes/renamed.md'"
	if fr.calls[7].cmd != want3 {
		t.Errorf("rsync step 3:\n got: %s\nwant: %s", fr.calls[7].cmd, want3)
	}
	for _, c := range fr.calls[5:] {
		if c.host != "" {
			t.Errorf("rsync should run on the local host (host=\"\"), got %q", c.host)
		}
	}
	for _, want := range []string{
		"Copying ~/.claude/skills/ to .claude/skills/...",
		"Copying ~/notes/*.md to ~/docs/...",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Index(out.String(), "Copying") > strings.Index(out.String(), "is ready") {
		t.Errorf("copies should run before the ready line:\n%s", out.String())
	}
}

// TestCreate_AdditionalCopyMissingSourceFailsEarly confirms a source that
// matches nothing is reported before any orchestrator command runs.
func TestCreate_AdditionalCopyMissingSourceFailsEarly(t *testing.T) {
	t.Parallel()
	a, fr := newAppWithHome(t, t.TempDir(), &stubKeys{key: "k"})

	err := a.Create(context.Background(), &bytes.Buffer{}, app.CreateRequest{
		Instance: "demo", Orchestrator: "podman", OS: "debian_13",
		AdditionalCopy: []app.CopyStep{{From: "~/nope/", To: "x/"}},
	})
	if err == nil || !strings.Contains(err.Error(), "matches no files") {
		t.Fatalf("Create: err = %v, want a matches-no-files error", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("no orchestrator command should run; got %d calls: %+v", len(fr.calls), fr.calls)
	}
}

// TestWorkflow_ForwardsAdditionalCopy confirms workflow passes the
// builder.additional-copy steps through to its create step: an
// unresolvable source fails before anything runs, exactly as for create.
func TestWorkflow_ForwardsAdditionalCopy(t *testing.T) {
	t.Parallel()
	a, fr := newAppWithHome(t, t.TempDir(), &stubKeys{key: "k"})

	err := a.Workflow(context.Background(), nil, &bytes.Buffer{}, &bytes.Buffer{}, app.WorkflowRequest{
		Orchestrator:   "podman",
		Refspec:        "main:issue-1",
		OS:             "debian_13",
		AdditionalCopy: []app.CopyStep{{From: "~/nope", To: "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "builder.additional-copy") {
		t.Fatalf("Workflow: err = %v, want a builder.additional-copy error", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("no orchestrator command should run; got %d calls: %+v", len(fr.calls), fr.calls)
	}
}
