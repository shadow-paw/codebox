// Package sshkey reads SSH public keys from disk for embedding into a
// codebox sandbox image. It is an IO adapter: business logic should
// consume it via the KeyResolver interface declared in internal/app.
package sshkey

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Resolver reads a public key from an explicit path or by scanning a
// single ssh directory (typically ~/.ssh).
type Resolver struct {
	sshDir string
}

// New returns a Resolver whose auto-detection scope is homeDir/.ssh.
func New(homeDir string) *Resolver {
	return &Resolver{sshDir: filepath.Join(homeDir, ".ssh")}
}

// NewWithDir returns a Resolver scoped to sshDir directly. Useful for
// tests that build a synthetic directory with t.TempDir.
func NewWithDir(sshDir string) *Resolver {
	return &Resolver{sshDir: sshDir}
}

// Resolve returns the trimmed contents of an SSH public key.
//
// When keyPath is non-empty the file at that path is read. The ".pub"
// suffix is appended if missing, so callers may pass either the private
// or the public side of a keypair. When keyPath is empty the resolver
// lists *.pub files in its ssh directory and requires exactly one
// match; zero or multiple matches return a descriptive error.
func (r *Resolver) Resolve(keyPath string) (string, error) {
	if keyPath != "" {
		if !strings.HasSuffix(keyPath, ".pub") {
			keyPath += ".pub"
		}
		return readPub(keyPath)
	}
	return r.autoDetect()
}

func (r *Resolver) autoDetect() (string, error) {
	entries, err := os.ReadDir(r.sshDir)
	if err != nil {
		return "", fmt.Errorf("sshkey: read %s: %w", r.sshDir, err)
	}
	var pubs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".pub") {
			pubs = append(pubs, e.Name())
		}
	}
	sort.Strings(pubs)
	switch len(pubs) {
	case 0:
		return "", fmt.Errorf("sshkey: no public keys in %s; pass --instance-key", r.sshDir)
	case 1:
		return readPub(filepath.Join(r.sshDir, pubs[0]))
	default:
		return "", fmt.Errorf("sshkey: %d public keys in %s (%s); pass --instance-key to choose one",
			len(pubs), r.sshDir, strings.Join(pubs, ", "))
	}
}

func readPub(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("sshkey: read %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}
