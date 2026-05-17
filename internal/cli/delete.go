package cli

import "github.com/spf13/cobra"

type deleteOpts struct {
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
		RunE:  stub(),
	}

	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&opts.orchestrator, "orchestrator", "podman",
		"Container orchestrator (podman, docker)")
	f.StringVar(&opts.remote, "remote", "",
		"Target a remote host running the orchestrator (user@host); default is local")
	return cmd
}
