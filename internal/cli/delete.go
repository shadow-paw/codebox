package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

type deleteOpts struct {
	instance     string
	orchestrator string
	remote       string
}

func newDeleteCmd() *cobra.Command {
	var opts deleteOpts

	cmd := &cobra.Command{
		Use:   "delete INSTANCE",
		Short: "Delete a sandbox instance",
		Long:  "Delete a sandbox instance. The container is stopped and removed.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.instance = args[0]
			return runDelete(cmd.Context(), cmd.OutOrStdout(), opts)
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

func runDelete(ctx context.Context, out io.Writer, opts deleteOpts) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).Delete(ctx, out, app.DeleteRequest{
		Instance:     opts.instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
	})
}
