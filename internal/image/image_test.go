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

func generateOpts(t *testing.T, opts image.Options) string {
	t.Helper()
	if opts.AuthorizedKey == "" {
		opts.AuthorizedKey = testKey
	}
	var b bytes.Buffer
	if err := image.Generate(&b, opts); err != nil {
		t.Fatalf("Generate(%+v): %v", opts, err)
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

// TestGenerate_ExtrasOmittedByDefault pins the no-extras baseline: with
// no language/tool flags set, the Dockerfile must not mention any of
// the optional installers.
func TestGenerate_ExtrasOmittedByDefault(t *testing.T) {
	t.Parallel()
	out := generate(t, "debian_13")
	for _, marker := range []string{
		"nvm", "uv/install.sh", "dotnet-install.sh",
		"go.dev/dl/go", "postgresql-client", "DOTNET_CLI_TELEMETRY_OPTOUT",
		"libicu", "USER user", "USER root",
	} {
		if strings.Contains(out, marker) {
			t.Errorf("default Dockerfile should not mention %q\n%s", marker, out)
		}
	}
}

func TestGenerate_PsqlInstall(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"debian_13": "postgresql-client",
		"redhat_10": "postgresql",
	}
	for osKey, want := range cases {
		osKey, want := osKey, want
		t.Run(osKey, func(t *testing.T) {
			t.Parallel()
			out := generateOpts(t, image.Options{OS: osKey, Psql: true})
			if !strings.Contains(out, want) {
				t.Fatalf("%s with --psql should install %q\n%s", osKey, want, out)
			}
		})
	}
}

func TestGenerate_GolangInstall(t *testing.T) {
	t.Parallel()
	out := generateOpts(t, image.Options{OS: "debian_13", Golang: "1.26.0"})
	wants := []string{
		"# Install Go 1.26.0",
		"https://go.dev/dl/go1.26.0.linux-${arch}.tar.gz",
		`case "$arch" in x86_64) arch=amd64;; aarch64) arch=arm64;;`,
		"tar -C /usr/local -xz",
		`echo 'export PATH="/usr/local/go/bin:$PATH"' >> /home/user/.profile`,
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("Go layer missing %q\n%s", want, out)
		}
	}
}

func TestGenerate_DotnetInstall(t *testing.T) {
	t.Parallel()
	out := generateOpts(t, image.Options{OS: "debian_13", Dotnet: "10"})
	wants := []string{
		"# Install .NET 10",
		"https://dot.net/v1/dotnet-install.sh",
		"--channel 10.0 --install-dir /usr/local/dotnet",
		"ln -sf /usr/local/dotnet/dotnet /usr/local/bin/dotnet",
		`echo 'export DOTNET_ROOT="/usr/local/dotnet"' >> /home/user/.profile`,
		"echo 'export DOTNET_CLI_TELEMETRY_OPTOUT=1' >> /home/user/.profile",
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf(".NET layer missing %q\n%s", want, out)
		}
	}
}

// TestGenerate_DotnetInstallsIcu pins the runtime-library install for the
// .NET SDK: apt-family distros pull libicu-dev, dnf-family pulls libicu.
// Without ICU the SDK aborts on first run with a globalization error.
func TestGenerate_DotnetInstallsIcu(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"debian_13": "libicu-dev",
		"redhat_10": "libicu",
	}
	for osKey, want := range cases {
		osKey, want := osKey, want
		t.Run(osKey, func(t *testing.T) {
			t.Parallel()
			out := generateOpts(t, image.Options{OS: osKey, Dotnet: "10"})
			if !strings.Contains(out, want) {
				t.Fatalf("%s with --dotnet should install %q\n%s", osKey, want, out)
			}
		})
	}
}

func TestGenerate_PythonInstall(t *testing.T) {
	t.Parallel()
	out := generateOpts(t, image.Options{OS: "debian_13", Python: "3.13"})
	wants := []string{
		"USER user",
		"curl -LsSf https://astral.sh/uv/install.sh | sh",
		`echo 'export PATH="$HOME/.local/bin:$PATH"' >> /home/user/.profile`,
		"/home/user/.local/bin/uv python install 3.13",
		"/home/user/.local/bin/uv python pin --global 3.13",
		"USER root",
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("Python layer missing %q\n%s", want, out)
		}
	}
}

// TestGenerate_ProfilePathPerFamily pins the per-family login-profile
// path: apt-family distros append exports to .profile, dnf-family
// distros append to .bash_profile so Red Hat login shells pick them up.
func TestGenerate_ProfilePathPerFamily(t *testing.T) {
	t.Parallel()
	cases := []struct {
		osKey, profile, banned string
	}{
		{"debian_13", "/home/user/.profile", "/home/user/.bash_profile"},
		{"redhat_10", "/home/user/.bash_profile", "/home/user/.profile "},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.osKey, func(t *testing.T) {
			t.Parallel()
			out := generateOpts(t, image.Options{
				OS: tc.osKey, Python: "3.13", Golang: "1.26.0", Dotnet: "10",
			})
			if !strings.Contains(out, tc.profile) {
				t.Errorf("%s should append exports to %s\n%s", tc.osKey, tc.profile, out)
			}
			if strings.Contains(out, tc.banned) {
				t.Errorf("%s should not write to %s\n%s", tc.osKey, tc.banned, out)
			}
		})
	}
}

// TestGenerate_PythonInstall_NoSystemBuildDeps pins the fact that the
// uv-based Python install does not pull C build dependencies on either
// family: uv ships prebuilt CPython binaries, so distro-level build
// prerequisites are not needed.
func TestGenerate_PythonInstall_NoSystemBuildDeps(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"debian_13": {"libssl-dev", "libffi-dev", "libreadline-dev", "libsqlite3-dev"},
		"redhat_10": {"openssl-devel", "libffi-devel", "readline-devel", "gdbm-devel", "rpmfind"},
	}
	for osKey, banned := range cases {
		osKey, banned := osKey, banned
		t.Run(osKey, func(t *testing.T) {
			t.Parallel()
			out := generateOpts(t, image.Options{OS: osKey, Python: "3.13"})
			for _, b := range banned {
				if strings.Contains(out, b) {
					t.Errorf("%s Python layer should not pull %q\n%s", osKey, b, out)
				}
			}
		})
	}
}

func TestGenerate_NodeInstall(t *testing.T) {
	t.Parallel()
	out := generateOpts(t, image.Options{OS: "debian_13", Node: "24"})
	wants := []string{
		"USER user",
		"raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh",
		". /home/user/.nvm/nvm.sh && nvm install 24 && nvm alias default 24",
		"USER root",
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("Node layer missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "libatomic") {
		t.Errorf("apt-family Node layer must not pull libatomic (provided by libc6)\n%s", out)
	}
}

// TestGenerate_NodeInstallsLibatomicOnDnf pins the Red Hat-specific
// libatomic install. UBI does not ship libatomic, and recent V8 binaries
// the nvm-managed Node distributes link against it, so prebuilt installs
// fault on first run without the package.
func TestGenerate_NodeInstallsLibatomicOnDnf(t *testing.T) {
	t.Parallel()
	out := generateOpts(t, image.Options{OS: "redhat_10", Node: "24"})
	if !strings.Contains(out, "libatomic") {
		t.Fatalf("redhat_10 with --node should install libatomic\n%s", out)
	}
}

// TestGenerate_ExtrasPosition pins the extras block to its slot —
// after the init script and before the operator key install — so that
// USER user has a valid home and cache invalidation behaves predictably.
func TestGenerate_ExtrasPosition(t *testing.T) {
	t.Parallel()
	out := generateOpts(t, image.Options{
		OS: "debian_13", Python: "3.13", Node: "24",
		Golang: "1.26.0", Dotnet: "10", Psql: true,
	})
	sections := []string{
		"# Init script",
		"# System packages for the requested toolchains.",
		"# Install Go 1.26.0",
		"# Install .NET 10",
		"USER user",
		"# Install uv and pin the requested Python version globally.",
		"# Install nvm",
		"USER root",
		"# Install the operator's public key.",
		"EXPOSE 2222",
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

// TestGenerate_UserSwitchOnlyWhenNeeded keeps the USER user/root pair
// off the Dockerfile when neither uv nor nvm is requested, since the
// Go/.NET/psql layers all run as root.
func TestGenerate_UserSwitchOnlyWhenNeeded(t *testing.T) {
	t.Parallel()
	out := generateOpts(t, image.Options{
		OS: "debian_13", Golang: "1.26.0", Dotnet: "10", Psql: true,
	})
	if strings.Contains(out, "USER user") {
		t.Errorf("USER user should be absent when no home-scoped install is enabled\n%s", out)
	}
}

// TestGenerate_RejectsUnsupportedVersions pins the enum check on each
// optional language toolchain: values outside the documented set fail
// before any Dockerfile is emitted, and the error names both the flag
// and the supported alternatives.
func TestGenerate_RejectsUnsupportedVersions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label string
		opts  image.Options
		flag  string
		known string
	}{
		{"python", image.Options{OS: "debian_13", Python: "3.10"}, "python", "3.12, 3.13, 3.14"},
		{"node", image.Options{OS: "debian_13", Node: "20"}, "node", "24, 25, 26"},
		{"golang", image.Options{OS: "debian_13", Golang: "1.25.0"}, "golang", "1.26.0"},
		{"dotnet", image.Options{OS: "debian_13", Dotnet: "6"}, "dotnet", "8, 10"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			opts := tc.opts
			opts.AuthorizedKey = testKey
			var b bytes.Buffer
			err := image.Generate(&b, opts)
			if err == nil {
				t.Fatalf("Generate must reject unsupported %s version", tc.flag)
			}
			for _, want := range []string{"unsupported " + tc.flag + " version", tc.known} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should contain %q", err.Error(), want)
				}
			}
			if b.Len() != 0 {
				t.Errorf("Dockerfile must not be written when validation fails; got %d bytes", b.Len())
			}
		})
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
