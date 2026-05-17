# codebox command specification

Authoritative reference for the `codebox` CLI surface. This document
describes the user-facing contract — invocation, flags, defaults, and exit
behaviour. The Go code in [`internal/cli`](../internal/cli) is the
implementation of this contract; if the two ever disagree, this file is
canonical and the code should be updated.

## Invocation

```
codebox [command] [flags] [arguments]
```

- Running `codebox` with **no arguments** prints the banner followed by the
  top-level help text and exits with code `0`.
- The banner (ASCII art, version, project URL, blank line) is written to
  **stdout** on every invocation, including `--help` and `--version`.
- `--help` is accepted at every level (`codebox --help`,
  `codebox <command> --help`).
- `--version` prints the version tag and exits.
- Help text preserves the flag order documented in this file — flags are
  never alphabetised.

## Common conventions

| Flag             | Type     | Default  | Notes |
| ---------------- | -------- | -------- | ----- |
| `--orchestrator` | enum     | `podman` | One of `podman`, `docker`. |
| `--remote`       | string   | *(unset)* | `user@host`. When omitted, the orchestrator runs on the local machine. SSH is the transport; `ProxyJump` configured in `~/.ssh/config` is honoured automatically. |
| `--instance-key` | path     | *(auto)* | SSH private key used to log into the instance. When unset, codebox tries the user's default keys. |

Exit codes:

| Code | Meaning |
| ---- | ------- |
| `0`  | Success. |
| `1`  | Generic failure (parse error, runtime error). |
| `130`| Interrupted by signal (SIGINT/SIGTERM). |

## Commands

### `codebox create INSTANCE`

Create a new sandbox instance.

```
codebox create demo \
  --orchestrator=podman --remote=user@host --instance-key=~/.ssh/id_rsa \
  --os=debian_12 \
  --python=3.14 --node=24 --golang=1.26.0 --dotnet=10 \
  --claude --codex --opencode --podman --psql
```

Flags (in help order):

| Flag             | Type   | Default        | Description |
| ---------------- | ------ | -------------- | ----------- |
| `--orchestrator` | enum   | `podman`       | Container orchestrator (`podman`, `docker`). |
| `--remote`       | string | *(local)*      | Provision on a remote host (`user@host`). |
| `--instance-key` | path   | *(auto)*       | SSH key for logging into the new instance. |
| `--rebuild`      | bool   | `false`        | Force a rebuild of the base image even if a cached one exists. |
| `--os`           | enum   | `debian_13`    | Base OS image (`debian_12`, `debian_13`, `ubuntu_24`, `ubuntu_26`, `redhat_10`). |
| `--python`       | enum   | *(none)*       | Install Python at `3.12`, `3.13`, or `3.14`. |
| `--node`         | enum   | *(none)*       | Install Node.js at major version `24`, `25`, or `26`. |
| `--golang`       | enum   | *(none)*       | Install Go at version `1.26.0`. |
| `--dotnet`       | enum   | *(none)*       | Install .NET at version `8` or `10`. |
| `--claude`       | bool   | `false`        | Install Claude Code. |
| `--codex`        | bool   | `false`        | Install OpenAI Codex CLI. |
| `--opencode`     | bool   | `false`        | Install opencode. |
| `--podman`       | bool   | `false`        | Install rootless Podman inside the instance. |
| `--psql`         | bool   | `false`        | Install the psql PostgreSQL client. |

The `--help` output for `create` ends with five footer sections that
restate the legal values for each kind of flag:

- **Orchestrators** — values accepted by `--orchestrator`.
- **OS** — values accepted by `--os`, with a human-readable label.
- **Languages** — language runtime flags and their version values.
- **Agents** — coding-agent install flags.
- **Tools** — other tool install flags.

### `codebox delete INSTANCE`

Delete a sandbox instance. The container is stopped and removed.

```
codebox delete demo --orchestrator=podman --remote=user@host
```

| Flag             | Type   | Default   | Description |
| ---------------- | ------ | --------- | ----------- |
| `--orchestrator` | enum   | `podman`  | Container orchestrator (`podman`, `docker`). |
| `--remote`       | string | *(local)* | Target a remote host (`user@host`). |

### `codebox list`

List sandbox instances managed by the chosen orchestrator.

```
codebox list --orchestrator=podman --remote=user@host
```

| Flag             | Type   | Default   | Description |
| ---------------- | ------ | --------- | ----------- |
| `--orchestrator` | enum   | `podman`  | Container orchestrator (`podman`, `docker`). |
| `--remote`       | string | *(local)* | Target a remote host (`user@host`). |

### `codebox shell INSTANCE`

Open an interactive shell into a sandbox instance over SSH.

```
codebox shell demo \
  --orchestrator=podman --remote=user@host --instance-key=~/.ssh/id_rsa \
  --port=8000:3000 --port=8001:3001
```

| Flag             | Type      | Default   | Description |
| ---------------- | --------- | --------- | ----------- |
| `--orchestrator` | enum      | `podman`  | Container orchestrator (`podman`, `docker`). |
| `--remote`       | string    | *(local)* | Target a remote host (`user@host`). |
| `--instance-key` | path      | *(auto)*  | SSH key for logging into the instance. |
| `--port`         | `L:R` (repeatable) | *(none)* | Forward `LOCAL:REMOTE` for the lifetime of the shell. Repeat for multiple forwards. |

### `codebox exec INSTANCE -- COMMAND [ARGS...]`

Execute a command inside a sandbox instance and exit with the command's
status code. The `--` separator is **required**: it marks the end of
codebox's own flags, so `COMMAND` and any flag-shaped arguments after it
(e.g. `-la`) are forwarded verbatim to the inner command.

```
codebox exec demo \
  --orchestrator=podman --remote=user@host --instance-key=~/.ssh/id_rsa \
  -- pytest -x tests/
```

| Flag             | Type   | Default   | Description |
| ---------------- | ------ | --------- | ----------- |
| `--orchestrator` | enum   | `podman`  | Container orchestrator (`podman`, `docker`). |
| `--remote`       | string | *(local)* | Target a remote host (`user@host`). |
| `--instance-key` | path   | *(auto)*  | SSH key for logging into the instance. |

Positional arguments:

| Argument  | Required | Description |
| --------- | -------- | ----------- |
| `INSTANCE`| yes      | Name of the target instance. Must appear before `--`. |
| `COMMAND` | yes      | First token after `--`; the command to run inside the instance. |
| `ARGS...` | no       | Remaining tokens after `--`, forwarded to `COMMAND` unchanged. |

Invocations without `--`, or with extra positionals before it, are
rejected with a non-zero exit code.

### `codebox pull INSTANCE`

Copy a file or directory from a sandbox instance down to the local machine.

```
codebox pull demo \
  --orchestrator=podman --remote=user@host --instance-key=~/.ssh/id_rsa \
  --instance-path=/workspace/out --local-path=./results
```

| Flag              | Type   | Default   | Description |
| ----------------- | ------ | --------- | ----------- |
| `--orchestrator`  | enum   | `podman`  | Container orchestrator (`podman`, `docker`). |
| `--remote`        | string | *(local)* | Target a remote host (`user@host`). |
| `--instance-key`  | path   | *(auto)*  | SSH key for logging into the instance. |
| `--instance-path` | path   | *(unset)* | File or directory on the instance to copy from. |
| `--local-path`    | path   | *(unset)* | Local directory to copy into. |

### `codebox push INSTANCE`

Copy a file or directory from the local machine up to a sandbox instance.

```
codebox push demo \
  --orchestrator=podman --remote=user@host --instance-key=~/.ssh/id_rsa \
  --local-path=./payload --instance-path=/workspace/in
```

| Flag              | Type   | Default   | Description |
| ----------------- | ------ | --------- | ----------- |
| `--orchestrator`  | enum   | `podman`  | Container orchestrator (`podman`, `docker`). |
| `--remote`        | string | *(local)* | Target a remote host (`user@host`). |
| `--instance-key`  | path   | *(auto)*  | SSH key for logging into the instance. |
| `--local-path`    | path   | *(unset)* | File or directory on the local machine to copy from. |
| `--instance-path` | path   | *(unset)* | Directory on the instance to copy into. |

## Status

All commands listed here are wired up to the cobra parser but their action
layer is a no-op. They accept and validate flags, then return success
without performing any orchestrator, SSH, or file-transfer work. The
behaviours described above are the **specification** that future
implementation work is held against.
