package cli

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

// staticCompletion returns a ValidArgsFunction that always emits the
// supplied candidate set with the NoFileComp directive. Used to wire
// fixed-enum value completion on flags like --orchestrator, --os, and
// the language-version flags whose accepted values are known at build
// time. Returning NoFileComp keeps the shell from falling back to
// path completion when the prefix does not match any candidate.
func staticCompletion(values []string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeInstances is the cobra ValidArgsFunction wired onto every
// subcommand whose first positional is INSTANCE. It returns the names
// of codebox-managed containers on the target host so shells can offer
// them as tab-completion candidates.
//
// The lookup honours `--orchestrator` and `--remote` from the partial
// command line: when the operator types
//
//	codebox shell --remote=ops@bastion <TAB>
//
// the completion query runs on `ops@bastion`. `--instance-key` is
// parsed off the command line by cobra but not forwarded — the listing
// path uses the operator's normal ssh config to reach the orchestrator
// host, never the per-instance key.
//
// When the completion query fails (engine missing, ssh unreachable, …)
// the function returns no candidates rather than surfacing a
// stderr-blob to the shell; the operator can still type the name
// manually.
func completeInstances(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	orchestrator, _ := cmd.Flags().GetString("orchestrator")
	remote, _ := cmd.Flags().GetString("remote")

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	names, err := app.New(home).ListInstanceNames(ctx, app.ListRequest{
		Orchestrator: orchestrator,
		Remote:       remote,
	})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
