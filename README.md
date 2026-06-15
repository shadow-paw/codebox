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

## Typical setups

The same four pieces appear in every deployment — they just land on
different machines:

- **VS Code** — your editor.
- **project** — the git checkout you edit, holding the project
  `./.codebox.conf`. `codebox` runs from here.
- **codebox** — this CLI; it drives the orchestrator and opens SSH
  sessions into the sandbox.
- **sandbox** — the container that runs the coding agent. It is an SSH
  endpoint with the repo checked out at `~/source`.

Where the orchestrator (Podman/Docker) runs is decided by the
`--remote` flag: omit it and the sandbox runs locally; set
`--remote=user@host` and `codebox` drives the orchestrator on that host
over SSH. The three layouts below cover almost every case.

In the diagrams, `══╪══ ssh ══` marks a machine boundary crossed over
SSH, and the double-ruled box is the sandbox container.

### 1. Full local

Everything on one workstation: you edit the project in VS Code, run
`codebox` from the same machine, and the sandbox container runs locally.
No `--remote`.

```
workstation ─────────────────────────────────────────────
   VS Code ── edits ─► [ project + ./.codebox.conf ]
   codebox ── drives ─► [ podman / docker ]
                              │ runs
                              ▼
   ╔═══════════════════════════════╗
   ║ sandbox container             ║
   ║   ~/source · agent · sshd     ║
   ╚═══════════════════════════════╝
      ▲
      └── codebox shell / VS Code Remote-SSH
```

```sh
codebox workflow issue-1234        # orchestrator + sandbox are local
```

Best for a workstation with enough CPU, RAM, and disk to host containers
directly.

### 2. Remote sandbox

You keep editing and running `codebox` on the workstation, but the
orchestrator — and therefore the sandbox — lives on a Linux host.
`codebox` drives it over SSH via `--remote`, and reaches the container
through the host as a jump (`ProxyJump`).

```
workstation ─────────────────────────────────────────────
   VS Code ── edits ─► [ project + ./.codebox.conf ]
   codebox
      │  --remote=user@linux   (drives the orchestrator over SSH)
      │
══════╪═══════════════ ssh ═══════════════════════════════
      ▼
linux host ──────────────────────────────────────────────
   [ podman / docker ]
      │ runs
      ▼
   ╔═══════════════════════════════╗
   ║ sandbox container             ║
   ║   ~/source · agent · sshd     ║
   ╚═══════════════════════════════╝
      ▲
      └── codebox shell / VS Code Remote-SSH
          (reaches the container via the linux host, ProxyJump)
```

```sh
codebox workflow issue-1234 --remote=user@linux
# or set it once in ~/.codebox.conf:  args.all: [ remote=user@linux ]
```

Best when your workstation is light (a laptop) but you have a beefy
build host. The project checkout and `git` stay on the workstation;
only the container workload moves.

### 3. Remote everything (VS Code Remote-SSH)

The workstation runs only VS Code, connected to the Linux host with
Remote-SSH. The project checkout, `codebox`, and the sandbox all live on
the host — from `codebox`'s point of view this is the *full local*
layout (setup 1), just driven through a VS Code Remote-SSH session.

```
workstation ─────────────────────────────────────────────
   VS Code
      │  Remote-SSH
══════╪═══════════════ ssh ═══════════════════════════════
      ▼
linux host ──────────────────────────────────────────────
   VS Code Server ── edits ─► [ project + ./.codebox.conf ]
   codebox ────────── drives ─► [ podman / docker ]
                                     │ runs
                                     ▼
   ╔═══════════════════════════════╗
   ║ sandbox container             ║
   ║   ~/source · agent · sshd     ║
   ╚═══════════════════════════════╝
      ▲
      └── codebox shell / VS Code terminal on the host
```

```sh
# in the VS Code Remote-SSH terminal, on the linux host:
codebox workflow issue-1234        # no --remote: orchestrator is local to the host
```

Best when the workstation is a thin client and you want a single Linux
environment that holds your editing, tooling, and sandboxes together.

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
codebox vscode demo                             # ...or open it in VS Code
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

Need a tool the flags don't cover? List shell commands under
`builder.additional-run` and each becomes an extra `RUN` step (run as
root, after the toolchains are installed and before the SSH key):
```yaml
builder:
  additional-run:
    - echo $(whoami)
```


For the full picture:

- [`doc/command.md`](doc/command.md) — CLI reference: invocation, subcommands, flags, defaults, exit codes.
- [`doc/config.md`](doc/config.md) — the global and project `.codebox.conf` files, with examples.
- [`doc/git.md`](doc/git.md) — the clone-push-shell-commit-pull workflow with worked examples.
