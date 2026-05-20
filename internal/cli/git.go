package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"codebox/internal/app"
)

type gitOpts struct {
	orchestrator string
	remote       string
	instanceKey  string
}

func newGitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git",
		Short: "Move git refs between the operator's repo and a sandbox instance",
		Long: "Move git refs between the operator's repo and a sandbox instance.\n\n" +
			"Use `push` to send a local ref into the instance and check it out\n" +
			"there; use `pull` to fetch a branch back from the instance into\n" +
			"a remote-tracking ref on the operator's machine.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newGitPushCmd(), newGitPullCmd())
	return cmd
}

func newGitPushCmd() *cobra.Command {
	var opts gitOpts

	cmd := &cobra.Command{
		Use:   "push INSTANCE source_remote/source_branch:target_branch",
		Short: "Push a fetched remote-tracking ref into a sandbox instance",
		Long: "Push a fetched remote-tracking ref into a sandbox instance.\n\n" +
			"REFSPEC has the form `source_remote/source_branch:target_branch`\n" +
			"(e.g. `origin/main:issue-1234`). codebox runs `git fetch source_remote`\n" +
			"locally, pushes the resulting remote-tracking ref to\n" +
			"refs/heads/target_branch on the instance, and checks target_branch\n" +
			"out at ~/source inside the sandbox.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGitPush(cmd.Context(),
				cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], args[1], opts)
		},
	}
	addGitFlags(cmd, &opts)
	return cmd
}

func newGitPullCmd() *cobra.Command {
	var opts gitOpts

	cmd := &cobra.Command{
		Use:   "pull INSTANCE BRANCH",
		Short: "Fetch a branch from a sandbox instance into a remote-tracking ref",
		Long: "Fetch a branch from a sandbox instance into a remote-tracking ref.\n\n" +
			"The instance's published port is re-resolved each run, so this still\n" +
			"works after the container has been restarted.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGitPull(cmd.Context(),
				cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], args[1], opts)
		},
	}
	addGitFlags(cmd, &opts)
	return cmd
}

func addGitFlags(cmd *cobra.Command, opts *gitOpts) {
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&opts.orchestrator, "orchestrator", "podman",
		"Container orchestrator (podman, docker)")
	f.StringVar(&opts.remote, "remote", "",
		"Target a remote host running the orchestrator (user@host); default is local")
	f.StringVar(&opts.instanceKey, "instance-key", "",
		"SSH key for logging into the instance (auto-detected if omitted)")
}

func runGitPush(
	ctx context.Context,
	stdout, stderr io.Writer,
	instance, refspec string,
	opts gitOpts,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	if err := requireGitCwd(); err != nil {
		return err
	}
	return app.New(home).GitPush(ctx, stdout, stderr, app.GitPushRequest{
		Instance:     instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
		InstanceKey:  opts.instanceKey,
		Refspec:      refspec,
	})
}

func runGitPull(
	ctx context.Context,
	stdout, stderr io.Writer,
	instance, branch string,
	opts gitOpts,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	if err := requireGitCwd(); err != nil {
		return err
	}
	return app.New(home).GitPull(ctx, stdout, stderr, app.GitPullRequest{
		Instance:     instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
		InstanceKey:  opts.instanceKey,
		Branch:       branch,
	})
}

// requireGitCwd confirms the operator's current working directory is
// the root of a git repository (i.e. has a `.git/` directory). Both
// git subcommands need to run a local `git remote`/`git fetch`/`git
// push`, so a non-repo cwd is rejected up front rather than letting
// the orchestrator-side work happen and then failing at the local
// git invocation.
func requireGitCwd() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate current directory: %w", err)
	}
	gitPath := filepath.Join(cwd, ".git")
	info, err := os.Stat(gitPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("not a git repository: no .git directory in %s", cwd)
	case err != nil:
		return fmt.Errorf("check for .git in %s: %w", cwd, err)
	case !info.IsDir():
		return fmt.Errorf("not a git repository: %s exists but is not a directory", gitPath)
	}
	return nil
}
