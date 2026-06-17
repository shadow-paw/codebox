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
	refspec             string
	rebuild             bool
	httpsProxy          string
	osImage             string
	python              string
	node                string
	golang              string
	dotnet              string
	claude              bool
	claudeCredentials   bool
	codex               bool
	codexCredentials    bool
	opencode            bool
	opencodeCredentials bool
	podman              bool
	psql                bool
	tmux                bool
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
			"When `git.push-from` is set in the project's .codebox.conf the source\n" +
			"may be omitted — pass just `target_branch` (or `:target_branch`)\n" +
			"and the configured source is filled in, so `codebox workflow\n" +
			"issue-1234` with `git.push-from: origin/main` means\n" +
			"`codebox workflow origin/main:issue-1234`.\n\n" +
			"target_branch doubles as the new instance name and as the branch\n" +
			"checked out at ~/source inside the sandbox. workflow is a shortcut\n" +
			"for:\n\n" +
			"  codebox create target_branch\n" +
			"  codebox git push target_branch REFSPEC\n" +
			"  codebox shell target_branch\n\n" +
			"All create-time flags (--os, --python, --claude, …) are accepted\n" +
			"here and forwarded to the underlying create step. The refspec is\n" +
			"validated up front, so a malformed one is rejected before any\n" +
			"container is built.",
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
		"Export HTTPS_PROXY=URL from the in-container user login profile (also used for agent installs)")
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
		"Copy ~/.claude/.credentials.json into the instance after it starts (ignored unless --claude)")
	f.BoolVar(&opts.codex, "codex", false,
		"Install OpenAI Codex CLI")
	f.BoolVar(&opts.codexCredentials, "codex-credentials", false,
		"Copy ~/.codex/auth.json into the instance after it starts (ignored unless --codex)")
	f.BoolVar(&opts.opencode, "opencode", false,
		"Install opencode")
	f.BoolVar(&opts.opencodeCredentials, "opencode-credentials", false,
		"Copy ~/.local/share/opencode/auth.json into the instance after it starts (ignored unless --opencode)")
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
	if err := requireGitCwd(); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	pushSource, err := projectPushSource(home)
	if err != nil {
		return err
	}
	additionalRun, err := builderAdditionalRun(home)
	if err != nil {
		return err
	}
	return app.New(home).Workflow(ctx, stdin, stdout, stderr, app.WorkflowRequest{
		Orchestrator:        opts.orchestrator,
		Remote:              opts.remote,
		InstanceKey:         opts.instanceKey,
		Refspec:             opts.refspec,
		PushSource:          pushSource,
		OS:                  opts.osImage,
		Rebuild:             opts.rebuild,
		HTTPSProxy:          opts.httpsProxy,
		Python:              opts.python,
		Node:                opts.node,
		Golang:              opts.golang,
		Dotnet:              opts.dotnet,
		Claude:              opts.claude,
		ClaudeCredentials:   opts.claudeCredentials,
		Codex:               opts.codex,
		CodexCredentials:    opts.codexCredentials,
		Opencode:            opts.opencode,
		OpencodeCredentials: opts.opencodeCredentials,
		Psql:                opts.psql,
		Tmux:                opts.tmux,
		Podman:              opts.podman,
		AdditionalRun:       additionalRun,
	})
}
