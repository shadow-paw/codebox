package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

type pushOpts struct {
	orchestrator string
	remote       string
	instanceKey  string
	localPath    string
	instancePath string
}

func newPushCmd() *cobra.Command {
	var opts pushOpts

	cmd := &cobra.Command{
		Use:   "push INSTANCE",
		Short: "Copy files from the local machine to a sandbox instance",
		Long:  "Copy a file or directory from the local machine up to a sandbox instance.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPush(cmd.Context(),
				cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], opts)
		},
	}

	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&opts.orchestrator, "orchestrator", "podman",
		"Container orchestrator (podman, docker)")
	f.StringVar(&opts.remote, "remote", "",
		"Target a remote host running the orchestrator (user@host); default is local")
	f.StringVar(&opts.instanceKey, "instance-key", "",
		"SSH key for logging into the instance (auto-detected if omitted)")
	f.StringVar(&opts.localPath, "local-path", "",
		"File or directory on the local machine to copy from")
	f.StringVar(&opts.instancePath, "instance-path", "",
		"Directory on the instance to copy into")
	return cmd
}

func runPush(
	ctx context.Context,
	stdout, stderr io.Writer,
	instance string,
	opts pushOpts,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).Push(ctx, stdout, stderr, app.PushRequest{
		Instance:     instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
		InstanceKey:  opts.instanceKey,
		LocalPath:    opts.localPath,
		InstancePath: opts.instancePath,
	})
}
