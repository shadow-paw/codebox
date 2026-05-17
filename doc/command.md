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

## `create` provisioning

`create` is fully wired today: it builds the image and starts the
container. The generated Dockerfile is printed to stdout, bracketed
by a labelled horizontal rule and a matching closing rule, so
operators can audit exactly what they are about to provision; the
engine's build progress, the success line, and the shell hint follow.

### Instance name

The positional `INSTANCE` argument must match `^[A-Za-z0-9_-]{1,32}$`:

- non-empty,
- at most 32 characters,
- characters from `A-Z`, `a-z`, `0-9`, `_`, `-`.

Codebox uses the instance name verbatim as the container name and the
image tag, so the cap stays comfortably inside engine-specific limits
while leaving room for descriptive suffixes (`project-feature-sha`).
Invalid names fail fast — no orchestrator command is issued.

### Flow

For each invocation the use-case layer performs, in order:

1. **Pre-existence check.** `<engine> ps -a --format '{{.Names}}'` is
   run (locally or via ssh). If a container with the requested name
   already exists, the command fails with a hint:

   ```
   Error: instance "demo" already exists; stop and delete it first:
     codebox delete demo
   ```

   The `--orchestrator` and `--remote` flags are echoed back in the
   hint when they differ from the defaults.

2. **Image build.** A Dockerfile is generated in memory and piped to
   `<engine> build -t INSTANCE -f -` whose context is a fresh
   `mktemp -d` directory (trapped for cleanup on exit). The empty
   context guarantees no files from the operator's working tree leak
   into the image. `--rebuild` adds `--no-cache`. Build output is
   streamed to the operator's terminal as it is produced.

3. **Container start.** `<engine> run -d --name INSTANCE --hostname
   INSTANCE --label codebox=true --publish-all INSTANCE`. The hostname
   is set so an interactive shell inside the container makes the
   sandbox immediately identifiable. Failures surface the engine's
   stderr verbatim.

4. **Success line.** A copy-paste-ready `codebox shell` command is
   printed on the line after a one-line success message, indented by
   two spaces. `--orchestrator`, `--remote`, and `--ssh-key` are
   included only when the operator supplied a non-default value:

   ```
   Instance "demo" is ready. Open a shell:
     codebox shell demo --remote=user@host --ssh-key=~/.ssh/id_rsa
   ```

### Transport

When `--remote=user@host` is set, each step's shell command is sent via
`ssh user@host '<command>'`. The operator's normal SSH configuration
(`~/.ssh/config`, ssh-agent, default keys) is used; the
`--instance-key` value is **not** passed to ssh — it is only embedded
into the container's `authorized_keys`. SSH connection failures (exit
status 255) surface as a distinct error message naming the host, so
the operator can tell them apart from build or run failures.

## Dockerfile rendering

### Base images

| `--os`      | `FROM` reference              |
| ----------- | ----------------------------- |
| `debian_12` | `docker.io/debian:12.13`      |
| `debian_13` | `docker.io/debian:13.4`       |
| `ubuntu_24` | `docker.io/ubuntu:24.04`      |
| `ubuntu_26` | `docker.io/ubuntu:26.04`      |
| `redhat_10` | `docker.io/redhat/ubi10:10.1` |

### Layer order

The Dockerfile is built in this order so that an unrelated change to a
later layer does not invalidate the package install cache:

1. Install base packages — `ca-certificates`, `nano`, `vim`, `sudo`,
   `openssl`, `openssh-server`, `rsync`, `git`,
   `iputils-ping`/`iputils`, `dnsutils`/`bind-utils`, `curl`. Names are
   remapped per distro family. The distro's build toolchain
   (`build-essential` on apt, `"Development Tools"` group on dnf) is
   installed in the same layer. Language and tool flags (`--python`,
   `--claude`, ...) are accepted but not yet installed.
2. OS-specific fixes (`debian_13`, `ubuntu_26`, `redhat_10` only):
   overwrite `/etc/pam.d/sudo` with the minimal container-friendly
   stack.
3. Create user `user` with a locked password slot (`useradd`), then
   mark the account unlocked with `usermod -p '*NP' user` so pubkey
   login succeeds.
4. Configure sshd: create `/run/sshd`, relax `pam_loginuid` to
   `optional`, and drop a `10-codebox.conf` into `/etc/ssh/sshd_config.d`
   with `Port 2222`, `PubkeyAuthentication yes`,
   `PasswordAuthentication no`, `UsePAM no`.
5. Configure sudoers: passwordless sudo for `user` via
   `/etc/sudoers.d/user` (mode 0440).
6. Init script `/usr/local/bin/codebox-init` that execs `sshd` and
   `sleep infinity`.
7. Install the operator's public key into
   `/home/user/.ssh/authorized_keys` (mode 0600, owned by `user`).
8. `EXPOSE 2222`, `CMD ["/usr/local/bin/codebox-init"]`.

### `--instance-key` resolution

| Input                              | Behaviour |
| ---------------------------------- | --------- |
| Path ending in `.pub`              | Read directly. |
| Path **not** ending in `.pub`      | `.pub` is appended, then read. |
| Leading `~/` or bare `~`           | Expanded against the operator's home directory. |
| Omitted                            | Scan `~/.ssh/` for `*.pub`. Exactly one match is required; zero or multiple matches return an error naming the candidates and asking the operator to pass `--instance-key`. |

## `delete` teardown

`delete` is fully wired today. The use-case layer performs, in order:

1. **Existence check.** `<engine> ps -a --format '{{.Names}}'` is run
   (locally or via ssh). If the instance is not present, the command
   fails with `instance "NAME" not found` and exits non-zero.
2. **Running check.** `<engine> ps --format '{{.Names}}'` lists only
   running containers.
3. **Stop (conditional).** If the container is running, codebox prints
   `Stopping container "NAME"...` and runs `<engine> stop NAME`. A
   failure surfaces the engine's stderr; the remove and untag steps
   are skipped.
4. **Remove.** Codebox prints `Deleting container "NAME"...` and runs
   `<engine> rm NAME`.
5. **Untag.** `<engine> untag NAME` is run silently to drop every tag
   on the image codebox built for the instance.

Engine stdout (which otherwise echoes the container/image name) is
captured to internal buffers throughout — only the human-readable
progress lines codebox prints reach the operator's terminal.

## `list` enumeration

`list` is fully wired today. The use-case layer runs

```
<engine> ps -a --filter label=codebox=true \
  --format '{{.Names}}|{{.CreatedAt}}|{{.Ports}}'
```

against the chosen target (locally or via ssh on `--remote`) and
prints a three-column table to stdout:

| Column        | Source                                                  |
| ------------- | ------------------------------------------------------- |
| `INSTANCE`    | `{{.Names}}` |
| `AGE`         | `time.Now() − {{.CreatedAt}}`, rendered in the largest non-zero unit (`<1 min`, `N min`, `N hr`, `N day`). Unparseable timestamps render as `?`. |
| `SSH COMMAND` | Copy-paste shell hint targeting the container's sshd. |

The `SSH COMMAND` column hard-codes the in-container login (`user`)
and sshd port (`2222`). Stopped containers have no published port and
surface `(stopped)` in place of an unusable hint. Otherwise:

- **Local**:
  `ssh -o StrictHostKeyChecking=no user@localhost -p <host_port>`
- **Remote** (`--remote=ops@bastion`):
  `ssh -o StrictHostKeyChecking=no -J ops@bastion user@localhost -p <host_port>`

When no codebox containers are present, the single line
`No codebox instances found.` is printed and the command exits `0`.

## `shell` interactive session

`shell` is fully wired today. The use-case layer performs, in order:

1. **Existence check.** `<engine> ps -a --format '{{.Names}}'` is run
   against `--remote` (locally if unset). When the instance is missing,
   the command fails with `instance "NAME" not found` and exits
   non-zero before any further work.
2. **Host port lookup.** `<engine> port NAME 2222` is run on the same
   target; the first `<addr>:<port>` line is parsed and the numeric
   port retained. A stopped container produces no mapping and surfaces
   `instance "NAME" is not exposing port 2222; is it running?`.
3. **Interactive ssh.** A locally-exec'd `ssh` connects to the
   container's published port with stdin/stdout/stderr passed through
   unchanged, so the operator gets a real tty. The command shape is:

   - **Local** (no `--remote`):
     `ssh -o StrictHostKeyChecking=no [-i KEY] [-L L:localhost:R ...] user@localhost -p PORT`
   - **Remote** (`--remote=ops@bastion`):
     `ssh -o StrictHostKeyChecking=no [-i KEY] [-L L:localhost:R ...] -J ops@bastion user@localhost -p PORT`

   `--instance-key` is `~`-expanded and passed as `-i`; it is **never**
   passed to the orchestrator-bound ssh that ran step 1 and 2. Each
   `--port=L:R` becomes `-L L:localhost:R` so the remote end is
   interpreted on the container side of any `-J` jump.

Connection-level ssh failures (exit status 255) bubble up as
`ssh: could not connect to <host>` so the operator can distinguish
them from a non-zero exit from the in-container shell.

## `exec` command execution

`exec` is fully wired today. The use-case layer performs, in order:

1. **Existence check.** `<engine> ps -a --format '{{.Names}}'` is run
   against `--remote` (locally if unset). When the instance is missing,
   the command fails with `instance "NAME" not found` and exits
   non-zero before any further work.
2. **Host port lookup.** `<engine> port NAME 2222` is run on the same
   target; the first `<addr>:<port>` line is parsed and the numeric
   port retained. A stopped container produces no mapping and surfaces
   `instance "NAME" is not exposing port 2222; is it running?`.
3. **Remote command.** A locally-exec'd `ssh` connects to the
   container's published port with stdin/stdout/stderr passed through
   unchanged, so callers can pipe data in or out. The command shape is:

   - **Local** (no `--remote`):
     `ssh -o StrictHostKeyChecking=no [-i KEY] user@localhost -p PORT '<inner>'`
   - **Remote** (`--remote=ops@bastion`):
     `ssh -o StrictHostKeyChecking=no [-i KEY] -J ops@bastion user@localhost -p PORT '<inner>'`

   `--instance-key` is `~`-expanded and passed as `-i` on the
   container-bound ssh only; it is **never** passed to the
   orchestrator-bound ssh that ran steps 1 and 2. `<inner>` is
   `COMMAND` followed by each `ARG`, single-quoted individually so the
   in-container login shell preserves argument boundaries (spaces and
   shell metacharacters survive verbatim).

Codebox exits with the inner command's status code; connection-level
ssh failures (exit status 255) surface as a distinct error naming the
host so they can be told apart from a non-zero exit from the inner
command.

## Status

`create`, `delete`, `list`, `shell`, and `exec` are implemented
end-to-end. `pull` and `push` are wired up to the cobra parser but
their action layer is still a no-op: they accept and validate flags,
then return success without performing any orchestrator, SSH, or
file-transfer work. The behaviours described above are the
**specification** that future implementation work is held against.
