package cli

import (
	"github.com/spf13/cobra"

	"codebox/internal/app"
)

// newRootCmd builds the codebox root command and wires every subcommand.
// Help is rendered when the binary is invoked with no arguments.
//
// --orchestrator, --remote and --instance-key are persistent flags on
// the root: they can appear before or after the subcommand name and
// still bind to the same values. For `exec`, the usual cobra rule
// applies — flags placed after the `--` separator are forwarded to the
// inner command instead of being consumed by codebox.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "codebox",
		Short: "Manage sandboxes for coding agents",
		Long: "codebox creates, inspects, and tears down container-based sandboxes\n" +
			"that host autonomous coding agents. Sandboxes run on Podman or\n" +
			"Docker, locally or on a remote host, and are reachable over SSH.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	root.Flags().SortFlags = false
	root.PersistentFlags().SortFlags = false

	pf := root.PersistentFlags()
	pf.String("orchestrator", "podman",
		"Container orchestrator (podman, docker)")
	pf.String("remote", "",
		"Target a remote host running the orchestrator (user@host); default is local")
	pf.String("instance-key", "",
		"SSH key for logging into the instance (auto-detected if omitted)")
	_ = root.RegisterFlagCompletionFunc("orchestrator",
		staticCompletion(app.SupportedOrchestrators()))

	root.AddCommand(
		newCreateCmd(),
		newDeleteCmd(),
		newListCmd(),
		newShellCmd(),
		newExecCmd(),
		newPullCmd(),
		newPushCmd(),
		newGitCmd(),
	)
	return root
}

// commonOpts collects the values of the three root-level persistent
// flags shared by every subcommand. Subcommands call readCommonOpts to
// pull them out of cobra's flag set; cobra inherits persistent flags
// from the parent, so the values are reachable from cmd.Flags() on the
// child regardless of whether the operator wrote them before or after
// the subcommand name.
type commonOpts struct {
	orchestrator string
	remote       string
	instanceKey  string
}

func readCommonOpts(cmd *cobra.Command) commonOpts {
	f := cmd.Flags()
	o, _ := f.GetString("orchestrator")
	r, _ := f.GetString("remote")
	k, _ := f.GetString("instance-key")
	return commonOpts{orchestrator: o, remote: r, instanceKey: k}
}
