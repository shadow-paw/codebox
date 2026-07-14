package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

func newVSCodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vscode INSTANCE",
		Short: "Open a sandbox instance in VS Code",
		Long: "Open a sandbox instance's ~/source in VS Code.\n\n" +
			"When run inside a VS Code SSH-remote terminal (where `code` opens\n" +
			"paths on this host), the instance's ~/source is sshfs-mounted onto\n" +
			".codebox/INSTANCE/ — mounting it first when that directory is\n" +
			"missing or empty — and that local path is opened.\n\n" +
			"When run inside a plain SSH session with no VS Code, there is no\n" +
			"local editor to drive, so the command refuses with a message and\n" +
			"exits — run it from your workstation or a VS Code Remote-SSH window\n" +
			"instead.\n\n" +
			"Otherwise (a local VS Code, or a non-VS-Code terminal on your\n" +
			"workstation), a Remote-SSH connection string is constructed and\n" +
			"printed, and VS Code is launched to open the instance over SSH. When\n" +
			"the host is reached through a bastion (--remote), a matching\n" +
			"ProxyJump host alias is written to ~/.ssh/codebox_config (included\n" +
			"from ~/.ssh/config) so Remote-SSH can traverse it without manual\n" +
			"ssh-config edits.\n\n" +
			"In the mount and Remote-SSH modes a *.code-workspace file in\n" +
			"~/source is opened as a workspace; otherwise the directory is opened.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstances,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVSCode(cmd.Context(),
				cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], readCommonOpts(cmd))
		},
	}
	cmd.Flags().SortFlags = false
	return cmd
}

func runVSCode(
	ctx context.Context,
	stdout, stderr io.Writer,
	instance string,
	opts commonOpts,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	insideVSCode, insideSSH := detectVSCodeEnv()
	return app.New(home).VSCode(ctx, stdout, stderr, app.VSCodeRequest{
		Instance:           instance,
		Orchestrator:       opts.orchestrator,
		Remote:             opts.remote,
		InstanceKeys:       opts.instanceKeys,
		InsideVSCodeRemote: insideVSCode && insideSSH,
		InsideSSHRemote:    insideSSH,
	})
}

// detectVSCodeEnv reports the two environment facts that select the
// `codebox vscode` open strategy:
//
//   - insideVSCode: the integrated terminal is VS Code's (TERM_PROGRAM=vscode).
//   - insideSSH: this terminal lives on a host reached over ssh
//     (SSH_CONNECTION present) — i.e. codebox is not on the operator's
//     workstation.
//
// The app layer combines them: insideVSCode && insideSSH is a VS Code
// Remote-SSH terminal (mount and open locally); insideSSH without
// insideVSCode is a plain remote shell (no editor to drive — refuse);
// neither is the operator's own machine (open over Remote-SSH).
func detectVSCodeEnv() (insideVSCode, insideSSH bool) {
	return os.Getenv("TERM_PROGRAM") == "vscode", os.Getenv("SSH_CONNECTION") != ""
}
