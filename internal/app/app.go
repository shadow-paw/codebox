// Package app is the use-case layer of codebox. internal/cli depends
// on app; the CLI never imports domain or adapter packages directly.
//
// An App is constructed once at startup with concrete adapters. Its
// methods take a context, an io.Writer, and a request value, and
// orchestrate the relevant domain packages. App itself performs no
// IO other than what is delegated through its adapters or to the
// supplied writer.
package app

import (
	"context"
	"io"

	"codebox/internal/adapters/runner"
	"codebox/internal/adapters/sshkey"
)

// KeyResolver returns the contents of an SSH public key to embed into a
// sandbox image's authorized_keys file. keyPath is the caller-supplied
// path; an empty string asks the resolver to auto-detect.
type KeyResolver interface {
	Resolve(keyPath string) (string, error)
}

// CommandRunner executes a single shell command, wiring stdin/stdout
// /stderr through. Implementations may target the local machine or a
// remote host over ssh. Connection-level ssh failures should be
// returned as a value matching *runner.ConnectError via errors.As.
type CommandRunner interface {
	Run(
		ctx context.Context,
		shellCmd string,
		stdin io.Reader,
		stdout, stderr io.Writer,
	) error
}

// RunnerFor returns the CommandRunner used for host. An empty host
// means run locally.
type RunnerFor func(host string) CommandRunner

// App holds the adapters the use-case layer needs.
type App struct {
	home    string
	keys    KeyResolver
	runners RunnerFor
}

// New returns an App wired with the default production adapters.
// homeDir is the operator's home directory (typically the value of
// os.UserHomeDir).
func New(homeDir string) *App {
	return &App{
		home:    homeDir,
		keys:    sshkey.New(homeDir),
		runners: defaultRunner,
	}
}

// NewWith returns an App with explicitly supplied adapters. Tests use
// this to inject fakes; production code uses New.
func NewWith(homeDir string, keys KeyResolver, runners RunnerFor) *App {
	return &App{home: homeDir, keys: keys, runners: runners}
}

// defaultRunner is the production RunnerFor: local when host is empty,
// otherwise a runner.SSH wrapping the supplied host.
func defaultRunner(host string) CommandRunner {
	if host == "" {
		return runner.Local()
	}
	return runner.SSH(host)
}
