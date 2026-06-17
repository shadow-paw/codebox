package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveAdditionalRun pins the merge contract: global steps run
// first, then project steps, and blank entries are dropped.
func TestResolveAdditionalRun(t *testing.T) {
	var global, project Config
	global.Builder.AdditionalRun = []string{"echo global", "  "}
	project.Builder.AdditionalRun = []string{"", "echo project"}

	got := ResolveAdditionalRun(global, project)
	want := []string{"echo global", "echo project"}
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
