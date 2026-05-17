package image_test

import (
	"bytes"
	"strings"
	"testing"

	"codebox/internal/image"
)

const testKey = "ssh-ed25519 AAAATESTKEY operator@host"

func generate(t *testing.T, osKey string) string {
	t.Helper()
	var b bytes.Buffer
	if err := image.Generate(&b, image.Options{OS: osKey, AuthorizedKey: testKey}); err != nil {
		t.Fatalf("Generate(%s): %v", osKey, err)
	}
	return b.String()
}

func TestSupportedOS(t *testing.T) {
	t.Parallel()
	got := image.SupportedOS()
	want := []string{"debian_12", "debian_13", "redhat_10", "ubuntu_24", "ubuntu_26"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("SupportedOS = %v, want %v", got, want)
	}
}

func TestGenerate_BaseImagePerOS(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"debian_12": "FROM docker.io/debian:12.13",
		"debian_13": "FROM docker.io/debian:13.4",
		"ubuntu_24": "FROM docker.io/ubuntu:24.04",
		"ubuntu_26": "FROM docker.io/ubuntu:26.04",
		"redhat_10": "FROM docker.io/redhat/ubi10:10.1",
	}
	for osKey, want := range cases {
		t.Run(osKey, func(t *testing.T) {
			t.Parallel()
			out := generate(t, osKey)
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in:\n%s", want, out)
			}
		})
	}
}

func TestGenerate_SyntaxDirectiveIsFirstLine(t *testing.T) {
	t.Parallel()
	out := generate(t, "debian_13")
	if !strings.HasPrefix(out, "# syntax=docker/dockerfile:1.7\n") {
		t.Fatalf("first line must be the syntax directive:\n%s", out)
	}
}

func TestGenerate_AptUsesCanonicalNames(t *testing.T) {
	t.Parallel()
	out := generate(t, "debian_13")
	for _, p := range []string{"apt-get install", "iputils-ping", "dnsutils", "git"} {
		if !strings.Contains(out, p) {
			t.Errorf("debian_13 missing %q", p)
		}
	}
	if strings.Contains(out, " iputils ") || strings.Contains(out, "bind-utils") {
		t.Errorf("debian_13 should not use Red Hat package names:\n%s", out)
	}
}

func TestGenerate_DnfRemapsPackageNames(t *testing.T) {
	t.Parallel()
	out := generate(t, "redhat_10")
	for _, p := range []string{"dnf install", "iputils", "bind-utils", "git"} {
		if !strings.Contains(out, p) {
			t.Errorf("redhat_10 missing %q", p)
		}
	}
	if strings.Contains(out, "iputils-ping") || strings.Contains(out, "dnsutils") {
		t.Errorf("redhat_10 should not contain Debian package names:\n%s", out)
	}
}

// TestGenerate_BuildToolchainPerFamily pins the build toolchain to the
// distro: apt-family OSes install build-essential alongside the base
// packages; dnf-family OSes ship without a build toolchain.
func TestGenerate_BuildToolchainPerFamily(t *testing.T) {
	t.Parallel()
	for _, osKey := range []string{"debian_12", "debian_13", "ubuntu_24", "ubuntu_26"} {
		out := generate(t, osKey)
		if !strings.Contains(out, "build-essential") {
			t.Errorf("%s missing build-essential", osKey)
		}
	}
	if out := generate(t, "redhat_10"); strings.Contains(out, "build-essential") {
		t.Errorf("redhat_10 should not reference build-essential:\n%s", out)
	}
}

func TestGenerate_PamSudoFixOnlyOnTargetedOS(t *testing.T) {
	t.Parallel()
	const marker = "/etc/pam.d/sudo"
	for _, osKey := range []string{"debian_13", "ubuntu_26", "redhat_10"} {
		if !strings.Contains(generate(t, osKey), marker) {
			t.Errorf("%s expected pam.d/sudo fix", osKey)
		}
	}
	for _, osKey := range []string{"debian_12", "ubuntu_24"} {
		if strings.Contains(generate(t, osKey), marker) {
			t.Errorf("%s should not include pam.d/sudo fix", osKey)
		}
	}
}

// TestGenerate_BuildOrder pins the section order to the spec — package
// install must come before user creation, sshd, sudoers, init, key, and
// EXPOSE — so cache invalidation behaves predictably.
func TestGenerate_BuildOrder(t *testing.T) {
	t.Parallel()
	out := generate(t, "debian_13")
	sections := []string{
		"FROM ",
		"# Base packages.",
		"# Create user \"user\"",
		"# Configure sshd",
		"# Passwordless sudo",
		"# Init script",
		"# Install the operator's public key.",
		"EXPOSE 2222",
		`CMD ["/usr/local/bin/codebox-init"]`,
	}
	prev := -1
	for _, s := range sections {
		i := strings.Index(out, s)
		if i == -1 {
			t.Fatalf("missing section %q\n%s", s, out)
		}
		if i <= prev {
			t.Fatalf("section %q out of order\n%s", s, out)
		}
		prev = i
	}
}

func TestGenerate_AuthorizedKeyEmbedded(t *testing.T) {
	t.Parallel()
	out := generate(t, "debian_13")
	if !strings.Contains(out, testKey) {
		t.Fatalf("authorized key not embedded:\n%s", out)
	}
	if !strings.Contains(out, "COPY --chown=user:user --chmod=0600 <<EOF /home/user/.ssh/authorized_keys") {
		t.Fatalf("authorized_keys COPY line missing or malformed:\n%s", out)
	}
}

func TestGenerate_GeneratesHostKeys(t *testing.T) {
	t.Parallel()
	for _, osKey := range []string{"debian_12", "debian_13", "ubuntu_24", "ubuntu_26", "redhat_10"} {
		out := generate(t, osKey)
		if !strings.Contains(out, "ssh-keygen -A") {
			t.Errorf("%s missing ssh-keygen -A for host-key generation", osKey)
		}
	}
}

func TestGenerate_SshdConfigDPath(t *testing.T) {
	t.Parallel()
	out := generate(t, "debian_13")
	if !strings.Contains(out, "/etc/ssh/sshd_config.d/10-codebox.conf") {
		t.Fatalf("expected sshd_config.d drop-in at priority 10:\n%s", out)
	}
	for _, line := range []string{
		"Port 2222",
		"PubkeyAuthentication yes",
		"PasswordAuthentication no",
		"UsePAM no",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("missing sshd directive %q", line)
		}
	}
}

func TestGenerate_UnknownOSIsRejected(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	err := image.Generate(&b, image.Options{OS: "freebsd_14", AuthorizedKey: testKey})
	if err == nil {
		t.Fatal("Generate must reject an unknown OS")
	}
	if !strings.Contains(err.Error(), "unsupported os") {
		t.Errorf("error message should explain the problem; got %v", err)
	}
}

func TestGenerate_EmptyKeyIsRejected(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := image.Generate(&b, image.Options{OS: "debian_13"}); err == nil {
		t.Fatal("Generate must reject an empty authorized key")
	}
}
