package app_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codebox/internal/app"
)

// TestVSCode_Remote_OpensFolderURI pins the non-remote-terminal branch:
// preflight, a workspace probe that finds nothing, then `code` launched
// against a folder URI targeting the in-container sshd.
func TestVSCode_Remote_OpensFolderURI(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},          // ps -a — exists
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{stdout: "\n"},              // workspace probe — none
		reply{},                          // code launch
	)

	var stdout, stderr bytes.Buffer
	err := a.VSCode(context.Background(), &stdout, &stderr, app.VSCodeRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		InstanceKey:  "k",
	})
	if err != nil {
		t.Fatalf("VSCode: %v", err)
	}
	if got := len(fr.calls); got != 4 {
		t.Fatalf("expected 4 runner calls (ps, port, probe, code), got %d: %+v", got, fr.calls)
	}

	probe := fr.calls[2].cmd
	if !strings.Contains(probe, "ls -1 ~/source/*.code-workspace") {
		t.Errorf("call[2] should probe for a workspace file, got %q", probe)
	}

	wantURI := "vscode-remote://ssh-remote+user@localhost:33000/home/user/source"
	wantCode := "code --folder-uri " + "'" + wantURI + "'"
	if fr.calls[3].cmd != wantCode {
		t.Errorf("code launch mismatch:\n got: %q\nwant: %q", fr.calls[3].cmd, wantCode)
	}
	if fr.calls[3].host != "" {
		t.Errorf("code must launch locally (host=\"\"), got %q", fr.calls[3].host)
	}
	out := stdout.String()
	for _, want := range []string{"vscode", "ssh target: user@localhost:33000", wantURI} {
		if !strings.Contains(out, want) {
			t.Errorf("connection block missing %q; got:\n%s", want, out)
		}
	}
}

// TestVSCode_Remote_OpensWorkspaceFileURI pins that a discovered
// *.code-workspace file is opened as a workspace via --file-uri.
func TestVSCode_Remote_OpensWorkspaceFileURI(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{stdout: "/home/user/source/project.code-workspace\n"}, // probe — found
		reply{}, // code launch
	)

	err := a.VSCode(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, app.VSCodeRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("VSCode: %v", err)
	}
	wantURI := "vscode-remote://ssh-remote+user@localhost:33000/home/user/source/project.code-workspace"
	wantCode := "code --file-uri '" + wantURI + "'"
	if fr.calls[3].cmd != wantCode {
		t.Errorf("code launch mismatch:\n got: %q\nwant: %q", fr.calls[3].cmd, wantCode)
	}
}

// TestVSCode_Remote_BastionWritesProxyJumpAlias pins that a bastion
// (--remote) makes codebox register a managed ssh host alias carrying the
// ProxyJump, target the vscode-remote URI at that alias, and include the
// fragment from ~/.ssh/config — so Remote-SSH traverses the bastion with
// no manual ssh-config edits.
func TestVSCode_Remote_BastionWritesProxyJumpAlias(t *testing.T) {
	home := t.TempDir()
	a, fr := newAppWithHome(t, home,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},          // ps -a — exists
		reply{stdout: "0.0.0.0:33000\n"}, // port lookup
		reply{stdout: "\n"},              // workspace probe — none
		reply{},                          // code launch
	)
	var stdout bytes.Buffer
	err := a.VSCode(context.Background(), &stdout, &bytes.Buffer{}, app.VSCodeRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		InstanceKey:  "/keys/id_demo",
		Remote:       "ops@bastion",
	})
	if err != nil {
		t.Fatalf("VSCode: %v", err)
	}

	// The launched URI must target the managed alias, not user@localhost.
	wantURI := "vscode-remote://ssh-remote+codebox-demo/home/user/source"
	wantCode := "code --folder-uri '" + wantURI + "'"
	if fr.calls[3].cmd != wantCode {
		t.Errorf("code launch should target the alias URI:\n got: %q\nwant: %q",
			fr.calls[3].cmd, wantCode)
	}
	if !strings.Contains(stdout.String(), "ops@bastion") ||
		!strings.Contains(stdout.String(), "codebox_config") {
		t.Errorf("connection block should show the configured ProxyJump; got:\n%s", stdout.String())
	}

	// The managed fragment must carry the Host alias with its ProxyJump,
	// port, and IdentityFile.
	frag, err := os.ReadFile(filepath.Join(home, ".ssh", "codebox_config"))
	if err != nil {
		t.Fatalf("read codebox_config: %v", err)
	}
	for _, want := range []string{
		"Host codebox-demo",
		"HostName localhost",
		"Port 33000",
		"User user",
		"ProxyJump ops@bastion",
		`IdentityFile "/keys/id_demo"`,
	} {
		if !strings.Contains(string(frag), want) {
			t.Errorf("codebox_config missing %q; got:\n%s", want, frag)
		}
	}

	// ~/.ssh/config must Include the fragment.
	cfg, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		t.Fatalf("read ssh config: %v", err)
	}
	if !strings.Contains(string(cfg), "Include codebox_config") {
		t.Errorf("ssh config should Include codebox_config; got:\n%s", cfg)
	}
}

// TestVSCode_Remote_DirectConnectionWritesNoSSHConfig pins that without a
// bastion codebox keeps the plain user@localhost:<port> authority and
// never touches ~/.ssh.
func TestVSCode_Remote_DirectConnectionWritesNoSSHConfig(t *testing.T) {
	home := t.TempDir()
	a, fr := newAppWithHome(t, home,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{stdout: "\n"},
		reply{},
	)
	err := a.VSCode(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, app.VSCodeRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err != nil {
		t.Fatalf("VSCode: %v", err)
	}
	wantURI := "vscode-remote://ssh-remote+user@localhost:33000/home/user/source"
	if fr.calls[3].cmd != "code --folder-uri '"+wantURI+"'" {
		t.Errorf("direct connection should use user@localhost authority; got %q", fr.calls[3].cmd)
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", "codebox_config")); !os.IsNotExist(err) {
		t.Errorf("direct connection must not write codebox_config (stat err=%v)", err)
	}
}

// TestVSCode_Remote_NotExposingPort pins the not-running diagnostic.
func TestVSCode_Remote_NotExposingPort(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "\n"}, // port — nothing mapped
	)
	err := a.VSCode(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, app.VSCodeRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err == nil || !strings.Contains(err.Error(), "not exposing port") {
		t.Fatalf("expected not-exposing-port error, got: %v", err)
	}
}

// TestVSCode_Remote_CodeNotOnPath pins the launch-failure hint.
func TestVSCode_Remote_CodeNotOnPath(t *testing.T) {
	t.Parallel()
	a, _ := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "demo\n"},
		reply{stdout: "0.0.0.0:33000\n"},
		reply{stdout: "\n"},
		reply{stderr: "sh: code: not found\n", err: &exec.ExitError{}},
	)
	err := a.VSCode(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, app.VSCodeRequest{
		Instance:     "demo",
		Orchestrator: "podman",
	})
	if err == nil || !strings.Contains(err.Error(), "launch VS Code") {
		t.Fatalf("expected launch-failure error, got: %v", err)
	}
}

// TestVSCode_Mounted_MountsThenOpens pins the VS-Code-remote branch: when
// the local mount directory is missing or empty, ~/source is mounted
// (full sshfs sequence), then `code` is pointed at the local mount path.
func TestVSCode_Mounted_MountsThenOpens(t *testing.T) {
	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "/usr/bin/sshfs\n"}, // Mount: command -v sshfs
		reply{stdout: ""},                 // Mount: cat /proc/mounts
		reply{stdout: "demo\n"},           // Mount: ps -a
		reply{stdout: "0.0.0.0:33000\n"},  // Mount: port lookup
		reply{},                           // Mount: ssh mkdir ~/source
		reply{},                           // Mount: sshfs
		reply{},                           // code launch
	)
	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	err := a.VSCode(context.Background(), &stdout, &stderr, app.VSCodeRequest{
		Instance:           "demo",
		Orchestrator:       "podman",
		InsideVSCodeRemote: true,
	})
	if err != nil {
		t.Fatalf("VSCode: %v", err)
	}

	wantDir := filepath.Join(cwd, ".codebox", "demo")
	last := fr.calls[len(fr.calls)-1].cmd
	if last != "code '"+wantDir+"'" {
		t.Errorf("expected code to open the mount dir; got %q", last)
	}
	if !strings.Contains(stdout.String(), "is not mounted") {
		t.Errorf("operator should see the mount notice; got:\n%s", stdout.String())
	}
}

// TestVSCode_Mounted_EmptyDirRemounts pins the contract that an
// existing-but-empty mount directory is treated as "not mounted" and
// triggers a fresh mount — distinguishing the directory-contents check
// from a bare "does the directory exist" test.
func TestVSCode_Mounted_EmptyDirRemounts(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	mountTarget := filepath.Join(cwd, ".codebox", "demo")
	if err := os.MkdirAll(mountTarget, 0o755); err != nil {
		t.Fatalf("mkdir empty mount target: %v", err)
	}

	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{stdout: "/usr/bin/sshfs\n"}, // Mount: command -v sshfs
		reply{stdout: ""},                 // Mount: cat /proc/mounts
		reply{stdout: "demo\n"},           // Mount: ps -a
		reply{stdout: "0.0.0.0:33000\n"},  // Mount: port lookup
		reply{},                           // Mount: ssh mkdir ~/source
		reply{},                           // Mount: sshfs
		reply{},                           // code launch
	)

	var stdout, stderr bytes.Buffer
	err := a.VSCode(context.Background(), &stdout, &stderr, app.VSCodeRequest{
		Instance:           "demo",
		Orchestrator:       "podman",
		InsideVSCodeRemote: true,
	})
	if err != nil {
		t.Fatalf("VSCode: %v", err)
	}
	if !strings.Contains(stdout.String(), "is not mounted") {
		t.Errorf("an empty mount dir should trigger a mount; got:\n%s", stdout.String())
	}
	if got := len(fr.calls); got != 7 {
		t.Fatalf("empty dir should run the full mount sequence; got %d calls: %+v", got, fr.calls)
	}
}

// TestVSCode_Mounted_AlreadyMountedReusesAndOpensWorkspace pins that a
// non-empty mount directory is reused (no sshfs work) and a workspace
// file present on the mounted path is opened.
func TestVSCode_Mounted_AlreadyMountedReusesAndOpensWorkspace(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	mountTarget := filepath.Join(cwd, ".codebox", "demo")

	a, fr := newApp(t,
		&stubKeys{key: "k"},
		reply{}, // code launch
	)
	// Place a workspace file on the (pretend) mount so the directory is
	// non-empty (reuse, no mount) and the glob finds it.
	if err := os.MkdirAll(mountTarget, 0o755); err != nil {
		t.Fatalf("mkdir mount target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mountTarget, "proj.code-workspace"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	var stdout bytes.Buffer
	err := a.VSCode(context.Background(), &stdout, &bytes.Buffer{}, app.VSCodeRequest{
		Instance:           "demo",
		Orchestrator:       "podman",
		InsideVSCodeRemote: true,
	})
	if err != nil {
		t.Fatalf("VSCode: %v", err)
	}
	if got := len(fr.calls); got != 1 {
		t.Fatalf("already-mounted should skip sshfs work; got %d calls: %+v", got, fr.calls)
	}
	wantTarget := filepath.Join(mountTarget, "proj.code-workspace")
	if fr.calls[0].cmd != "code '"+wantTarget+"'" {
		t.Errorf("expected code to open the workspace file; got %q", fr.calls[0].cmd)
	}
	if !strings.Contains(stdout.String(), "already mounted at") {
		t.Errorf("operator should see the already-mounted notice; got:\n%s", stdout.String())
	}
}

// TestVSCode_InsidePlainSSH_Refuses pins case 2: when codebox runs inside
// an SSH session that is not a VS Code Remote-SSH terminal
// (InsideSSHRemote without InsideVSCodeRemote), there is no local editor to
// drive, so the command refuses with an actionable message and never
// touches the runner.
func TestVSCode_InsidePlainSSH_Refuses(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})

	err := a.VSCode(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, app.VSCodeRequest{
		Instance:        "demo",
		Orchestrator:    "podman",
		InsideSSHRemote: true,
	})
	if err == nil || !strings.Contains(err.Error(), "plain SSH session") {
		t.Fatalf("expected a plain-SSH refusal, got: %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("refusal must not invoke the runner; got %d calls: %+v", len(fr.calls), fr.calls)
	}
}

func TestVSCode_RejectsInvalidInstanceName(t *testing.T) {
	t.Parallel()
	a, fr := newApp(t, &stubKeys{key: "k"})
	err := a.VSCode(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, app.VSCodeRequest{
		Instance:     "bad name",
		Orchestrator: "podman",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("expected invalid character error, got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner should not be invoked for invalid name")
	}
}
