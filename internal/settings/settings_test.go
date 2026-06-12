package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeArgLists(t *testing.T) {
	tests := []struct {
		name    string
		global  []string
		project []string
		want    []string
	}{
		{
			name: "empty",
			want: nil,
		},
		{
			name:   "global only",
			global: []string{"orchestrator=podman", "remote=user@host"},
			want:   []string{"orchestrator=podman", "remote=user@host"},
		},
		{
			name:    "project only",
			project: []string{"orchestrator=docker"},
			want:    []string{"orchestrator=docker"},
		},
		{
			name:    "project overrides global",
			global:  []string{"orchestrator=podman", "remote=user@host"},
			project: []string{"orchestrator=docker"},
			want:    []string{"orchestrator=docker", "remote=user@host"},
		},
		{
			name:    "project adds to global",
			global:  []string{"orchestrator=podman"},
			project: []string{"remote=user@host"},
			want:    []string{"orchestrator=podman", "remote=user@host"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeArgLists(tt.global, tt.project)
			if len(got) != len(tt.want) {
				t.Fatalf("mergeArgLists() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("mergeArgLists()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestInjectArgs(t *testing.T) {
	tests := []struct {
		name    string
		cli     []string
		global  Config
		project Config
		want    []string
	}{
		{
			name: "no settings",
			cli:  []string{"create", "mybox"},
			want: []string{"create", "mybox"},
		},
		{
			name:   "all setting injected before args",
			cli:    []string{"create", "mybox"},
			global: configWith([]string{"orchestrator=podman"}, nil),
			want:   []string{"--orchestrator=podman", "create", "mybox"},
		},
		{
			name:   "create setting injected after create token",
			cli:    []string{"create", "mybox"},
			global: configWith(nil, []string{"python=3.14"}),
			want:   []string{"create", "--python=3.14", "mybox"},
		},
		{
			name:   "create settings not injected for non-create subcommand",
			cli:    []string{"list"},
			global: configWith(nil, []string{"python=3.14"}),
			want:   []string{"list"},
		},
		{
			name:   "cli flag takes precedence over global",
			cli:    []string{"--orchestrator=docker", "create", "mybox"},
			global: configWith([]string{"orchestrator=podman"}, nil),
			want:   []string{"--orchestrator=docker", "create", "mybox"},
		},
		{
			name:    "project overrides global",
			cli:     []string{"create", "mybox"},
			global:  configWith([]string{"orchestrator=podman"}, nil),
			project: configWith([]string{"orchestrator=docker"}, nil),
			want:    []string{"--orchestrator=docker", "create", "mybox"},
		},
		{
			name:   "boolean flag in create settings",
			cli:    []string{"create", "mybox"},
			global: configWith(nil, []string{"claude", "claude-credentials"}),
			want:   []string{"create", "--claude", "--claude-credentials", "mybox"},
		},
		{
			name:   "boolean agent disabled via config injects =false",
			cli:    []string{"create", "mybox"},
			global: configWith(nil, []string{"codex=false"}),
			want:   []string{"create", "--codex=false", "mybox"},
		},
		{
			name:   "cli --claude=false overrides config-enabled claude",
			cli:    []string{"create", "--claude=false", "mybox"},
			global: configWith(nil, []string{"claude"}),
			// The config "claude" entry is dropped because --claude is
			// explicit on the CLI; the operator's --claude=false wins.
			want: []string{"create", "--claude=false", "mybox"},
		},
		{
			name:   "cli create flag not duplicated",
			cli:    []string{"create", "--python=3.12", "mybox"},
			global: configWith(nil, []string{"python=3.14", "node=25"}),
			// --node=25 is injected right after "create"; --python=3.12 follows as the original CLI arg
			want: []string{"create", "--node=25", "--python=3.12", "mybox"},
		},
		{
			name:   "persistent flag with separate value token still detected",
			cli:    []string{"--orchestrator", "docker", "create", "mybox"},
			global: configWith(nil, []string{"python=3.14"}),
			want:   []string{"--orchestrator", "docker", "create", "--python=3.14", "mybox"},
		},
		{
			name:   "create settings injected after workflow token",
			cli:    []string{"workflow", "origin/main:demo"},
			global: configWith(nil, []string{"python=3.14", "claude"}),
			want:   []string{"workflow", "--python=3.14", "--claude", "origin/main:demo"},
		},
		{
			name:   "all and create settings both apply to workflow",
			cli:    []string{"workflow", "origin/main:demo"},
			global: configWith([]string{"orchestrator=podman"}, []string{"claude"}),
			want:   []string{"--orchestrator=podman", "workflow", "--claude", "origin/main:demo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InjectArgs(tt.cli, tt.global, tt.project)
			if len(got) != len(tt.want) {
				t.Fatalf("InjectArgs() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("InjectArgs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestLoadFiles(t *testing.T) {
	dir := t.TempDir()

	yaml := `
args:
  all:
    - orchestrator=podman
    - remote=user@host
  create:
    - python=3.14
    - claude
`
	if err := os.WriteFile(filepath.Join(dir, ".codebox.conf"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := loadFile(filepath.Join(dir, ".codebox.conf"), &cfg); err != nil {
		t.Fatalf("loadFile: %v", err)
	}

	wantAll := []string{"orchestrator=podman", "remote=user@host"}
	if len(cfg.Args.All) != len(wantAll) {
		t.Fatalf("Args.All = %v, want %v", cfg.Args.All, wantAll)
	}
	for i, v := range wantAll {
		if cfg.Args.All[i] != v {
			t.Errorf("Args.All[%d] = %q, want %q", i, cfg.Args.All[i], v)
		}
	}

	wantCreate := []string{"python=3.14", "claude"}
	if len(cfg.Args.Create) != len(wantCreate) {
		t.Fatalf("Args.Create = %v, want %v", cfg.Args.Create, wantCreate)
	}
	for i, v := range wantCreate {
		if cfg.Args.Create[i] != v {
			t.Errorf("Args.Create[%d] = %q, want %q", i, cfg.Args.Create[i], v)
		}
	}
}

func TestLoadGitPush(t *testing.T) {
	dir := t.TempDir()

	yaml := `
git:
  push-from: origin/main
`
	if err := os.WriteFile(filepath.Join(dir, ".codebox.conf"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := loadFile(filepath.Join(dir, ".codebox.conf"), &cfg); err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	if cfg.Git.Push != "origin/main" {
		t.Errorf("Git.Push = %q, want %q", cfg.Git.Push, "origin/main")
	}
}

func TestLoadMissingFilesOK(t *testing.T) {
	global, project, err := Load("/nonexistent-home", "/nonexistent-work")
	if err != nil {
		t.Fatalf("Load() returned error for missing files: %v", err)
	}
	if len(global.Args.All) != 0 || len(project.Args.Create) != 0 {
		t.Error("expected empty configs for missing files")
	}
}

// configWith is a test helper to build a Config value inline.
func configWith(all, create []string) Config {
	var c Config
	c.Args.All = all
	c.Args.Create = create
	return c
}
