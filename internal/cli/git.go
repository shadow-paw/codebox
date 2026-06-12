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
	"codebox/internal/settings"
)

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
	cmd := &cobra.Command{
		Use:   "push INSTANCE [REFSPEC]",
		Short: "Push a ref from the operator's repo into a sandbox instance",
		Long: "Push a ref from the operator's repo into a sandbox instance.\n\n" +
			"REFSPEC takes one of two shapes:\n\n" +
			"  source_remote/source_branch:target_branch\n" +
			"      e.g. `origin/main:issue-1234`. codebox runs\n" +
			"      `git fetch source_remote` locally, then pushes the resulting\n" +
			"      remote-tracking ref to refs/heads/target_branch on the\n" +
			"      instance.\n\n" +
			"  local_branch:target_branch\n" +
			"      e.g. `main:issue-1234`. No source remote and no local fetch;\n" +
			"      the named local branch is pushed straight to\n" +
			"      refs/heads/target_branch on the instance.\n\n" +
			"The source side may be omitted when `git.push-from` is set in the\n" +
			"project's .codebox.conf: write `:target_branch` (or, with REFSPEC\n" +
			"left off entirely, just `codebox git push INSTANCE`, which targets\n" +
			"a branch named after the instance) and the configured source is\n" +
			"filled in.\n\n" +
			"In every form target_branch is checked out at ~/source inside\n" +
			"the sandbox after the push.",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeInstances,
		RunE: func(cmd *cobra.Command, args []string) error {
			refspec := ""
			if len(args) > 1 {
				refspec = args[1]
			}
			return runGitPush(cmd.Context(),
				cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], refspec, readCommonOpts(cmd))
		},
	}
	cmd.Flags().SortFlags = false
	return cmd
}

func newGitPullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull INSTANCE [BRANCH]",
		Short: "Fetch a branch from a sandbox instance into a remote-tracking ref",
		Long: "Fetch a branch from a sandbox instance into a remote-tracking ref.\n\n" +
			"BRANCH is optional; when omitted it defaults to INSTANCE, matching\n" +
			"the branch `codebox workflow`/`codebox git push` check out at\n" +
			"~/source inside a sandbox of the same name.\n\n" +
			"The instance's published port is re-resolved each run, so this still\n" +
			"works after the container has been restarted.",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeInstances,
		RunE: func(cmd *cobra.Command, args []string) error {
			branch := ""
			if len(args) > 1 {
				branch = args[1]
			}
			return runGitPull(cmd.Context(),
				cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], branch, readCommonOpts(cmd))
		},
	}
	cmd.Flags().SortFlags = false
	return cmd
}

func runGitPush(
	ctx context.Context,
	stdout, stderr io.Writer,
	instance, refspec string,
	opts commonOpts,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	if err := requireGitCwd(); err != nil {
		return err
	}
	pushSource, err := projectPushSource(home)
	if err != nil {
		return err
	}
	// A bare `git push INSTANCE` (no refspec) pushes to a branch named
	// after the instance — the same convention as `git pull` and
	// `workflow` — with the source taken from git.push-from.
	if refspec == "" {
		refspec = instance
	}
	return app.New(home).GitPush(ctx, stdout, stderr, app.GitPushRequest{
		Instance:     instance,
		Orchestrator: opts.orchestrator,
		Remote:       opts.remote,
		InstanceKey:  opts.instanceKey,
		Refspec:      refspec,
		PushSource:   pushSource,
	})
}

func runGitPull(
	ctx context.Context,
	stdout, stderr io.Writer,
	instance, branch string,
	opts commonOpts,
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

// projectPushSource returns the `git.push-from` default from the project's
// .codebox.conf (the working directory's config; the global file is
// ignored, matching port-forward). It is the source side filled into a
// push refspec when the operator omits it. An empty string means no
// default is configured — the callers turn that into a clear error only
// if the operator actually omitted the source.
func projectPushSource(home string) (string, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate current directory: %w", err)
	}
	_, project, err := settings.Load(home, workDir)
	if err != nil {
		return "", err
	}
	return project.Git.Push, nil
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
