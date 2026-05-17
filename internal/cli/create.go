package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

type createOpts struct {
	instance     string
	orchestrator string
	remote       string
	instanceKey  string
	rebuild      bool
	osImage      string
	python       string
	node         string
	golang       string
	dotnet       string
	claude       bool
	codex        bool
	opencode     bool
	podman       bool
	psql         bool
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
  --claude     Claude Code
  --codex      OpenAI Codex CLI
  --opencode   opencode

Tools:
  --podman   Rootless Podman (inside the instance)
  --psql     psql PostgreSQL client
`

const createHelpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}` + createHelpFooter

func newCreateCmd() *cobra.Command {
	var opts createOpts

	cmd := &cobra.Command{
		Use:   "create INSTANCE",
		Short: "Create a new sandbox instance",
		Long: "Create a new sandbox instance.\n\n" +
			"This release renders the Dockerfile for the instance and prints it\n" +
			"to stdout; it does not yet hand the file to the orchestrator.\n" +
			"Defaults target a local rootless Podman setup; pass --remote to\n" +
			"target another host once provisioning is wired up.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.instance = args[0]
			return runCreate(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}

	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&opts.orchestrator, "orchestrator", "podman",
		"Container orchestrator (podman, docker)")
	f.StringVar(&opts.remote, "remote", "",
		"Provision on a remote host running the orchestrator (user@host); default is local")
	f.StringVar(&opts.instanceKey, "instance-key", "",
		"SSH key for logging into the new instance (auto-detected if omitted)")
	f.BoolVar(&opts.rebuild, "rebuild", false,
		"Force a rebuild of the base image even if a cached one exists")
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
	f.BoolVar(&opts.codex, "codex", false,
		"Install OpenAI Codex CLI")
	f.BoolVar(&opts.opencode, "opencode", false,
		"Install opencode")
	f.BoolVar(&opts.podman, "podman", false,
		"Install rootless Podman inside the instance")
	f.BoolVar(&opts.psql, "psql", false,
		"Install the psql PostgreSQL client")

	cmd.SetHelpTemplate(createHelpTemplate)
	return cmd
}

func runCreate(ctx context.Context, out io.Writer, opts createOpts) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}

	return app.New(home).Create(ctx, out, app.CreateRequest{
		Instance:     opts.instance,
		Orchestrator: opts.orchestrator,
		OS:           opts.osImage,
		InstanceKey:  expandHome(opts.instanceKey, home),
	})
}

// expandHome replaces a leading "~/" with home so users can pass paths
// the way they would type them in a shell. An empty input returns "".
func expandHome(p, home string) string {
	if p == "" {
		return ""
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
