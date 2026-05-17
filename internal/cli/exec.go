package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

type execOpts struct {
	orchestrator string
	remote       string
	instanceKey  string
}

func newExecCmd() *cobra.Command {
	var opts execOpts

	cmd := &cobra.Command{
		Use:   "exec INSTANCE -- COMMAND [ARGS...]",
		Short: "Execute a command inside a sandbox instance",
		Long: "Execute a command inside a sandbox instance and exit with its\n" +
			"status code. Place '--' before COMMAND so flags meant for the\n" +
			"inner command are not consumed by codebox.",
		Args: execArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd.Context(),
				cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				args[0], args[1], args[2:], opts)
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
	return cmd
}

// execArgs enforces the "exec INSTANCE -- COMMAND [ARGS...]" shape. The
// '--' separator is required: it tells codebox where its own flags end
// and the inner command begins, so flags like `-la` are forwarded to
// COMMAND instead of being interpreted by codebox.
func execArgs(cmd *cobra.Command, args []string) error {
	dash := cmd.ArgsLenAtDash()
	switch {
	case dash < 0:
		return errors.New("missing '--' before COMMAND (use: exec INSTANCE -- COMMAND [ARGS...])")
	case dash != 1:
		return errors.New("expected exactly one INSTANCE before '--'")
	case len(args) == dash:
		return errors.New("missing COMMAND after '--'")
	}
	return nil
}

func runExec(
	ctx context.Context,
	stdin io.Reader,
	stdout, stderr io.Writer,
	instance, command string,
	innerArgs []string,
	opts execOpts,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return app.New(home).Exec(ctx, stdin, stdout, stderr, app.ExecRequest{
		Instance:     instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
		InstanceKey:  opts.instanceKey,
		Command:      command,
		Args:         innerArgs,
	})
}
