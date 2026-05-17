package container_test

import (
	"strings"
	"testing"

	"codebox/internal/container"
)

func TestNew_SupportedOrchestrators(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"podman", "docker"} {
		e, err := container.New(name)
		if err != nil {
			t.Fatalf("New(%q): %v", name, err)
		}
		if e.Name() != name {
			t.Errorf("Name() = %q, want %q", e.Name(), name)
		}
	}
}

func TestNew_RejectsUnknown(t *testing.T) {
	t.Parallel()
	_, err := container.New("containerd")
	if err == nil {
		t.Fatal("New should reject unknown orchestrator")
	}
	if !strings.Contains(err.Error(), "containerd") {
		t.Errorf("error %q should mention the offending value", err.Error())
	}
}

func TestListAllNames_FormatIsParseable(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	got := e.ListAllNames()
	want := "podman ps -a --format '{{.Names}}'"
	if got != want {
		t.Fatalf("ListAllNames = %q, want %q", got, want)
	}
}

func TestBuild_EmptyContextAndStdinDockerfile(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	got := e.Build("demo", false)

	for _, want := range []string{
		"mktemp -d",
		`trap 'rm -rf "$t"' EXIT`,
		"podman build",
		"-t 'demo'",
		"-f -",
		`"$t"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Build missing %q\n  got: %s", want, got)
		}
	}
	if strings.Contains(got, "--no-cache") {
		t.Errorf("Build without rebuild should NOT pass --no-cache\n  got: %s", got)
	}
}

func TestBuild_NoCacheFlagOnRebuild(t *testing.T) {
	t.Parallel()
	e, _ := container.New("docker")
	got := e.Build("demo", true)
	if !strings.Contains(got, "docker build --no-cache -t 'demo' -f -") {
		t.Errorf("docker build with --no-cache:\n  got: %s", got)
	}
}

func TestRun_LabelsAndPublishAll(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	got := e.Run("demo")
	want := `podman run -d --name 'demo' --hostname 'demo' --label codebox=true --publish-all 'demo'`
	if got != want {
		t.Fatalf("Run = %q\nwant %q", got, want)
	}
}

func TestRun_QuotesInstanceName(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	got := e.Run("nasty'name")
	// Single quote must be properly escaped, not break out of the string.
	if !strings.Contains(got, `'nasty'\''name'`) {
		t.Errorf("Run should shell-quote the instance name:\n  got: %s", got)
	}
}
