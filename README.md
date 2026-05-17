# codebox

> Manage sandbox for coding agents.

`codebox` is a small, focused command-line tool for creating, inspecting, and
tearing down sandboxes that host autonomous coding agents. It is written in
Go and intentionally has no runtime dependencies beyond the standard library.

## Design principles

- **Orchestrate, don't reinvent.** Sandboxes are containers managed by an
  existing orchestrator ([Podman](https://podman.io/) or
  [Docker](https://www.docker.com/)). `codebox` drives it; it doesn't replace it.
- **Local or remote host, same UX.** The container may run on your machine
  or on a remote host. The host is configuration, not a separate code path.
- **SSH as the transport.** Every sandbox is an SSH endpoint, so `git`,
  `rsync`, `scp`, `sshfs`, and IDE remote plugins work out of the box.
- **Reachable through `ProxyJump`.** Sandboxes behind a bastion or gateway
  are first-class — same UX as a directly reachable one.
- **Opinionated defaults.** Sensible base images, tooling, and shell/editor
  configuration ship by default. Override when you need to.

## Prerequisite

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

See [`doc/command.md`](doc/command.md) for the full CLI reference —
invocation, subcommands, flags, defaults, and exit codes.

Available `make` targets:

```sh
make deps      # download and tidy module dependencies
make format    # format all sources at 120 cols (golines + gofmt)
make lint      # run golangci-lint
make test      # run unit tests (race detector + coverage)
make audit     # govulncheck + go mod verify
make build     # produce ./bin/codebox (default target)
make clean     # remove build artefacts
```
