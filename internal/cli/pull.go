package cli

import "github.com/spf13/cobra"

type pullOpts struct {
	orchestrator string
	remote       string
	instanceKey  string
	instancePath string
	localPath    string
}

func newPullCmd() *cobra.Command {
	var opts pullOpts

	cmd := &cobra.Command{
		Use:   "pull INSTANCE",
		Short: "Copy files from a sandbox instance to the local machine",
		Long:  "Copy a file or directory from a sandbox instance down to the local machine.",
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
	f.StringVar(&opts.instancePath, "instance-path", "",
		"File or directory on the instance to copy from")
	f.StringVar(&opts.localPath, "local-path", "",
		"Local directory to copy into")
	return cmd
}
