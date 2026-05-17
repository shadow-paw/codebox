package image

import (
	"fmt"
	"strings"
)

// render returns the Dockerfile text for spec s with authKey embedded
// as the operator's authorized_keys entry. The build order matches the
// codebox spec: packages first (cache-friendly), OS fixes, user, sshd,
// sudoers, init script, key, then EXPOSE.
func render(s spec, authKey string) string {
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

	b.WriteString("# Install the operator's public key.\n")
	b.WriteString("RUN install -d -m 0700 -o user -g user /home/user/.ssh\n")
	b.WriteString("COPY --chown=user:user --chmod=0600 <<EOF /home/user/.ssh/authorized_keys\n")
	b.WriteString(authKey)
	b.WriteString("\nEOF\n\n")

	b.WriteString("EXPOSE 2222\n\n")
	b.WriteString(`CMD ["/usr/local/bin/codebox-init"]` + "\n")
	return b.String()
}
