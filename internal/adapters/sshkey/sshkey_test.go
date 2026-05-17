package sshkey_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codebox/internal/adapters/sshkey"
)

func writeKey(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestResolve_ExplicitPubPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const body = "ssh-ed25519 AAAA explicit\n"
	pub := writeKey(t, dir, "id_ed25519.pub", body)

	got, err := sshkey.NewWithDir(dir).Resolve(pub)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != strings.TrimSpace(body) {
		t.Fatalf("Resolve = %q, want %q", got, strings.TrimSpace(body))
	}
}

func TestResolve_PrivatePathAppendsPubSuffix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const body = "ssh-rsa AAAA suffix\n"
	writeKey(t, dir, "id_rsa.pub", body)

	priv := filepath.Join(dir, "id_rsa")
	got, err := sshkey.NewWithDir(dir).Resolve(priv)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != strings.TrimSpace(body) {
		t.Fatalf("Resolve = %q, want %q", got, strings.TrimSpace(body))
	}
}

func TestResolve_AutoDetectsSingleKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const body = "ssh-ed25519 AAAA auto"
	writeKey(t, dir, "id_ed25519.pub", body)
	writeKey(t, dir, "id_ed25519", "PRIVATE")
	writeKey(t, dir, "known_hosts", "host ssh-rsa AAAA noise")

	got, err := sshkey.NewWithDir(dir).Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != body {
		t.Fatalf("Resolve = %q, want %q", got, body)
	}
}

func TestResolve_AutoDetectMultiplePubsErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeKey(t, dir, "id_rsa.pub", "ssh-rsa AAAA one")
	writeKey(t, dir, "id_ed25519.pub", "ssh-ed25519 AAAA two")

	_, err := sshkey.NewWithDir(dir).Resolve("")
	if err == nil {
		t.Fatal("Resolve must error when multiple .pub files exist")
	}
	for _, want := range []string{"2 public keys", "id_ed25519.pub", "id_rsa.pub", "--instance-key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestResolve_AutoDetectNoPubErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeKey(t, dir, "known_hosts", "host ssh-rsa AAAA noise")

	_, err := sshkey.NewWithDir(dir).Resolve("")
	if err == nil {
		t.Fatal("Resolve must error when no .pub files exist")
	}
	if !strings.Contains(err.Error(), "no public keys") {
		t.Errorf("error %q should mention no public keys", err.Error())
	}
}

func TestResolve_MissingFileSurfaces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := sshkey.NewWithDir(dir).Resolve(filepath.Join(dir, "absent"))
	if err == nil {
		t.Fatal("Resolve must error when file is missing")
	}
}
