package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCopySources(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for _, f := range []string{".claude/skills/a/SKILL.md", ".claude/skills/b/SKILL.md", "notes/x.md", "notes/y.txt"} {
		p := filepath.Join(home, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		from string
		want []string
	}{
		{"dir contents keep trailing slash", "~/.claude/skills/", []string{home + "/.claude/skills/"}},
		{"dir itself", "~/.claude/skills", []string{home + "/.claude/skills"}},
		{"single file", "~/notes/x.md", []string{home + "/notes/x.md"}},
		{"glob files", "~/notes/*.md", []string{home + "/notes/x.md"}},
		{"glob dirs as contents", "~/.claude/skills/*/", []string{
			home + "/.claude/skills/a/", home + "/.claude/skills/b/",
		}},
		{"absolute path", home + "/notes/y.txt", []string{home + "/notes/y.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveCopySources(tt.from, home)
			if err != nil {
				t.Fatalf("resolveCopySources(%q): %v", tt.from, err)
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("resolveCopySources(%q) = %v, want %v", tt.from, got, tt.want)
			}
		})
	}
}

func TestResolveCopySources_Errors(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for _, from := range []string{"~/missing", "~/nothing-*.md", "~/[bad"} {
		if _, err := resolveCopySources(from, home); err == nil {
			t.Errorf("resolveCopySources(%q): expected error", from)
		} else if !strings.Contains(err.Error(), "builder.additional-copy") {
			t.Errorf("resolveCopySources(%q): error %q should name the config key", from, err)
		}
	}
}

func TestResolveCopyDest(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{".claude/skills/", ".claude/skills/"},
		{".claude/skills", ".claude/skills"},
		{"notes/today.md", "notes/today.md"},
		{"~", "/home/user"},
		{"~/", "/home/user/"},
		{"~/.claude/skills", "/home/user/.claude/skills"},
		{"/opt/data", "/opt/data"},
	}
	for _, tt := range tests {
		if got := resolveCopyDest(tt.in); got != tt.want {
			t.Errorf("resolveCopyDest(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveCopySteps_RequiresFromAndTo(t *testing.T) {
	t.Parallel()
	for _, s := range []CopyStep{{From: "~/x"}, {To: "x/"}} {
		_, err := resolveCopySteps([]CopyStep{s}, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "missing") {
			t.Errorf("resolveCopySteps(%+v) = %v, want a missing-field error", s, err)
		}
	}
}

func TestExpandHome_KeepsTrailingSlash(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"~/a/", "/h/a/"},
		{"~/a", "/h/a"},
		{"~/", "/h/"},
		{"~", "/h"},
		{"rel/", "rel/"},
	}
	for _, tt := range tests {
		if got := expandHome(tt.in, "/h"); got != tt.want {
			t.Errorf("expandHome(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBuildCopyRsyncCommand(t *testing.T) {
	t.Parallel()
	got := buildCopyRsyncCommand("ops@bastion", "33000", []string{"/k/id"},
		[]string{"*.log", "it's dir/"}, []string{"/h/a/", "/h/b"}, "user@localhost:.claude/skills/")
	want := "rsync --verbose --archive --compress --update --progress --mkpath --copy-unsafe-links " +
		`'--exclude=*.log' '--exclude=it'\''s dir/' ` +
		`-e 'ssh -o StrictHostKeyChecking=no -i '\''/k/id'\'' -J '\''ops@bastion'\'' -p 33000' ` +
		"'/h/a/' '/h/b' 'user@localhost:.claude/skills/'"
	if got != want {
		t.Errorf("buildCopyRsyncCommand:\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildCopyRsyncCommand_NoExcludes(t *testing.T) {
	t.Parallel()
	got := buildCopyRsyncCommand("", "1", nil, nil, []string{"/h/a"}, "user@localhost:x")
	if strings.Contains(got, "--exclude") {
		t.Errorf("no --exclude expected without patterns: %s", got)
	}
}
