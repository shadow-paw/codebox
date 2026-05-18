package image

import (
	"fmt"
	"strings"
)

// render returns the Dockerfile text for spec s with authKey embedded
// as the operator's authorized_keys entry. The build order matches the
// codebox spec: packages first (cache-friendly), OS fixes, user, sshd,
// sudoers, init script, optional language/tool layers, then the
// operator's key, EXPOSE, and CMD.
func render(s spec, authKey string, opts Options) string {
	pkgs := make([]string, len(basePackages))
	for i, p := range basePackages {
		pkgs[i] = s.family.pkg(p)
	}

	var b strings.Builder
	// The syntax directive must be the first line of the Dockerfile so
	// BuildKit picks up the modern parser (heredocs, COPY --chmod, ...).
	b.WriteString("# syntax=docker/dockerfile:1.7\n")
	fmt.Fprintf(&b, "FROM %s\n\n", s.baseImage)

	b.WriteString("# Base packages.\n")
	fmt.Fprintf(&b, "RUN %s\n\n", s.family.installLine(pkgs))

	if s.needsPamSudoFix {
		b.WriteString("# Relax /etc/pam.d/sudo for container-friendly passwordless sudo.\n")
		b.WriteString("COPY <<EOF /etc/pam.d/sudo\n")
		b.WriteString("auth       sufficient   pam_permit.so\n")
		b.WriteString("account    sufficient   pam_permit.so\n")
		b.WriteString("session    required     pam_limits.so\n")
		b.WriteString("EOF\n\n")
	}

	b.WriteString("# Create user \"user\" with a locked password slot, then unlock the account.\n")
	b.WriteString("RUN useradd -m -s /bin/bash user && \\\n")
	b.WriteString("    usermod -p '*NP' user\n\n")

	b.WriteString("# Configure sshd: prepare runtime dir, generate host keys, " +
		"make pam_loginuid optional.\n")
	b.WriteString("RUN mkdir -p /run/sshd && \\\n")
	b.WriteString("    ssh-keygen -A && \\\n")
	b.WriteString("    sed -i 's|^session[[:space:]]\\+required[[:space:]]\\+" +
		"pam_loginuid\\.so|session optional pam_loginuid.so|' /etc/pam.d/sshd\n")
	if s.hasSshdConfigD {
		b.WriteString("COPY <<EOF /etc/ssh/sshd_config.d/10-codebox.conf\n")
	} else {
		b.WriteString("COPY <<EOF /etc/ssh/sshd_config\n")
	}
	b.WriteString("Port 2222\n")
	b.WriteString("PubkeyAuthentication yes\n")
	b.WriteString("PasswordAuthentication no\n")
	b.WriteString("UsePAM no\n")
	b.WriteString("EOF\n\n")

	b.WriteString("# Passwordless sudo for \"user\".\n")
	b.WriteString("RUN echo 'user ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/user && \\\n")
	b.WriteString("    chmod 0440 /etc/sudoers.d/user\n\n")

	b.WriteString("# Init script: start sshd, then block forever.\n")
	b.WriteString("COPY <<EOF /usr/local/bin/codebox-init\n")
	b.WriteString("#!/bin/sh\n")
	b.WriteString("/usr/sbin/sshd\n")
	b.WriteString("sleep infinity\n")
	b.WriteString("EOF\n")
	b.WriteString("RUN chmod 0755 /usr/local/bin/codebox-init\n\n")

	renderExtras(&b, s, opts)

	b.WriteString("# Install the operator's public key.\n")
	b.WriteString("RUN install -d -m 0700 -o user -g user /home/user/.ssh\n")
	b.WriteString("COPY --chown=user:user --chmod=0600 <<EOF /home/user/.ssh/authorized_keys\n")
	b.WriteString(authKey)
	b.WriteString("\nEOF\n\n")

	b.WriteString("EXPOSE 2222\n\n")
	b.WriteString(`CMD ["/usr/local/bin/codebox-init"]` + "\n")
	return b.String()
}

// renderExtras appends the optional language/tool install layers. The
// block sits after the init script and before the authorized_keys
// install so that user "user" already exists for uv/nvm. Root-scoped
// installs (psql, Go, .NET) go first; we then switch to USER user for
// the home-directory installs (uv, nvm) and switch back to root so the
// subsequent key install can write under /home/user.
func renderExtras(b *strings.Builder, s spec, opts Options) {
	var rootPkgs []string
	if opts.Psql {
		rootPkgs = append(rootPkgs, s.family.psqlPkg())
	}
	if opts.Node != "" {
		rootPkgs = append(rootPkgs, s.family.nodeDeps()...)
	}
	if opts.Dotnet != "" {
		rootPkgs = append(rootPkgs, s.family.dotnetDeps()...)
	}
	if len(rootPkgs) > 0 {
		b.WriteString("# System packages for the requested toolchains.\n")
		fmt.Fprintf(b, "RUN %s\n\n", s.family.extraInstallLine(rootPkgs))
	}

	profile := s.family.profilePath()
	if opts.Golang != "" {
		renderGolang(b, opts.Golang, profile)
	}
	if opts.Dotnet != "" {
		renderDotnet(b, opts.Dotnet, profile)
	}

	needsUser := opts.Python != "" || opts.Node != ""
	if needsUser {
		b.WriteString("USER user\n\n")
	}
	if opts.Python != "" {
		renderPython(b, opts.Python, profile)
	}
	if opts.Node != "" {
		renderNode(b, opts.Node)
	}
	if needsUser {
		b.WriteString("USER root\n\n")
	}
}

// renderGolang installs the requested Go release into /usr/local/go and
// appends the binary directory to user "user"'s PATH via the per-family
// login profile file.
func renderGolang(b *strings.Builder, version, profile string) {
	fmt.Fprintf(b, "# Install Go %s into /usr/local/go.\n", version)
	fmt.Fprintf(b,
		"RUN arch=\"$(uname -m)\" && \\\n"+
			"    case \"$arch\" in x86_64) arch=amd64;; aarch64) arch=arm64;; "+
			"*) echo \"unsupported arch: $arch\" >&2; exit 1;; esac && \\\n"+
			"    curl -fsSL \"https://go.dev/dl/go%s.linux-${arch}.tar.gz\" \\\n"+
			"      | tar -C /usr/local -xz\n", version)
	fmt.Fprintf(b, "RUN echo 'export PATH=\"/usr/local/go/bin:$PATH\"' >> %s\n\n", profile)
}

// renderDotnet installs the requested .NET SDK channel into
// /usr/local/dotnet via Microsoft's official installer and wires the
// runtime onto user "user"'s PATH. DOTNET_CLI_TELEMETRY_OPTOUT=1 is
// exported in the login profile so telemetry stays off in interactive
// shells.
func renderDotnet(b *strings.Builder, version, profile string) {
	fmt.Fprintf(b, "# Install .NET %s into /usr/local/dotnet.\n", version)
	fmt.Fprintf(b,
		"RUN curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh && \\\n"+
			"    chmod +x /tmp/dotnet-install.sh && \\\n"+
			"    /tmp/dotnet-install.sh --channel %s.0 --install-dir /usr/local/dotnet && \\\n"+
			"    ln -sf /usr/local/dotnet/dotnet /usr/local/bin/dotnet && \\\n"+
			"    rm /tmp/dotnet-install.sh\n", version)
	fmt.Fprintf(b, "RUN echo 'export DOTNET_ROOT=\"/usr/local/dotnet\"' >> %s && \\\n", profile)
	fmt.Fprintf(b, "    echo 'export PATH=\"$DOTNET_ROOT:$PATH\"' >> %s && \\\n", profile)
	fmt.Fprintf(b, "    echo 'export DOTNET_CLI_TELEMETRY_OPTOUT=1' >> %s\n\n", profile)
}

// renderPython installs uv and pins the requested Python version
// globally. uv installs to $HOME/.local/bin and downloads a prebuilt
// CPython, so no system build dependencies are needed. The login
// profile exports $HOME/.local/bin onto PATH so `uv` is on the
// operator's shell.
func renderPython(b *strings.Builder, version, profile string) {
	b.WriteString("# Install uv and pin the requested Python version globally.\n")
	b.WriteString("RUN curl -LsSf https://astral.sh/uv/install.sh | sh\n")
	fmt.Fprintf(b, "RUN echo 'export PATH=\"$HOME/.local/bin:$PATH\"' >> %s\n", profile)
	fmt.Fprintf(b, "RUN /home/user/.local/bin/uv python install %s && \\\n", version)
	fmt.Fprintf(b, "    /home/user/.local/bin/uv python pin --global %s\n\n", version)
}

// renderNode installs nvm and the requested Node major version. The
// install script is pinned to a known-good release so that future
// repository renames or breaking changes cannot silently affect builds.
func renderNode(b *strings.Builder, version string) {
	b.WriteString("# Install nvm and the requested Node major version.\n")
	b.WriteString("RUN curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash\n")
	fmt.Fprintf(b,
		"RUN bash -c '. /home/user/.nvm/nvm.sh && nvm install %s && nvm alias default %s'\n\n",
		version, version,
	)
}
