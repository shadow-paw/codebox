package cli

import "github.com/spf13/cobra"

// newRootCmd builds the codebox root command and wires every subcommand.
// Help is rendered when the binary is invoked with no arguments.
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

	root.CompletionOptions.DisableDefaultCmd = true
	root.Flags().SortFlags = false
	root.PersistentFlags().SortFlags = false

	root.AddCommand(
		newCreateCmd(),
		newDeleteCmd(),
		newListCmd(),
		newShellCmd(),
		newExecCmd(),
		newPullCmd(),
		newPushCmd(),
	)
	return root
}

// stub returns a RunE that does nothing. Used by every command while the
// action layer is unimplemented; replace as each operation gets wired up.
func stub() func(*cobra.Command, []string) error {
	return func(*cobra.Command, []string) error { return nil }
}
