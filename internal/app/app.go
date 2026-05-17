// Package app is the use-case layer of codebox. internal/cli depends on
// app; the CLI layer never imports domain or adapter packages directly.
//
// An App is constructed once at startup with concrete adapters; its
// methods take a context, an io.Writer, and a request value and
// orchestrate the relevant domain packages. App itself performs no IO
// other than what is delegated to its adapters or to the supplied
// writer.
package app

import "codebox/internal/adapters/sshkey"

// KeyResolver returns the contents of an SSH public key to embed in a
// sandbox image's authorized_keys file. keyPath is the caller-supplied
// path; an empty string asks the resolver to auto-detect.
type KeyResolver interface {
	Resolve(keyPath string) (string, error)
}

// App holds the adapters the use-case layer needs.
type App struct {
	keys KeyResolver
}

// New returns an App wired with the default production adapters.
// homeDir is the operator's home directory (typically the value of
// os.UserHomeDir).
func New(homeDir string) *App {
	return &App{keys: sshkey.New(homeDir)}
}

// NewWith returns an App with explicitly supplied adapters. Tests use
// this to inject fakes; production code uses New.
func NewWith(keys KeyResolver) *App {
	return &App{keys: keys}
}
