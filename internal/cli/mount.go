package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

func newMountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mount INSTANCE [LOCAL_DIR]",
		Short: "sshfs-mount a sandbox instance's ~/source onto a local directory",
		Long: "sshfs-mount a sandbox instance's ~/source directory onto a local directory.\n\n" +
			"LOCAL_DIR is optional; when omitted it defaults to .codebox/INSTANCE/\n" +
			"relative to the current working directory. The local directory is\n" +
			"created if it does not exist, and ~/source is created inside the\n" +
			"instance if missing.",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeInstances,
		RunE: func(cmd *cobra.Command, args []string) error {
			localDir := ""
			if len(args) == 2 {
				localDir = args[1]
			}
			return runMount(cmd.Context(),
				cmd.OutOrStdout(), cmd.ErrOrStderr(),
				args[0], localDir, readCommonOpts(cmd))
		},
	}
	cmd.Flags().SortFlags = false
	return cmd
}

func newUmountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "umount INSTANCE [LOCAL_DIR]",
		Short: "Unmount a sandbox instance's sshfs mount",
		Long: "Unmount a previously sshfs-mounted sandbox instance.\n\n" +
			"LOCAL_DIR is optional; when omitted it defaults to .codebox/INSTANCE/\n" +
			"relative to the current working directory — the same default used\n" +
			"by `codebox mount`.",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeInstances,
		RunE: func(cmd *cobra.Command, args []string) error {
			localDir := ""
			if len(args) == 2 {
				localDir = args[1]
			}
			return runUmount(cmd.Context(),
				cmd.OutOrStdout(), cmd.ErrOrStderr(),
				args[0], localDir, readCommonOpts(cmd))
		},
	}
	cmd.Flags().SortFlags = false
	return cmd
}

func runMount(
	ctx context.Context,
	stdout, stderr io.Writer,
	instance, localDir string,
	opts commonOpts,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).Mount(ctx, stdout, stderr, app.MountRequest{
		Instance:     instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
		InstanceKeys: opts.instanceKeys,
		LocalDir:     localDir,
	})
}

func runUmount(
	ctx context.Context,
	stdout, stderr io.Writer,
	instance, localDir string,
	opts commonOpts,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).Unmount(ctx, stdout, stderr, app.UnmountRequest{
		Instance:     instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
		InstanceKeys: opts.instanceKeys,
		LocalDir:     localDir,
	})
}
