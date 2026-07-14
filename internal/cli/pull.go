package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

type pullOpts struct {
	commonOpts
	instancePath string
	localPath    string
}

func newPullCmd() *cobra.Command {
	var opts pullOpts

	cmd := &cobra.Command{
		Use:               "pull INSTANCE",
		Short:             "Copy files from a sandbox instance to the local machine",
		Long:              "Copy a file or directory from a sandbox instance down to the local machine.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstances,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.commonOpts = readCommonOpts(cmd)
			return runPull(cmd.Context(),
				cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], opts)
		},
	}

	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&opts.instancePath, "instance-path", "",
		"File or directory on the instance to copy from")
	f.StringVar(&opts.localPath, "local-path", "",
		"Local directory to copy into")
	return cmd
}

func runPull(
	ctx context.Context,
	stdout, stderr io.Writer,
	instance string,
	opts pullOpts,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).Pull(ctx, stdout, stderr, app.PullRequest{
		Instance:     instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
		InstanceKeys: opts.instanceKeys,
		InstancePath: opts.instancePath,
		LocalPath:    opts.localPath,
	})
}
