package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

type listOpts struct {
	orchestrator string
	remote       string
}

func newListCmd() *cobra.Command {
	var opts listOpts

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sandbox instances",
		Long:  "List sandbox instances managed by the chosen orchestrator.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}

	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&opts.orchestrator, "orchestrator", "podman",
		"Container orchestrator (podman, docker)")
	f.StringVar(&opts.remote, "remote", "",
		"Target a remote host running the orchestrator (user@host); default is local")
	return cmd
}

func runList(ctx context.Context, out io.Writer, opts listOpts) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).List(ctx, out, app.ListRequest{
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
	})
}
