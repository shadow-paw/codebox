package cli

import (
	"fmt"
	"runtime/debug"
)

// version is the codebox release tag rendered in the banner. It can be
// overridden at build time with:
//
//	go build -ldflags "-X codebox/internal/cli.version=<tag>" ./cmd/codebox
var version = "0.1.0"

// gitSHA returns the short (7-character) git revision the binary was
// built from, or "" when the VCS stamp is unavailable — for example a
// build from a source tree outside a git work tree, or one made with
// -buildvcs=false. Go records the full revision in the build info under
// the "vcs.revision" key for builds made from inside a git work tree.
func gitSHA() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key != "vcs.revision" {
			continue
		}
		if len(s.Value) > 7 {
			return s.Value[:7]
		}
		return s.Value
	}
	return ""
}

// versionString renders the release tag followed by the short git SHA
// in parentheses when available, e.g. "0.1.0 (219e07a)". It falls back
// to the bare tag when the VCS stamp is missing.
func versionString() string {
	if sha := gitSHA(); sha != "" {
		return fmt.Sprintf("%s (%s)", version, sha)
	}
	return version
}

// projectURL is the upstream repository for codebox.
const projectURL = "https://github.com/shadow-paw/codebox"

// bannerTmpl carries the ASCII art with two format placeholders: the first
// for the literal backtick character (which cannot appear inside a Go raw
// string literal), and the second/third for the version tag and project URL.
const bannerTmpl = `   ___ ___   __| | ___| |__   _____  __
  / __/ _ \ / _%s |/ _ \ '_ \ / _ \ \/ /
 | (_| (_) | (_| |  __/ |_) | (_) >  <
  \___\___/ \__,_|\___|_.__/ \___/_/\_\  %s
 %s

`

// banner returns the ASCII banner rendered with the current version and URL.
func banner() string {
	return fmt.Sprintf(bannerTmpl, "`", versionString(), projectURL)
}
