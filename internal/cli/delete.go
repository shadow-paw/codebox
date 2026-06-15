package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

func newDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete INSTANCE",
		Short: "Delete a sandbox instance",
		Long: "Delete a sandbox instance. The container is stopped and removed, " +
			"and the local artifacts codebox created for it are cleaned up: any " +
			"sshfs mounts are unmounted, the VS Code ssh alias is removed from " +
			"~/.ssh/codebox_config, and the instance's git remote is dropped.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstances,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDelete(cmd.Context(), cmd.OutOrStdout(), args[0], readCommonOpts(cmd))
		},
	}
	cmd.Flags().SortFlags = false
	return cmd
}

func runDelete(ctx context.Context, out io.Writer, instance string, opts commonOpts) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).Delete(ctx, out, app.DeleteRequest{
		Instance:     instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
	})
}
