package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sandbox instances",
		Long:  "List sandbox instances managed by the chosen orchestrator.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd.Context(), cmd.OutOrStdout(), readCommonOpts(cmd))
		},
	}
	cmd.Flags().SortFlags = false
	return cmd
}

func runList(ctx context.Context, out io.Writer, opts commonOpts) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).List(ctx, out, app.ListRequest{
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
	})
}
