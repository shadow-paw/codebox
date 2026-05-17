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
	pkg(canonical string) string
}

type aptFamily struct{}

func (aptFamily) installLine(pkgs []string) string {
	all := append(append([]string{}, pkgs...), "build-essential")
	return "apt-get update && \\\n" +
		"    apt-get install -y --no-install-recommends \\\n" +
		"        " + strings.Join(all, " ") + " && \\\n" +
		"    rm -rf /var/lib/apt/lists/*"
}

func (aptFamily) pkg(canonical string) string { return canonical }

type dnfFamily struct{}

func (dnfFamily) installLine(pkgs []string) string {
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
