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
	if _, err := io.WriteString(w, render(s, key)); err != nil {
		return fmt.Errorf("image: write Dockerfile: %w", err)
	}
	return nil
}
