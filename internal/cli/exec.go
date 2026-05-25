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

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec INSTANCE -- COMMAND [ARGS...]",
		Short: "Execute a command inside a sandbox instance",
		Long: "Execute a command inside a sandbox instance and exit with its\n" +
			"status code. Place '--' before COMMAND so flags meant for the\n" +
			"inner command are not consumed by codebox.",
		Args:              execArgs,
		ValidArgsFunction: completeInstances,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd.Context(),
				cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				args[0], args[1], args[2:], readCommonOpts(cmd))
		},
	}
	cmd.Flags().SortFlags = false
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
	opts commonOpts,
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
