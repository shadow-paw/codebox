# codebox

> Manage sandbox for coding agents.

`codebox` is a small, focused command-line tool for creating, inspecting, and
tearing down sandboxes that host autonomous coding agents. It is written in Go
and intentionally has no runtime dependencies beyond the standard library.

## Design principles

- **Orchestrate, don't reinvent.** Sandboxes are containers managed by an
  existing orchestrator ([Podman](https://podman.io/) or
  [Docker](https://www.docker.com/)). `codebox` drives it; it doesn't replace it.
- **Local or remote host, same UX.** The container may run on your machine
  or on a remote host. The host is configuration, not a separate code path.
- **SSH as the transport.** Every sandbox is an SSH endpoint, so `git`,
  `rsync`, `scp`, `sshfs`, and IDE remote plugins work out of the box.
- **Reachable through `ProxyJump`.** Sandboxes behind a bastion or gateway
  are first-class — same UX as a directly reachable host.
- **Opinionated defaults.** Sensible base images, tooling, and shell/editor
  configuration ship by default. Override when you need to.

## Prerequisites

- [Go](https://go.dev/dl/) **1.26.x** or newer.
- GNU `make` (any reasonably recent version).

Development tools (`golines`, `golangci-lint`, `govulncheck`) are invoked via
`go run` at pinned versions inside the `Makefile`, so there is no separate
install step.

## Installation

Build from source:

```sh
make deps
make            # equivalent to `make build`
```

The resulting binary is written to `./bin/codebox`. Move it onto your `PATH`
to install system-wide, for example:

```sh
install -m 0755 bin/codebox /usr/local/bin/codebox
```

## Usage

A typical end-to-end flow:

```sh
cd my-repo                                      # cd to a git repo
codebox create demo --python=3.14 --claude      # start sandbox with Python 3.14 & Claude Code
codebox git push demo origin/main:issue-1234    # origin/main → ~/source as issue-1234
codebox shell demo                              # ssh in
# inside the sandbox: let Claude (or you) write the commits
codebox git pull demo issue-1234                # issue-1234 → demo/issue-1234
codebox delete demo                             # stop and remove the sandbox
```

### Advanced usage

Create a `.codebox.conf`:
```yaml
args:
  create:
    - python=3.14
    - claude
    - claude-credentials
git:
  push-from: origin/main
```
then use the `workflow` shortcut:
```sh
codebox workflow issue-1234                     # create, git push, and shell in one command
# inside the sandbox: let Claude (or you) write the commits
codebox git pull issue-1234                     # branch defaults to the instance name
codebox delete issue-1234                       # stop and remove the sandbox
```
Agent flags are on/off toggles, so a `.codebox.conf` default can be turned
off for a single run from the command line:
```sh
codebox workflow origin/main:issue-1234 --claude=false   # skip the Claude install this time
```
> HINT: You can run multiple sandboxes at the same time.

To reach services running inside a sandbox, declare the forwards in the
project `.codebox.conf` (`local:remote`, or a bare port for `port:port`):
```yaml
port-forward:
  - 13000:3000
  - 13001:3001
```
```sh
codebox port-forward demo                       # hold the forwards open until Ctrl-C
```
When there is no `port-forward:` list but a compose file is present
(`compose.yaml`, `compose.yml`, `docker-compose.yaml`,
`docker-compose.yml`, `podman-compose.yaml`, or `podman-compose.yml`),
the command auto-detects the compose services' published ports and
forwards each to itself.


For the full picture:

- [`doc/command.md`](doc/command.md) — CLI reference: invocation, subcommands, flags, defaults, exit codes.
- [`doc/config.md`](doc/config.md) — the global and project `.codebox.conf` files, with examples.
- [`doc/git.md`](doc/git.md) — the clone-push-shell-commit-pull workflow with worked examples.
