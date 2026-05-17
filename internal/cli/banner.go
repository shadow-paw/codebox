package cli

import "fmt"

// version is the codebox release tag rendered in the banner. It can be
// overridden at build time with:
//
//	go build -ldflags "-X codebox/internal/cli.version=<tag>" ./cmd/codebox
var version = "0.1.0"

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
	return fmt.Sprintf(bannerTmpl, "`", version, projectURL)
}
