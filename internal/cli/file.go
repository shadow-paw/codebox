package cli

import (
	"github.com/spf13/cobra"
)

// newFileCmd builds the `file` parent command and wires its two
// children. `file push` copies from the local machine up to an
// instance; `file pull` copies from an instance back down. The parent
// itself takes no arguments and just renders help, mirroring the `git`
// parent command.
func newFileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file",
		Short: "Copy files between the local machine and a sandbox instance",
		Long: "Copy files between the local machine and a sandbox instance.\n\n" +
			"Use `push` to copy a file or directory from the local machine up to\n" +
			"an instance; use `pull` to copy one back down from an instance to\n" +
			"the local machine.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newPushCmd(), newPullCmd())
	return cmd
}
