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

	if s.renameFromUser != "" {
		fmt.Fprintf(&b,
			"# Rename the base image's %q account to \"user\" (login, group, home), "+
				"then unlock it.\n", s.renameFromUser)
		fmt.Fprintf(&b, "RUN usermod -l user -d /home/user -m -s /bin/bash %s && \\\n", s.renameFromUser)
		fmt.Fprintf(&b, "    groupmod -n user %s && \\\n", s.renameFromUser)
		b.WriteString("    usermod -p '*NP' user\n\n")
	} else {
		b.WriteString("# Create user \"user\" with a locked password slot, then unlock the account.\n")
		b.WriteString("RUN useradd -m -s /bin/bash user && \\\n")
		b.WriteString("    usermod -p '*NP' user\n\n")
	}

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
	if opts.Tmux {
		rootPkgs = append(rootPkgs, s.family.pkg("tmux"))
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

	// Podman install + setup runs before the agent installs below so an
	// agent can drive containers as part of its first task. The install
	// line is family-specific (RHEL needs a module enabled and gets
	// podman-compose from PyPI), so it is rendered apart from the generic
	// toolchain packages above.
	if opts.Podman {
		b.WriteString("# Install rootless Podman and its supporting stack.\n")
		fmt.Fprintf(b, "RUN %s\n\n", s.family.podmanInstall())
		renderPodman(b)
	}

	profile := s.family.profilePath()
	if proxy := strings.TrimSpace(opts.HTTPSProxy); proxy != "" {
		renderHTTPSProxy(b, proxy, profile)
	}
	if opts.Golang != "" {
		renderGolang(b, opts.Golang, profile)
	}
	if opts.Dotnet != "" {
		renderDotnet(b, opts.Dotnet, profile)
	}

	needsUser := opts.Python != "" || opts.Node != "" || opts.Claude || opts.Codex || opts.Opencode
	if needsUser {
		b.WriteString("USER user\n\n")
	}
	// uv (Python) and the Claude/Codex installers all drop binaries under
	// $HOME/.local/bin; emit the PATH export once if any of them is enabled.
	if opts.Python != "" || opts.Claude || opts.Codex {
		fmt.Fprintf(b, "RUN echo 'export PATH=\"$HOME/.local/bin:$PATH\"' >> %s\n\n", profile)
	}
	if opts.Python != "" {
		renderPython(b, opts.Python)
	}
	if opts.Claude {
		renderClaude(b, strings.TrimSpace(opts.HTTPSProxy))
	}
	if opts.Codex {
		renderCodex(b, strings.TrimSpace(opts.HTTPSProxy))
	}
	if opts.Opencode {
		renderOpencode(b, profile, strings.TrimSpace(opts.HTTPSProxy))
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
// CPython, so no system build dependencies are needed. The
// $HOME/.local/bin PATH export is emitted once by renderExtras so the
// directory is on PATH for any caller that lands a binary there.
func renderPython(b *strings.Builder, version string) {
	b.WriteString("# Install uv and pin the requested Python version globally.\n")
	b.WriteString("RUN curl -LsSf https://astral.sh/uv/install.sh | sh\n")
	fmt.Fprintf(b, "RUN /home/user/.local/bin/uv python install %s && \\\n", version)
	fmt.Fprintf(b, "    /home/user/.local/bin/uv python pin --global %s\n\n", version)
}

// renderClaude installs Claude Code via Anthropic's native installer.
// The installer drops the `claude` binary into $HOME/.local/bin; the
// PATH export is emitted once by renderExtras so the binary is on the
// operator's shell. Credentials are *not* baked into the image — they
// are pushed in afterwards by App.Create when --claude-credentials is
// set.
//
// When httpsProxy is non-empty the proxy is exported inline so curl
// (and any sub-downloads the install script performs) routes through
// it; the proxy is *not* emitted as a global ENV directive, so other
// build steps continue to use the builder host's network.
//
// The layer also drops two pre-seeded config files so the CLI runs
// non-interactively inside the sandbox:
//
//   - /home/user/.claude.json with hasCompletedOnboarding so the
//     first-run prompt is skipped.
//   - /home/user/.claude/settings.json with permissions.defaultMode
//     set to "bypassPermissions" so every tool call is auto-approved.
//     The sandbox is the trust boundary; the CLI must not gate actions
//     behind interactive prompts inside it.
func renderClaude(b *strings.Builder, httpsProxy string) {
	b.WriteString("# Install Claude Code.\n")
	if httpsProxy == "" {
		b.WriteString("RUN curl -fsSL https://claude.ai/install.sh | bash\n\n")
	} else {
		escaped := strings.ReplaceAll(httpsProxy, "'", `'\''`)
		fmt.Fprintf(b,
			"RUN export HTTPS_PROXY='%s' && curl -fsSL https://claude.ai/install.sh | bash\n\n",
			escaped,
		)
	}
	b.WriteString("# Pre-seed the Claude onboarding flag so the CLI does not prompt on first run.\n")
	b.WriteString("COPY --chown=user:user <<EOF /home/user/.claude.json\n")
	b.WriteString("{\n")
	b.WriteString("  \"hasCompletedOnboarding\": true\n")
	b.WriteString("}\n")
	b.WriteString("EOF\n\n")
	b.WriteString("# Auto-approve every tool call inside the sandbox.\n")
	b.WriteString("RUN install -d -m 0700 -o user -g user /home/user/.claude\n")
	b.WriteString("COPY --chown=user:user <<EOF /home/user/.claude/settings.json\n")
	b.WriteString("{\n")
	b.WriteString("  \"permissions\": {\n")
	b.WriteString("    \"defaultMode\": \"bypassPermissions\"\n")
	b.WriteString("  }\n")
	b.WriteString("}\n")
	b.WriteString("EOF\n\n")
}

// renderCodex installs the OpenAI Codex CLI via its native installer.
// The installer drops the `codex` binary into $HOME/.local/bin (the same
// directory Claude and uv use), so the shared PATH export emitted by
// renderExtras already puts it on the login shell's PATH — no per-layer
// export is needed.
//
// When httpsProxy is non-empty it is exported inline for the install RUN
// so curl (and the release-asset download it performs) route through it,
// mirroring renderClaude; the proxy is not emitted as a global ENV
// directive.
//
// The operator's ~/.codex/config.toml is *not* baked into the image — it
// is pushed in afterwards by App.Create when it exists on the host.
func renderCodex(b *strings.Builder, httpsProxy string) {
	b.WriteString("# Install OpenAI Codex CLI.\n")
	if httpsProxy == "" {
		b.WriteString("RUN curl -fsSL https://chatgpt.com/codex/install.sh | bash\n\n")
	} else {
		escaped := strings.ReplaceAll(httpsProxy, "'", `'\''`)
		fmt.Fprintf(b,
			"RUN export HTTPS_PROXY='%s' && curl -fsSL https://chatgpt.com/codex/install.sh | bash\n\n",
			escaped,
		)
	}
}

// renderOpencode installs the opencode CLI via its native installer.
// The installer drops the `opencode` binary into $HOME/.opencode/bin and
// only edits interactive rc files (.bashrc/.zshrc), which login shells do
// not source, so the layer appends an explicit PATH export to the
// per-family login profile — the same one the other agent/toolchain
// layers write to — so `$SHELL -lc opencode` (the tmux right-pane
// command) finds the binary.
//
// When httpsProxy is non-empty it is exported inline for the install RUN
// so curl (and any sub-downloads) route through it, mirroring
// renderClaude; the proxy is not emitted as a global ENV directive.
//
// The operator's opencode.json is *not* baked into the image — it is
// pushed in afterwards by App.Create when it exists on the host.
func renderOpencode(b *strings.Builder, profile, httpsProxy string) {
	b.WriteString("# Install opencode.\n")
	if httpsProxy == "" {
		b.WriteString("RUN curl -fsSL https://opencode.ai/install | bash\n")
	} else {
		escaped := strings.ReplaceAll(httpsProxy, "'", `'\''`)
		fmt.Fprintf(b,
			"RUN export HTTPS_PROXY='%s' && curl -fsSL https://opencode.ai/install | bash\n",
			escaped,
		)
	}
	fmt.Fprintf(b, "RUN echo 'export PATH=\"$HOME/.opencode/bin:$PATH\"' >> %s\n\n", profile)
}

// renderHTTPSProxy appends `export HTTPS_PROXY="<value>"` to the
// operator's login profile so interactive shells inside the instance
// route HTTPS through the configured proxy. The proxy is *not* set as
// an ENV directive — image build time downloads continue to use the
// builder host's network, only the in-container shell sees the proxy.
// Single quotes in the value are shell-escaped so the surrounding
// `echo '...'` invocation survives an embedded apostrophe.
func renderHTTPSProxy(b *strings.Builder, value, profile string) {
	escaped := strings.ReplaceAll(value, "'", `'\''`)
	b.WriteString("# HTTPS proxy for interactive shells inside the instance.\n")
	fmt.Fprintf(b, "RUN echo 'export HTTPS_PROXY=\"%s\"' >> %s\n\n", escaped, profile)
}

// renderPodman configures rootless Podman for the in-container user.
// The packages themselves are installed alongside the other root-scoped
// system packages above; this block writes the pieces of configuration
// the rootless stack needs:
//
//   - /etc/subuid and /etc/subgid hand the user the subordinate UID/GID
//     ranges Podman maps into the user namespace. The base image seeds a
//     single range for "user"; we replace both files with the explicit
//     ranges codebox reserves for root and user. The second range starts
//     at 1001 (just past user "user"'s own UID 1000) on every supported
//     distro — Ubuntu's "ubuntu" account is renamed rather than adding a
//     new user, so "user" lands at 1000 there too.
//   - /home/user/.config/containers/containers.conf empties
//     [containers] default_sysctls so the nested Podman does not try to
//     set sysctls the host has masked, which would otherwise fail
//     container start inside the sandbox.
//   - /home/user/.config/containers/registries.conf adds docker.io to
//     the unqualified-search list so `podman pull alpine` resolves the
//     way operators expect rather than erroring on an ambiguous name.
//
// The rootless network backend is pasta: the passt package is installed
// and pasta is Podman's default rootless networking command, so no
// containers.conf network key is needed.
//
// The COPY directives create their parent directories if the engine
// package has not already laid them down, so the block is
// order-independent of the install RUN above.
func renderPodman(b *strings.Builder) {
	b.WriteString("# Configure rootless Podman inside the instance.\n")
	ranges := "root:1:65535\\nuser:1:999\\nuser:1001:64535\\n"
	b.WriteString("RUN printf '" + ranges + "' > /etc/subuid && \\\n")
	b.WriteString("    printf '" + ranges + "' > /etc/subgid\n")
	b.WriteString("COPY --chown=user:user <<EOF /home/user/.config/containers/containers.conf\n")
	b.WriteString("[containers]\n")
	b.WriteString("default_sysctls = []\n")
	b.WriteString("EOF\n")
	b.WriteString("COPY --chown=user:user <<EOF /home/user/.config/containers/registries.conf\n")
	b.WriteString("[registries.search]\n")
	b.WriteString("registries = ['docker.io']\n")
	b.WriteString("EOF\n\n")
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
