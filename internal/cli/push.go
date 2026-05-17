package cli

import "github.com/spf13/cobra"

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
		RunE:  stub(),
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
