package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

type workflowOpts struct {
	commonOpts
	refspec           string
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
	tmux              bool
}

const workflowHelpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}` + createHelpFooter

func newWorkflowCmd() *cobra.Command {
	var opts workflowOpts

	cmd := &cobra.Command{
		Use:   "workflow REFSPEC",
		Short: "Create an instance, push a branch into it, and open a shell",
		Long: "Create an instance, push a branch into it, and open a shell.\n\n" +
			"REFSPEC takes the same shape as `codebox git push`:\n\n" +
			"  source_remote/source_branch:target_branch\n" +
			"      e.g. `origin/main:issue-1234`. Equivalent to\n" +
			"      `git fetch source_remote` followed by a push of\n" +
			"      `source_remote/source_branch` into the instance.\n\n" +
			"  local_branch:target_branch\n" +
			"      e.g. `main:issue-1234`. The local branch is pushed\n" +
			"      straight into the instance with no upstream fetch.\n\n" +
			"target_branch doubles as the new instance name and as the branch\n" +
			"checked out at ~/source inside the sandbox. workflow is a shortcut\n" +
			"for:\n\n" +
			"  codebox create target_branch\n" +
			"  codebox git push target_branch REFSPEC\n" +
			"  codebox shell target_branch\n\n" +
			"All create-time flags (--os, --python, --claude, …) are accepted\n" +
			"here and forwarded to the underlying create step. Argument formats\n" +
			"are validated up front, so a malformed refspec or unsupported flag\n" +
			"is rejected before any container is built.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.commonOpts = readCommonOpts(cmd)
			opts.refspec = args[0]
			return runWorkflow(cmd.Context(),
				cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
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
	f.BoolVar(&opts.tmux, "tmux", false,
		"Install tmux; the workflow shell launches it in the source directory")

	registerWorkflowValueCompletions(cmd)

	cmd.SetHelpTemplate(workflowHelpTemplate)
	return cmd
}

// registerWorkflowValueCompletions mirrors registerCreateValueCompletions
// for the workflow command — fixed-enum value completion on the same set
// of create-style flags.
func registerWorkflowValueCompletions(cmd *cobra.Command) {
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

func runWorkflow(
	ctx context.Context,
	stdin io.Reader,
	stdout, stderr io.Writer,
	opts workflowOpts,
) error {
	if err := rejectUnsupportedWorkflowFlags(opts); err != nil {
		return err
	}
	if opts.claudeCredentials && !opts.claude {
		return fmt.Errorf("--claude-credentials requires --claude")
	}
	if err := requireGitCwd(); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).Workflow(ctx, stdin, stdout, stderr, app.WorkflowRequest{
		Orchestrator:      opts.orchestrator,
		Remote:            opts.remote,
		InstanceKey:       opts.instanceKey,
		Refspec:           opts.refspec,
		OS:                opts.osImage,
		Rebuild:           opts.rebuild,
		HTTPSProxy:        opts.httpsProxy,
		Python:            opts.python,
		Node:              opts.node,
		Golang:            opts.golang,
		Dotnet:            opts.dotnet,
		Claude:            opts.claude,
		ClaudeCredentials: opts.claudeCredentials,
		Psql:              opts.psql,
		Tmux:              opts.tmux,
		Podman:            opts.podman,
	})
}

// rejectUnsupportedWorkflowFlags mirrors rejectUnsupportedFlags on
// create — workflow surfaces the same agent/tool flags and must reject
// the ones whose installers have not yet shipped.
func rejectUnsupportedWorkflowFlags(opts workflowOpts) error {
	return rejectUnsupportedFlags(createOpts{
		codex:    opts.codex,
		opencode: opts.opencode,
	})
}
