# Configuration

`codebox` reads optional `.codebox.conf` files and folds their contents
into the command line before the flags are parsed. Nothing in a config
file does anything you could not do by typing flags by hand — it just
saves you from retyping the same flags on every invocation.

There are two files, both YAML, both optional:

| File                | Scope   | Purpose                                                        |
| ------------------- | ------- | -------------------------------------------------------------- |
| `~/.codebox.conf`   | Global  | Defaults that follow you across every project.                 |
| `./.codebox.conf`   | Project | Per-repository settings, read from the current directory.      |

A missing file is silently ignored, so you only create the ones you
need. When both exist they are merged (see [Precedence](#precedence)).

> The companion documents are [`command.md`](command.md) for the full
> flag/exit-code reference and [`git.md`](git.md) for the push/pull
> workflow. This document covers only the config files.

## File format

A config file has up to five top-level keys:

```yaml
args:
  all:            # flags injected before the subcommand (every command)
    - ...
  create:         # flags injected into `create` and `workflow`
    - ...
builder:
  additional-run: # custom RUN steps appended to the generated image build
    - ...
port-forward:     # forwards for `codebox port-forward`
  - ...
git:
  push-from: ...  # default push source for workflow / git push
```

Flag entries are written **without** the leading `--`. A boolean flag is
just its name; a flag that takes a value uses `name=value`:

```yaml
args:
  create:
    - claude          # becomes --claude
    - python=3.14     # becomes --python=3.14
```

## `args.all` — flags for every command

Entries under `args.all` are prepended before the subcommand, so they
apply to *every* `codebox` invocation. This is the right place for the
three root-level persistent flags — `--orchestrator`, `--remote`, and
`--instance-key`.

For example, to always talk to Docker instead of the default Podman, and
to always drive a remote host:

```yaml
# ~/.codebox.conf
args:
  all:
    - orchestrator=docker
    - remote=me@build-box.internal
```

With that in place, `codebox list` runs as if you had typed
`codebox --orchestrator=docker --remote=me@build-box.internal list`.

## `args.create` — flags for `create` and `workflow`

Entries under `args.create` are inserted right after the `create` token.
The `workflow` command runs `create` internally and exposes the same
flag set, so `args.create` applies to it too — anything else
(`shell`, `git`, `port-forward`, …) ignores this section.

This is where you pin the toolchain and agents a project's sandboxes
should always have:

```yaml
# ./.codebox.conf in a project that needs Node + Claude
args:
  create:
    - os=debian_13
    - node=25
    - claude
    - claude-credentials
```

Now a bare `codebox create demo` provisions the instance as though you
had written every flag out:

```sh
codebox create demo
# == codebox create demo --os=debian_13 --node=25 --claude --claude-credentials
```

and the `workflow` shortcut inherits the same setup:

```sh
codebox workflow origin/main:issue-1234   # create + git push + shell, fully configured
```

See [`command.md`](command.md) for the complete list of `create` flags
(`--python`, `--golang`, `--dotnet`, `--codex`, `--podman`, `--tmux`, …)
and their accepted values.

## `builder.additional-run` — custom image build steps

The flags above pick from the toolchains, agents, and tools `codebox`
knows how to install. When a project needs something off that list — an
extra global npm package, a linter, a system library — list the commands
under `builder.additional-run` and each becomes its own `RUN` in the
generated Dockerfile:

```yaml
# ./.codebox.conf
builder:
  additional-run:
    - echo $(whoami)
```

The steps are emitted in the **late build stage**: after the toolchains,
agents, and tools the flags requested are installed, and before the
operator's SSH key is added. Each command is written out verbatim as its
own `RUN` and runs as **root**, the same as the install layers above —
so a step that pulls system packages needs no `sudo`:

```yaml
builder:
  additional-run:
    - apt-get update && apt-get install -y --no-install-recommends graphviz
```

Because a `RUN` is not a login shell, address tools the toolchains
install by absolute path (e.g. `/home/user/.local/bin/uv`) rather than
relying on the in-container user's `PATH`.

The global `~/.codebox.conf` and the project `./.codebox.conf` both
contribute. The project steps run first, then the global steps — so a
project's own tooling lands before the org-wide global steps that follow
it. Blank list entries are ignored.

The commands are run verbatim, so a typo surfaces as an image-build
failure rather than a config error. `codebox create` echoes the full
generated Dockerfile before building, so you can see exactly where the
steps land.

## `port-forward` — forwards for `codebox port-forward`

The `port-forward` key lists the forwards that `codebox port-forward`
holds open. Each entry is `LOCAL:REMOTE`; a bare `PORT` maps that port to
itself (`PORT:PORT`):

```yaml
# ./.codebox.conf
port-forward:
  - 13000:3000     # localhost:13000 -> 3000 in the instance
  - 13001:3001
  - 8080           # localhost:8080  -> 8080 in the instance
```

```sh
codebox port-forward demo    # hold these forwards open until Ctrl-C
```

Both configs contribute: a `port-forward:` list in the global
`~/.codebox.conf` and one in the project `./.codebox.conf` are merged
(project entries first, then global) and deduplicated. When neither
config has a `port-forward:` list and a compose file is present in the
current directory, `codebox port-forward` auto-detects the compose
services' published ports instead — see the
[`codebox port-forward`](command.md) reference for the auto-detection
rules.

## `git.push-from` — default push source for `workflow` and `git push`

`codebox workflow` and `codebox git push` take a refspec that names a
source and a target — `source_remote/source_branch:target_branch` (or
`local_branch:target_branch`). When you always start sandboxes from the
same upstream, set `git.push-from` to that source side and omit it from the
command line:

```yaml
# ./.codebox.conf
git:
  push-from: origin/main
```

With that in place, the source may be left off the refspec — write just
the `target_branch` (or `:target_branch`) and the configured source is
filled in:

```sh
codebox workflow issue-1234        # == codebox workflow origin/main:issue-1234
codebox git push issue-1234        # == codebox git push issue-1234 origin/main:issue-1234
codebox git push demo :hotfix      # == codebox git push demo origin/main:hotfix
```

A refspec that already carries its own source (`upstream/dev:work`,
`main:work`) is used as-is and ignores `git.push-from`. When the source is
omitted and no `git.push-from` is configured, the command fails and tells you
to pass an explicit source.

Both configs contribute, but this key is a single value rather than a
list, so it does not merge: the project's `git.push-from` wins when set,
and the global `~/.codebox.conf` value is used only as a fallback.

## Precedence

When the same flag could come from more than one place, the most
specific source wins:

```
explicit CLI flag  >  project .codebox.conf  >  global ~/.codebox.conf
```

- **CLI overrides config.** A flag you type on the command line is never
  overridden or duplicated by a config entry. `codebox create demo --node=24`
  uses Node 24 even if `args.create` pins `node=25`.
- **Project overrides global.** When both config files set the same flag,
  the project value replaces the global one (matched by flag name, so
  `node=25` in the project beats `node=24` in the global file).
- **Otherwise they merge.** Flags set in only one file are all applied.

This precedence governs the flag sections. The remaining keys are not
flags and follow their own rules:

- **List keys merge, project-first.** `builder.additional-run` and
  `port-forward` concatenate the project list ahead of the global one
  (`port-forward` also deduplicates), so both files always contribute
  and the project's entries come first.
- **`git.push-from` is a single value.** The project's value wins when
  set; the global value is only a fallback.

## A complete example

A global file with your cross-project defaults:

```yaml
# ~/.codebox.conf
args:
  all:
    - orchestrator=docker
  create:
    - claude
    - claude-credentials
```

A project file that adds a toolchain and declares its forwards:

```yaml
# ./.codebox.conf
args:
  create:
    - node=25
    - psql
port-forward:
  - 3000
  - 5432
```

With both in place:

```sh
codebox create demo
# == codebox --orchestrator=docker create demo \
#      --claude --claude-credentials --node=25 --psql

codebox port-forward demo
# forwards localhost:3000 -> 3000 and localhost:5432 -> 5432
```
