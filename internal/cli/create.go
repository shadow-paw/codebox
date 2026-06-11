package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

type createOpts struct {
	commonOpts
	instance          string
	rebuild           bool
	httpsProxy        string
	osImage           string
	python            string
	node              string
	golang            string
	dotnet            string
	claude            bool
	claudeCredentials bool
	codex             bool
	opencode          bool
	podman            bool
	psql              bool
}

const createHelpFooter = `
Orchestrators:
  podman, docker

OS:
  debian_12   Debian 12
  debian_13   Debian 13
  ubuntu_24   Ubuntu 24 LTS
  ubuntu_26   Ubuntu 26 LTS
  redhat_10   Red Hat 10

Languages:
  --python   3.12, 3.13, 3.14
  --node     24, 25, 26
  --golang   1.26.0
  --dotnet   8, 10

Agents:
  --claude               Claude Code (also writes /home/user/.claude.json onboarding flag)
  --claude-credentials   Copy ~/.claude/.credentials.json into the instance (requires --claude)
  --codex                OpenAI Codex CLI
  --opencode             opencode

Tools:
  --podman   Rootless Podman (inside the instance)
  --psql     psql PostgreSQL client

Network:
  --https-proxy=URL   Export HTTPS_PROXY=URL from the in-container user login profile
                      (also exported during the Claude install so the curl pipeline routes through it)
`

const createHelpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}` + createHelpFooter

func newCreateCmd() *cobra.Command {
	var opts createOpts

	cmd := &cobra.Command{
		Use:   "create INSTANCE",
		Short: "Create a new sandbox instance",
		Long: "Create a new sandbox instance.\n\n" +
			"Builds an image from a Dockerfile generated on the fly (no files\n" +
			"from the working directory are sent to the orchestrator), then\n" +
			"creates and starts the container labelled codebox=true. Pass\n" +
			"--remote to provision on another host over ssh.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.commonOpts = readCommonOpts(cmd)
			opts.instance = args[0]
			return runCreate(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}

	f := cmd.Flags()
	f.SortFlags = false
	f.BoolVar(&opts.rebuild, "rebuild", false,
		"Force a rebuild of the base image even if a cached one exists")
	f.StringVar(&opts.httpsProxy, "https-proxy", "",
		"Export HTTPS_PROXY=URL from the in-container user login profile (also used to install Claude)")
	f.StringVar(&opts.osImage, "os", "debian_13",
		"Base OS image (debian_12, debian_13, ubuntu_24, ubuntu_26, redhat_10)")
	f.StringVar(&opts.python, "python", "",
		"Install Python at this version (3.12, 3.13, 3.14)")
	f.StringVar(&opts.node, "node", "",
		"Install Node.js at this major version (24, 25, 26)")
	f.StringVar(&opts.golang, "golang", "",
		"Install Go at this version (1.26.0)")
	f.StringVar(&opts.dotnet, "dotnet", "",
		"Install .NET at this version (8, 10)")
	f.BoolVar(&opts.claude, "claude", false,
		"Install Claude Code")
	f.BoolVar(&opts.claudeCredentials, "claude-credentials", false,
		"Copy ~/.claude/.credentials.json into the instance after it starts (requires --claude)")
	f.BoolVar(&opts.codex, "codex", false,
		"Install OpenAI Codex CLI")
	f.BoolVar(&opts.opencode, "opencode", false,
		"Install opencode")
	f.BoolVar(&opts.podman, "podman", false,
		"Install rootless Podman inside the instance")
	f.BoolVar(&opts.psql, "psql", false,
		"Install the psql PostgreSQL client")

	registerCreateValueCompletions(cmd)

	cmd.SetHelpTemplate(createHelpTemplate)
	return cmd
}

// registerCreateValueCompletions wires fixed-enum value completion for
// the create flags whose accepted values are known at build time. The
// candidate sets come through internal/app (which re-exports the
// domain-layer enums) so the CLI stays decoupled from internal/image.
//
// RegisterFlagCompletionFunc only fails when the flag does not exist
// — a programmer error this file would catch on the first build — so
// the returned errors are intentionally discarded.
func registerCreateValueCompletions(cmd *cobra.Command) {
	pairs := []struct {
		flag   string
		values []string
	}{
		{"os", app.SupportedOS()},
		{"python", app.SupportedPython()},
		{"node", app.SupportedNode()},
		{"golang", app.SupportedGolang()},
		{"dotnet", app.SupportedDotnet()},
	}
	for _, p := range pairs {
		_ = cmd.RegisterFlagCompletionFunc(p.flag, staticCompletion(p.values))
	}
}

func runCreate(ctx context.Context, out io.Writer, opts createOpts) error {
	if err := rejectUnsupportedFlags(opts); err != nil {
		return err
	}
	if opts.claudeCredentials && !opts.claude {
		return fmt.Errorf("--claude-credentials requires --claude")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).Create(ctx, out, app.CreateRequest{
		Instance:          opts.instance,
		Orchestrator:      opts.orchestrator,
		OS:                opts.osImage,
		InstanceKey:       opts.instanceKey,
		Remote:            opts.remote,
		Rebuild:           opts.rebuild,
		HTTPSProxy:        opts.httpsProxy,
		Python:            opts.python,
		Node:              opts.node,
		Golang:            opts.golang,
		Dotnet:            opts.dotnet,
		Claude:            opts.claude,
		ClaudeCredentials: opts.claudeCredentials,
		Psql:              opts.psql,
		Podman:            opts.podman,
	})
}

// rejectUnsupportedFlags fails fast when the operator enables a flag
// whose installer has not yet been implemented. The flags are kept on
// the surface so help text and shell completion stay stable, but
// invoking them errors out instead of silently producing an image
// without the requested tool.
func rejectUnsupportedFlags(opts createOpts) error {
	var names []string
	if opts.codex {
		names = append(names, "--codex")
	}
	if opts.opencode {
		names = append(names, "--opencode")
	}
	if len(names) == 0 {
		return nil
	}
	return fmt.Errorf("%s not yet supported", strings.Join(names, ", "))
}
