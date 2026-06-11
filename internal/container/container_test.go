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
	got := e.Run("demo", false)
	want := `podman run -d --name 'demo' --hostname 'demo' --label codebox=true --publish-all 'demo'`
	if got != want {
		t.Fatalf("Run = %q\nwant %q", got, want)
	}
}

// TestRun_PodmanAddsDeviceAndCapFlags pins that podman=true inserts the
// device/capability/security-opt flags the nested rootless Podman needs
// (in place of --privileged) and podman=false leaves them off.
func TestRun_PodmanAddsDeviceAndCapFlags(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	got := e.Run("demo", true)
	want := `podman run -d --name 'demo' --hostname 'demo' --label codebox=true ` +
		`--device /dev/fuse --device /dev/net/tun ` +
		`--cap-add=sys_admin --cap-add=net_admin --cap-add=mknod ` +
		`--security-opt label=disable --security-opt unmask=ALL --publish-all 'demo'`
	if got != want {
		t.Fatalf("Run podman = %q\nwant %q", got, want)
	}
	if strings.Contains(e.Run("demo", false), "--device") ||
		strings.Contains(e.Run("demo", false), "--cap-add") {
		t.Errorf("Run without podman must not contain device/cap flags")
	}
}

// TestExec_RunsAsUserWithHome pins the engine exec command shape used to
// run one-off setup (e.g. `podman system migrate`) as user "user" with
// HOME set so per-user rootless state resolves.
func TestExec_RunsAsUserWithHome(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	got := e.Exec("demo", "podman", "system", "migrate")
	want := `podman exec --user user --env HOME=/home/user 'demo' podman system migrate`
	if got != want {
		t.Fatalf("Exec = %q\nwant %q", got, want)
	}
}

func TestRun_QuotesInstanceName(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	got := e.Run("nasty'name", false)
	// Single quote must be properly escaped, not break out of the string.
	if !strings.Contains(got, `'nasty'\''name'`) {
		t.Errorf("Run should shell-quote the instance name:\n  got: %s", got)
	}
}

func TestListRunningNames_FormatIsParseable(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	got := e.ListRunningNames()
	want := "podman ps --format '{{.Names}}'"
	if got != want {
		t.Fatalf("ListRunningNames = %q, want %q", got, want)
	}
}

func TestStop_QuotesInstanceName(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	if got := e.Stop("demo"); got != "podman stop 'demo'" {
		t.Errorf("Stop = %q, want %q", got, "podman stop 'demo'")
	}
	if got := e.Stop("nasty'name"); !strings.Contains(got, `'nasty'\''name'`) {
		t.Errorf("Stop should shell-quote the instance name: %s", got)
	}
}

func TestRemove_QuotesInstanceName(t *testing.T) {
	t.Parallel()
	e, _ := container.New("docker")
	if got := e.Remove("demo"); got != "docker rm 'demo'" {
		t.Errorf("Remove = %q, want %q", got, "docker rm 'demo'")
	}
	if got := e.Remove("nasty'name"); !strings.Contains(got, `'nasty'\''name'`) {
		t.Errorf("Remove should shell-quote the instance name: %s", got)
	}
}

func TestListCodeboxInstances_FormatIsParseable(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	got := e.ListCodeboxInstances()
	want := `podman ps -a --filter label=codebox=true --format '{{.Names}}|{{.CreatedAt}}|{{.Ports}}'`
	if got != want {
		t.Fatalf("ListCodeboxInstances = %q, want %q", got, want)
	}
}

func TestHostPort_FormatIsParseable(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	if got, want := e.HostPort("demo"), "podman port 'demo' 2222"; got != want {
		t.Fatalf("HostPort = %q, want %q", got, want)
	}
	if got := e.HostPort("nasty'name"); !strings.Contains(got, `'nasty'\''name'`) {
		t.Errorf("HostPort should shell-quote the instance name: %s", got)
	}
}

func TestUntag_QuotesInstanceName(t *testing.T) {
	t.Parallel()
	e, _ := container.New("podman")
	if got := e.Untag("demo"); got != "podman untag 'demo'" {
		t.Errorf("Untag = %q, want %q", got, "podman untag 'demo'")
	}
	if got := e.Untag("nasty'name"); !strings.Contains(got, `'nasty'\''name'`) {
		t.Errorf("Untag should shell-quote the instance name: %s", got)
	}
}

func TestUntag_DockerUsesRmi(t *testing.T) {
	t.Parallel()
	e, _ := container.New("docker")
	if got := e.Untag("demo"); got != "docker rmi 'demo'" {
		t.Errorf("Untag = %q, want %q", got, "docker rmi 'demo'")
	}
}
