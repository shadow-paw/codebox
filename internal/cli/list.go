package cli

import "github.com/spf13/cobra"

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
