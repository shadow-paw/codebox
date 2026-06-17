// Package image generates Dockerfiles for codebox sandbox base images.
//
// The package is purely declarative: it owns the per-OS knowledge of base
// image, package manager, package name remapping, and PAM/sshd quirks.
// Callers ask Generate for a Dockerfile and decide what to do with it
// (print, save, hand to buildah, etc.). The package performs no IO of
// its own beyond writing the rendered Dockerfile to the supplied writer.
package image

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
)

// Options carries the inputs to Generate.
type Options struct {
	// OS is one of the keys returned by SupportedOS.
	OS string
	// AuthorizedKey is the SSH public-key content to install into
	// /home/user/.ssh/authorized_keys inside the image. A trailing
	// newline is normalised away before embedding.
	AuthorizedKey string

	// HTTPSProxy, when non-empty, becomes an `ENV HTTPS_PROXY=<value>`
	// directive emitted near the top of the Dockerfile so the proxy is
	// honoured by package managers, curl, and the installed toolchains.
	HTTPSProxy string

	// Optional language toolchains. An empty string disables the
	// corresponding install layer; a non-empty string is passed
	// through to the installer verbatim (e.g. "3.13", "24", "1.26.0",
	// "10"). Validation of the value itself is left to the installer.
	Python string
	Node   string
	Golang string
	Dotnet string

	// Optional agents.
	Claude bool
	// Codex installs the OpenAI Codex CLI via its native installer. The
	// installer drops the binary under $HOME/.local/bin (the same
	// directory Claude and uv use), so it shares the single PATH export
	// emitted for that directory. The operator's ~/.codex/config.toml is
	// *not* baked into the image — it is pushed in afterwards by
	// App.Create when present on the host.
	Codex bool
	// Opencode installs the opencode CLI via its native installer. The
	// installer drops the binary under $HOME/.opencode/bin; the layer
	// appends that directory to the login profile's PATH. The operator's
	// opencode.json is *not* baked into the image — it is pushed in
	// afterwards by App.Create when present on the host.
	Opencode bool

	// Optional tools.
	Psql bool
	// Tmux installs the tmux terminal multiplexer. `codebox shell`
	// launches tmux automatically when the instance carries the
	// matching `tmux=true` container label.
	Tmux bool
	// Podman installs rootless Podman (plus podman-compose and the
	// rootless networking/storage stack) and configures /etc/subuid,
	// /etc/subgid, and containers.conf so the in-container user can run
	// containers. The layer is emitted before any agent install.
	Podman bool

	// AdditionalRun lists the operator's custom build commands (the
	// builder.additional-run config). Each entry becomes its own RUN in
	// the late build stage — after the toolchains, agents, and tools are
	// installed and before the operator's SSH key is added — emitted
	// verbatim and executed as root. An empty slice adds nothing.
	AdditionalRun []string
}

// SupportedOS returns the OS keys understood by Generate in
// deterministic order.
func SupportedOS() []string {
	keys := make([]string, 0, len(specs))
	for k := range specs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SupportedPython returns the version strings Generate will accept
// for `--python`. Each call returns a fresh slice so callers cannot
// mutate the package-level enum. Intended for shell-completion
// candidate lookup; the validator inside Generate uses the same list.
func SupportedPython() []string { return append([]string(nil), supportedPython...) }

// SupportedNode returns the version strings Generate will accept for
// `--node`. See SupportedPython for the sharing/copy contract.
func SupportedNode() []string { return append([]string(nil), supportedNode...) }

// SupportedGolang returns the version strings Generate will accept
// for `--golang`. See SupportedPython for the sharing/copy contract.
func SupportedGolang() []string { return append([]string(nil), supportedGolang...) }

// SupportedDotnet returns the version strings Generate will accept
// for `--dotnet`. See SupportedPython for the sharing/copy contract.
func SupportedDotnet() []string { return append([]string(nil), supportedDotnet...) }

// Generate writes a Dockerfile for the requested OS to w.
func Generate(w io.Writer, opts Options) error {
	s, ok := specs[opts.OS]
	if !ok {
		return fmt.Errorf("image: unsupported os %q (known: %s)",
			opts.OS, strings.Join(SupportedOS(), ", "))
	}
	key := strings.TrimSpace(opts.AuthorizedKey)
	if key == "" {
		return fmt.Errorf("image: authorized key is empty")
	}
	if err := validateVersions(opts); err != nil {
		return err
	}
	if _, err := io.WriteString(w, render(s, key, opts)); err != nil {
		return fmt.Errorf("image: write Dockerfile: %w", err)
	}
	return nil
}

// validateVersions rejects unsupported values for the optional language
// toolchains. Each enum is documented in doc/command.md; out-of-set
// versions fail before any Dockerfile is emitted so the operator gets a
// concrete message instead of an opaque build error.
func validateVersions(opts Options) error {
	checks := []struct {
		flag, value string
		known       []string
	}{
		{"python", opts.Python, supportedPython},
		{"node", opts.Node, supportedNode},
		{"golang", opts.Golang, supportedGolang},
		{"dotnet", opts.Dotnet, supportedDotnet},
	}
	for _, c := range checks {
		if c.value == "" {
			continue
		}
		if !slices.Contains(c.known, c.value) {
			return fmt.Errorf("image: unsupported %s version %q (known: %s)",
				c.flag, c.value, strings.Join(c.known, ", "))
		}
	}
	return nil
}
