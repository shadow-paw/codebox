package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
	"codebox/internal/settings"
)

func newPortForwardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "port-forward INSTANCE",
		Short: "Forward configured ports from localhost to a sandbox instance",
		Long: "Forward TCP ports from localhost to a sandbox instance and hold them\n" +
			"open until interrupted, without opening a shell.\n\n" +
			"Ports come from the `port-forward:` list in either .codebox.conf\n" +
			"(project and global entries are merged, project first). Each entry is\n" +
			"LOCAL:REMOTE; a bare PORT maps that port to itself:\n\n" +
			"  port-forward:\n" +
			"    - 13000:3000\n" +
			"    - 13001:3001\n\n" +
			"When that list is absent and a compose file is present in the current\n" +
			"directory (compose.yaml, compose.yml, docker-compose.yaml,\n" +
			"docker-compose.yml, podman-compose.yaml, or podman-compose.yml, in\n" +
			"that order), the published ports are detected automatically and each\n" +
			"is forwarded to itself (localhost:PORT -> PORT in the instance).",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstances,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPortForward(cmd.Context(),
				cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], readCommonOpts(cmd))
		},
	}
	cmd.Flags().SortFlags = false
	return cmd
}

func runPortForward(
	ctx context.Context,
	stdout, stderr io.Writer,
	instance string,
	opts commonOpts,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate current directory: %w", err)
	}

	global, project, err := settings.Load(home, workDir)
	if err != nil {
		return err
	}
	ports, err := settings.ResolvePortForwards(global, project, workDir)
	if err != nil {
		return err
	}
	if len(ports) == 0 {
		return errors.New(
			"no ports to forward: add a port-forward: list to .codebox.conf, " +
				"or run from a directory containing a compose file")
	}

	return app.New(home).PortForward(ctx, stdout, stderr, app.PortForwardRequest{
		Instance:     instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
		InstanceKeys: opts.instanceKeys,
		Ports:        ports,
	})
}
