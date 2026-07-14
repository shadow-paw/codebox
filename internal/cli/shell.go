package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

type shellOpts struct {
	commonOpts
	instance string
	ports    []string
}

func newShellCmd() *cobra.Command {
	var ports []string

	cmd := &cobra.Command{
		Use:   "shell INSTANCE",
		Short: "Open an interactive shell into a sandbox instance",
		Long: "Open an interactive shell into a sandbox instance over SSH.\n\n" +
			"Use --port to set up TCP forwards while the shell is open. Pass the\n" +
			"flag multiple times for multiple forwards.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstances,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := shellOpts{
				commonOpts: readCommonOpts(cmd),
				instance:   args[0],
				ports:      ports,
			}
			return runShell(cmd.Context(),
				cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
		},
	}

	f := cmd.Flags()
	f.SortFlags = false
	f.StringArrayVar(&ports, "port", nil,
		"Forward port LOCAL:REMOTE; repeat the flag for multiple forwards")
	return cmd
}

func runShell(
	ctx context.Context,
	stdin io.Reader,
	stdout, stderr io.Writer,
	opts shellOpts,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).Shell(ctx, stdin, stdout, stderr, app.ShellRequest{
		Instance:     opts.instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
		InstanceKeys: opts.instanceKeys,
		Ports:        opts.ports,
	})
}
