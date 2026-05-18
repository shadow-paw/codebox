package image

import "strings"

// spec captures the per-OS data the renderer needs. A new OS is added by
// appending an entry to specs; the renderer is otherwise generic.
type spec struct {
	baseImage       string
	family          family
	needsPamSudoFix bool
	hasSshdConfigD  bool
}

// family abstracts a distro's package manager and any per-distro
// package-name remapping. Canonical names are the Debian-flavour ones;
// dnfFamily.pkg remaps the entries that differ on Red Hat.
type family interface {
	installLine(pkgs []string) string
	extraInstallLine(pkgs []string) string
	pkg(canonical string) string
	nodeDeps() []string
	dotnetDeps() []string
	psqlPkg() string
	profilePath() string
}

type aptFamily struct{}

func (aptFamily) installLine(pkgs []string) string {
	all := append(append([]string{}, pkgs...), "build-essential")
	return aptInstall(all)
}

func (aptFamily) extraInstallLine(pkgs []string) string {
	return aptInstall(pkgs)
}

func (aptFamily) pkg(canonical string) string { return canonical }

// nodeDeps returns the extra system packages the Node toolchain needs on
// apt-family distros. libc6 already provides libatomic, so the list is
// empty; the method exists to keep the family interface symmetric.
func (aptFamily) nodeDeps() []string { return nil }

// dotnetDeps lists the runtime libraries the .NET SDK needs on apt-family
// distros. ICU powers globalization APIs and is not pulled in transitively
// by the dotnet-install.sh script.
func (aptFamily) dotnetDeps() []string {
	return []string{"libicu-dev"}
}

func (aptFamily) psqlPkg() string { return "postgresql-client" }

// profilePath returns the login-shell rc file useradd creates from
// /etc/skel on apt-family distros. /home/user/.profile is sourced by
// bash login shells on Debian and Ubuntu.
func (aptFamily) profilePath() string { return "/home/user/.profile" }

func aptInstall(pkgs []string) string {
	return "apt-get update && \\\n" +
		"    apt-get install -y --no-install-recommends \\\n" +
		"        " + strings.Join(pkgs, " ") + " && \\\n" +
		"    rm -rf /var/lib/apt/lists/*"
}

type dnfFamily struct{}

func (dnfFamily) installLine(pkgs []string) string {
	return "dnf install -y \\\n" +
		"        " + strings.Join(pkgs, " ") + " && \\\n" +
		"    dnf clean all"
}

func (dnfFamily) extraInstallLine(pkgs []string) string {
	return "dnf install -y \\\n" +
		"        " + strings.Join(pkgs, " ") + " && \\\n" +
		"    dnf clean all"
}

func (dnfFamily) pkg(canonical string) string {
	switch canonical {
	case "iputils-ping":
		return "iputils"
	case "dnsutils":
		return "bind-utils"
	default:
		return canonical
	}
}

// nodeDeps lists the system packages the Node toolchain needs on
// dnf-family distros. UBI ships without libatomic, which the V8 binaries
// in recent Node releases link against, so prebuilt installs crash on
// first use without it.
func (dnfFamily) nodeDeps() []string {
	return []string{"libatomic"}
}

// dotnetDeps lists the runtime libraries the .NET SDK needs on dnf-family
// distros. libicu is the Red Hat counterpart to apt's libicu-dev.
func (dnfFamily) dotnetDeps() []string {
	return []string{"libicu"}
}

func (dnfFamily) psqlPkg() string { return "postgresql" }

// profilePath returns the login-shell rc file useradd creates from
// /etc/skel on dnf-family distros. Red Hat-derived bash login shells
// read /home/user/.bash_profile, not .profile.
func (dnfFamily) profilePath() string { return "/home/user/.bash_profile" }

// basePackages is the canonical set installed into every sandbox image.
// Anything OS-specific is handled by family.pkg; family.installLine adds
// the distro's build toolchain (apt installs build-essential).
var basePackages = []string{
	"ca-certificates",
	"nano", "vim", "sudo",
	"openssl", "openssh-server", "rsync", "git",
	"iputils-ping", "dnsutils", "curl",
}

var (
	apt = aptFamily{}
	dnf = dnfFamily{}
)

// specs lists every supported OS. New OS keys go here.
var specs = map[string]spec{
	"debian_12": {baseImage: "docker.io/debian:12.13", family: apt, hasSshdConfigD: true},
	"debian_13": {baseImage: "docker.io/debian:13.4", family: apt, needsPamSudoFix: true, hasSshdConfigD: true},
	"ubuntu_24": {baseImage: "docker.io/ubuntu:24.04", family: apt, hasSshdConfigD: true},
	"ubuntu_26": {baseImage: "docker.io/ubuntu:26.04", family: apt, needsPamSudoFix: true, hasSshdConfigD: true},
	"redhat_10": {baseImage: "docker.io/redhat/ubi10:10.1", family: dnf, needsPamSudoFix: true, hasSshdConfigD: true},
}

// Supported version sets for the optional toolchains. Generate rejects
// values not listed here so unknown versions fail fast with a clear
// message instead of producing an image that fails to build.
var (
	supportedPython = []string{"3.12", "3.13", "3.14"}
	supportedNode   = []string{"24", "25", "26"}
	supportedGolang = []string{"1.26.0"}
	supportedDotnet = []string{"8", "10"}
)
