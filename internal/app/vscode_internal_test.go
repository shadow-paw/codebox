package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureVSCodeSSHHost_Idempotent pins that re-registering an
// instance's alias rewrites its block in place (picking up a new port)
// and never duplicates either the Host block or the Include line.
func TestEnsureVSCodeSSHHost_Idempotent(t *testing.T) {
	home := t.TempDir()
	a := &App{home: home}

	if _, err := a.ensureVSCodeSSHHost("demo", "33000", "/keys/k", "ops@bastion"); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	// Second run with a different port — the block must be replaced.
	if _, err := a.ensureVSCodeSSHHost("demo", "44000", "/keys/k", "ops@bastion"); err != nil {
		t.Fatalf("second ensure: %v", err)
	}

	frag := mustRead(t, filepath.Join(home, ".ssh", vscodeIncludeFile))
	if n := strings.Count(frag, "Host codebox-demo"); n != 1 {
		t.Errorf("expected exactly one Host block, got %d:\n%s", n, frag)
	}
	if strings.Contains(frag, "Port 33000") || !strings.Contains(frag, "Port 44000") {
		t.Errorf("block should reflect the new port 44000, not the stale 33000:\n%s", frag)
	}

	cfg := mustRead(t, filepath.Join(home, ".ssh", "config"))
	if n := strings.Count(cfg, "Include "+vscodeIncludeFile); n != 1 {
		t.Errorf("expected exactly one Include line, got %d:\n%s", n, cfg)
	}
}

// TestEnsureVSCodeSSHHost_PreservesExistingConfig pins that a
// hand-written ~/.ssh/config is preserved and the Include is prepended
// ahead of it (so the managed host wins ssh's first-match resolution).
func TestEnsureVSCodeSSHHost_PreservesExistingConfig(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const handWritten = "Host myserver\n    HostName example.com\n"
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(handWritten), 0o600); err != nil {
		t.Fatal(err)
	}

	a := &App{home: home}
	if _, err := a.ensureVSCodeSSHHost("demo", "33000", "", "ops@bastion"); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	cfg := mustRead(t, filepath.Join(sshDir, "config"))
	if !strings.Contains(cfg, handWritten) {
		t.Errorf("hand-written host should be preserved; got:\n%s", cfg)
	}
	if strings.Index(cfg, "Include "+vscodeIncludeFile) > strings.Index(cfg, "Host myserver") {
		t.Errorf("Include should be prepended ahead of existing hosts; got:\n%s", cfg)
	}

	// No --instance-key means the block omits IdentityFile.
	frag := mustRead(t, filepath.Join(sshDir, vscodeIncludeFile))
	if strings.Contains(frag, "IdentityFile") {
		t.Errorf("no key supplied: block should omit IdentityFile; got:\n%s", frag)
	}
}

// TestRemoveVSCodeSSHHost_EmptiesFileAndDropsInclude pins that deleting
// the only registered instance removes its block, deletes the now-empty
// fragment, and strips the Include line from ~/.ssh/config — while the
// operator's hand-written hosts survive.
func TestRemoveVSCodeSSHHost_EmptiesFileAndDropsInclude(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"),
		[]byte("Host myserver\n    HostName example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := &App{home: home}
	if _, err := a.ensureVSCodeSSHHost("demo", "33000", "/keys/k", "ops@bastion"); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	var out strings.Builder
	if err := a.removeVSCodeSSHHost(&out, "demo"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(out.String(), "Removing VS Code ssh alias \"codebox-demo\"") {
		t.Errorf("expected a removal notice; got:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(sshDir, vscodeIncludeFile)); !os.IsNotExist(err) {
		t.Errorf("emptied fragment should be deleted; stat err=%v", err)
	}
	cfg := mustRead(t, filepath.Join(sshDir, "config"))
	if strings.Contains(cfg, "Include "+vscodeIncludeFile) {
		t.Errorf("Include line should be gone; got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "Host myserver") {
		t.Errorf("hand-written host must be preserved; got:\n%s", cfg)
	}
}

// TestRemoveVSCodeSSHHost_KeepsOtherInstances pins that deleting one
// instance leaves a co-registered instance's block, the fragment file,
// and the Include line all intact.
func TestRemoveVSCodeSSHHost_KeepsOtherInstances(t *testing.T) {
	home := t.TempDir()
	a := &App{home: home}
	if _, err := a.ensureVSCodeSSHHost("demo", "33000", "/keys/k", "ops@bastion"); err != nil {
		t.Fatalf("ensure demo: %v", err)
	}
	if _, err := a.ensureVSCodeSSHHost("other", "44000", "/keys/k", "ops@bastion"); err != nil {
		t.Fatalf("ensure other: %v", err)
	}

	if err := a.removeVSCodeSSHHost(&strings.Builder{}, "demo"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	frag := mustRead(t, filepath.Join(home, ".ssh", vscodeIncludeFile))
	if strings.Contains(frag, "Host codebox-demo") {
		t.Errorf("demo block should be gone; got:\n%s", frag)
	}
	if !strings.Contains(frag, "Host codebox-other") {
		t.Errorf("other block must survive; got:\n%s", frag)
	}
	cfg := mustRead(t, filepath.Join(home, ".ssh", "config"))
	if !strings.Contains(cfg, "Include "+vscodeIncludeFile) {
		t.Errorf("Include should remain while a block survives; got:\n%s", cfg)
	}
}

// TestRemoveVSCodeSSHHost_NoFragmentIsQuietNoop pins that deleting an
// instance that never registered an alias touches nothing and prints
// nothing — the path every non-bastion delete takes.
func TestRemoveVSCodeSSHHost_NoFragmentIsQuietNoop(t *testing.T) {
	home := t.TempDir()
	a := &App{home: home}

	var out strings.Builder
	if err := a.removeVSCodeSSHHost(&out, "demo"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("no-op removal should be silent; got:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", vscodeIncludeFile)); !os.IsNotExist(err) {
		t.Errorf("removal must not create a fragment; stat err=%v", err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
