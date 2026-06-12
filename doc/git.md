# Git flow

The intended way to move code between your working tree and a
sandbox instance: clone locally, push to the sandbox, work inside
the sandbox, pull commits back.

The contract for the underlying commands is in
[`command.md`](command.md); this document is the walkthrough.

## Six-step flow

```sh
# 1. Clone the repository (or `cd` into an existing checkout).
git clone git@github.com:user/repo.git && cd repo

# 2. Provision a sandbox with whatever tooling you need.
codebox create instance_name --node=25 --claude

# 3. Push a branch derived from origin/main into the sandbox.
#    codebox fetches `origin` first, then pushes `origin/main` onto
#    a new branch `issue-1234` inside the sandbox. The repo lands at
#    ~/source on the instance — codebox always uses that path so one
#    sandbox holds exactly one checkout.
codebox git push instance_name origin/main:issue-1234

# 4. Drop into the sandbox.
codebox shell instance_name
#   user@instance_name:~$ cd ~/source

# 5. Use an agent (or your own editor) and `git commit` inside the
#    sandbox. The instance's per-repo `user.name` and `user.email`
#    were copied from your local git config at init time, so
#    attribution matches your usual identity.

# 6. Pull the sandbox's branch back as a remote-tracking ref. The
#    command also prints the exact command to materialise it as a
#    local branch. Omit the branch to default it to the instance name.
codebox git pull instance_name issue-1234
#   Fetched "issue-1234" from instance "instance_name".
#   To check it out locally:
#     git checkout codebox-instance_name/issue-1234 -b issue-1234
```

Both `git push` and `git pull` confirm the current directory is
the root of a git repository (has a `.git/` directory) before doing
any work; otherwise they fail with
`not a git repository: no .git directory in <cwd>`. Run them from
the same directory you would run plain `git` from.

Steps 2–4 (create, push, shell) collapse into a single command with
`codebox workflow`, which accepts every `create` flag and the same
refspec as `git push`:

```sh
codebox workflow origin/main:issue-1234 --node=25 --claude
```

See [`workflow`](command.md#codebox-workflow-refspec) for the full
contract.

## What `git push` does the first time

- Initialises `~/source` on the instance as a git repository (idempotent —
  subsequent pushes leave the existing repo alone).
- Sets `receive.denyCurrentBranch=updateInstead` so subsequent pushes
  to the currently checked-out branch update the working tree
  atomically (and refuse if it is dirty).
- Copies your local `user.name` and `user.email` into the instance's
  per-repo config so commits made inside the sandbox are attributed
  to you.
- Adds a remote named `codebox-<instance>` in your local repo whose
  URL points at the sandbox's ssh-published port. The `codebox-`
  prefix keeps it from colliding with remotes you set up by hand
  (`origin`, `upstream`, …). The URL is refreshed on every
  `push` / `pull`, so a restarted container with a new host port keeps
  working.

## Picking the refspec

`codebox git push INSTANCE REFSPEC` accepts two refspec shapes. The
shape codebox picks is determined by whether the part before `:`
contains a slash.

### Remote form: `source_remote/source_branch:target_branch`

| Component       | Meaning |
| --------------- | ------- |
| `source_remote` | A remote configured in your local repo (e.g. `origin`, `upstream`). codebox runs `git fetch <source_remote>` before pushing, so its tip is current. |
| `source_branch` | The branch on that remote (may itself contain slashes — `feature/x` is fine). |
| `target_branch` | The branch name created on the sandbox at `~/source` and checked out there. |

Common shapes:

| Refspec                       | Sandbox branch starts from |
| ----------------------------- | -------------------------- |
| `origin/main:issue-1234`      | The current tip of `origin/main`. |
| `origin/main:work`            | A scratch branch off main. |
| `upstream/release-2:hotfix`   | A hotfix branch off an upstream release. |
| `origin/feature/auth:auth-wip`| A WIP branch off the auth feature. |

### Local form: `local_branch:target_branch`

| Component       | Meaning |
| --------------- | ------- |
| `local_branch`  | A branch that already exists in your local repo. No slash, so codebox treats it as a local ref and skips the upstream fetch. |
| `target_branch` | The branch name created on the sandbox at `~/source` and checked out there. |

Use this form when your operator-side repo has no remote configured
(or you want to push a purely local branch without touching the
network):

```sh
codebox git push instance_name main:issue-1234
codebox git push instance_name wip:work
```

Local branches whose names contain slashes are ambiguous with the
remote form (`feature/x:work` would be read as remote `feature`,
branch `x`). To push such a branch, either configure a remote first
or rename the branch locally before pushing.

### Omitting the source: `git.push-from`

When a project always starts sandboxes from the same upstream, set the
source once in the project's `.codebox.conf` and leave it off the
command line:

```yaml
# ./.codebox.conf
git:
  push-from: origin/main
```

The source side of the refspec then becomes optional. Give just the
`target_branch` (or `:target_branch`) and the configured source is
filled in; dropping the refspec from `git push` entirely targets a
branch named after the instance:

```sh
codebox git push instance_name issue-1234   # == … origin/main:issue-1234
codebox git push instance_name :hotfix       # == … origin/main:hotfix
codebox git push instance_name               # == … origin/main:instance_name
codebox workflow issue-1234                   # == codebox workflow origin/main:issue-1234
```

A refspec that already names its own source is used as-is. With no
`git.push-from` configured, omitting the source is an error that asks for an
explicit one. See [`config.md`](config.md#gitpush-from--default-push-source-for-workflow-and-git-push)
for the full description.

## Restarted containers

The host-side port a sandbox publishes can change when the container
is recreated. Both `git push` and `git pull` re-resolve the port
each invocation and rewrite the local remote URL, so you don't need
to clean anything up by hand — just run the command again.

If you want to inspect the URL stored in your local config:

```sh
git remote get-url codebox-instance_name
```

## Pushing to the currently checked-out branch

After `codebox git push instance_name origin/main:issue-1234`, the
instance has `issue-1234` checked out at `~/source`. A subsequent
`codebox git push instance_name origin/main:issue-1234` is pushing to
the current branch — `receive.denyCurrentBranch=updateInstead`
handles this:

- If the instance-side working tree is **clean**, the push succeeds
  and the working tree is updated to the new HEAD.
- If the instance-side working tree is **dirty** (uncommitted
  changes), the push fails with a clear message. Commit, stash, or
  drop the changes inside the sandbox before retrying.

This is intentional: codebox never overwrites in-progress work on
the sandbox side silently.

## Removing the remote

`codebox git push` and `codebox git pull` only touch a single git
remote named `codebox-<instance>`. `codebox delete` already removes
this remote as part of its teardown, so the common case needs no
manual cleanup. To detach a sandbox from your local repo without
deleting it, drop the remote the usual way:

```sh
git remote remove codebox-instance_name
```

The next `codebox git push instance_name …` or
`codebox git pull instance_name …` will recreate it.
