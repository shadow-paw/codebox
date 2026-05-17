package cli

import "github.com/spf13/cobra"

type shellOpts struct {
	orchestrator string
	remote       string
	instanceKey  string
	ports        []string
}

func newShellCmd() *cobra.Command {
	var opts shellOpts

	cmd := &cobra.Command{
		Use:   "shell INSTANCE",
		Short: "Open an interactive shell into a sandbox instance",
		Long: "Open an interactive shell into a sandbox instance over SSH.\n\n" +
			"Use --port to set up TCP forwards while the shell is open. Pass the\n" +
			"flag multiple times for multiple forwards.",
		Args: cobra.ExactArgs(1),
		RunE: stub(),
	}

	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&opts.orchestrator, "orchestrator", "podman",
		"Container orchestrator (podman, docker)")
	f.StringVar(&opts.remote, "remote", "",
		"Target a remote host running the orchestrator (user@host); default is local")
	f.StringVar(&opts.instanceKey, "instance-key", "",
		"SSH key for logging into the instance (auto-detected if omitted)")
	f.StringArrayVar(&opts.ports, "port", nil,
		"Forward port LOCAL:REMOTE; repeat the flag for multiple forwards")
	return cmd
}
