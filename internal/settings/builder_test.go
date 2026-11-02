package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestResolveAdditionalRun pins the merge contract: project steps run
// first, then global steps, and blank entries are dropped.
func TestResolveAdditionalRun(t *testing.T) {
	var global, project Config
	global.Builder.AdditionalRun = []string{"echo global", "  "}
	project.Builder.AdditionalRun = []string{"", "echo project"}

	got := ResolveAdditionalRun(global, project)
	want := []string{"echo project", "echo global"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ResolveAdditionalRun = %v, want %v", got, want)
	}
}

// TestResolveAdditionalRunEmpty confirms two empty configs resolve to no
// steps.
func TestResolveAdditionalRunEmpty(t *testing.T) {
	if got := ResolveAdditionalRun(Config{}, Config{}); len(got) != 0 {
		t.Fatalf("ResolveAdditionalRun(empty) = %v, want none", got)
	}
}

// TestLoadBuilderAdditionalRun confirms the builder.additional-run list
// parses off a .codebox.conf file.
func TestLoadBuilderAdditionalRun(t *testing.T) {
	dir := t.TempDir()
	yaml := `
builder:
  additional-run:
    - echo $(whoami)
`
	if err := os.WriteFile(filepath.Join(dir, ".codebox.conf"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := loadFile(filepath.Join(dir, ".codebox.conf"), &cfg); err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	want := []string{"echo $(whoami)"}
	if strings.Join(cfg.Builder.AdditionalRun, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Builder.AdditionalRun = %v, want %v", cfg.Builder.AdditionalRun, want)
	}
}

// TestResolveAdditionalCopy pins the merge contract: project steps first,
// then global; fully blank entries dropped; half-filled entries kept so
// the create flow can reject them; values trimmed.
func TestResolveAdditionalCopy(t *testing.T) {
	var global, project Config
	global.Builder.AdditionalCopy = []CopyStep{{From: "~/g", To: "g/"}, {From: " ", To: ""}}
	project.Builder.AdditionalCopy = []CopyStep{
		{From: " ~/p/ ", To: " p/ ", Exclude: []string{" *.log ", " ", ".git/"}},
		{From: "~/half"},
	}

	got := ResolveAdditionalCopy(global, project)
	want := []CopyStep{
		{From: "~/p/", To: "p/", Exclude: []string{"*.log", ".git/"}},
		{From: "~/half"},
		{From: "~/g", To: "g/"},
	}
	if len(got) != len(want) {
		t.Fatalf("ResolveAdditionalCopy = %+v, want %+v", got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("ResolveAdditionalCopy[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestLoadBuilderAdditionalCopy confirms the builder.additional-copy list
// parses off a .codebox.conf file.
func TestLoadBuilderAdditionalCopy(t *testing.T) {
	dir := t.TempDir()
	yaml := `
builder:
  additional-copy:
    - from: ~/.claude/skills/
      to: .claude/skills/
      exclude:
        - synced/
        - find-skills/
`
	if err := os.WriteFile(filepath.Join(dir, ".codebox.conf"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := loadFile(filepath.Join(dir, ".codebox.conf"), &cfg); err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	want := []CopyStep{{From: "~/.claude/skills/", To: ".claude/skills/", Exclude: []string{"synced/", "find-skills/"}}}
	if !reflect.DeepEqual(cfg.Builder.AdditionalCopy, want) {
		t.Fatalf("Builder.AdditionalCopy = %+v, want %+v", cfg.Builder.AdditionalCopy, want)
	}
}

// TestLoadBuilderAdditionalCopyExcludeForms confirms exclude accepts a
// single pattern as well as a list.
func TestLoadBuilderAdditionalCopyExcludeForms(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want StringList
	}{
		{"single", `exclude: synced/`, StringList{"synced/"}},
		{"single quoted", `exclude: "*.log"`, StringList{"*.log"}},
		{"list", "exclude:\n        - synced/\n        - find-skills/", StringList{"synced/", "find-skills/"}},
		{"flow list", `exclude: [synced/, find-skills/]`, StringList{"synced/", "find-skills/"}},
		{"absent", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			conf := "builder:\n  additional-copy:\n    - from: a\n      to: b\n      " + tt.yaml + "\n"
			if err := os.WriteFile(filepath.Join(dir, ".codebox.conf"), []byte(conf), 0600); err != nil {
				t.Fatal(err)
			}
			var cfg Config
			if err := loadFile(filepath.Join(dir, ".codebox.conf"), &cfg); err != nil {
				t.Fatalf("loadFile: %v", err)
			}
			if got := cfg.Builder.AdditionalCopy[0].Exclude; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Exclude = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestLoadBuilderAdditionalCopyExcludeRejectsMap confirms a malformed
// exclude (a mapping) is a load error rather than being ignored.
func TestLoadBuilderAdditionalCopyExcludeRejectsMap(t *testing.T) {
	dir := t.TempDir()
	conf := "builder:\n  additional-copy:\n    - from: a\n      to: b\n      exclude:\n        x: y\n"
	if err := os.WriteFile(filepath.Join(dir, ".codebox.conf"), []byte(conf), 0600); err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := loadFile(filepath.Join(dir, ".codebox.conf"), &cfg); err == nil {
		t.Fatal("expected an error for a mapping-valued exclude")
	}
}
