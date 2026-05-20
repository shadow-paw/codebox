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
  --https-proxy=http://proxy.corp:3128 \
  --os=debian_12 \
  --python=3.14 --node=24 --golang=1.26.0 --dotnet=10 \
  --claude --claude-credentials --codex --opencode --podman --psql
```

Flags (in help order):

| Flag                    | Type   | Default        | Description |
| ----------------------- | ------ | -------------- | ----------- |
| `--orchestrator`        | enum   | `podman`       | Container orchestrator (`podman`, `docker`). |
| `--remote`              | string | *(local)*      | Provision on a remote host (`user@host`). |
| `--instance-key`        | path   | *(auto)*       | SSH key for logging into the new instance. |
| `--rebuild`             | bool   | `false`        | Force a rebuild of the base image even if a cached one exists. |
| `--https-proxy`         | string | *(unset)*      | Append `export HTTPS_PROXY="<value>"` to the in-container user's login profile so interactive shells route HTTPS through the configured proxy. Other build-time downloads (apt, dnf, Go, .NET, uv, nvm) still go through the builder host's network — only the operator's shell inside the running container sees the proxy. The one exception is the `--claude` install layer, which exports `HTTPS_PROXY` inline so curl (and any sub-downloads the install script performs) route through the proxy too. The value is not validated; pass it the way curl would accept it (`http://proxy:3128`, `http://user:pw@proxy:3128`, ...). |
| `--os`                  | enum   | `debian_13`    | Base OS image (`debian_12`, `debian_13`, `ubuntu_24`, `ubuntu_26`, `redhat_10`). |
| `--python`              | enum   | *(none)*       | Install Python at `3.12`, `3.13`, or `3.14`. |
| `--node`                | enum   | *(none)*       | Install Node.js at major version `24`, `25`, or `26`. |
| `--golang`              | enum   | *(none)*       | Install Go at version `1.26.0`. |
| `--dotnet`              | enum   | *(none)*       | Install .NET at version `8` or `10`. |
| `--claude`              | bool   | `false`        | Install [Claude Code](https://claude.ai/code) via Anthropic's native installer. Also drops `/home/user/.claude.json` with the onboarding flag pre-set so the CLI does not prompt on first run inside the sandbox. |
| `--claude-credentials`  | bool   | `false`        | After the container starts, rsync `~/.claude/.credentials.json` from the operator's machine into `/home/user/.claude/.credentials.json` inside the instance. Requires `--claude` and fails fast if it is not set. Credentials are **never** baked into the image. |
| `--codex`               | bool   | `false`        | Install OpenAI Codex CLI. |
| `--opencode`            | bool   | `false`        | Install opencode. |
| `--podman`              | bool   | `false`        | Install rootless Podman inside the instance. |
| `--psql`                | bool   | `false`        | Install the psql PostgreSQL client. |

The `--help` output for `create` ends with six footer sections that
restate the legal values for each kind of flag:

- **Orchestrators** — values accepted by `--orchestrator`.
- **OS** — values accepted by `--os`, with a human-readable label.
- **Languages** — language runtime flags and their version values.
- **Agents** — coding-agent install flags.
- **Tools** — other tool install flags.
- **Network** — proxy and other network-related knobs.

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

### `codebox git push INSTANCE source_remote/source_branch:target_branch`

Push a fetched remote-tracking ref into a sandbox instance and check
the resulting branch out at `~/source` inside the container. One
repository per sandbox: codebox always uses `~/source` so the operator
never has to remember a per-checkout path.

```
codebox git push demo origin/main:issue-1234 \
  --orchestrator=podman --remote=user@host --instance-key=~/.ssh/id_rsa
```

| Flag             | Type   | Default   | Description |
| ---------------- | ------ | --------- | ----------- |
| `--orchestrator` | enum   | `podman`  | Container orchestrator (`podman`, `docker`). |
| `--remote`       | string | *(local)* | Target a remote host (`user@host`). |
| `--instance-key` | path   | *(auto)*  | SSH key for logging into the instance. |

Positional arguments:

| Argument | Required | Description |
| -------- | -------- | ----------- |
| `INSTANCE` | yes | Name of the target sandbox instance. |
| `source_remote/source_branch:target_branch` | yes | Source remote and branch in the operator's repo (the part before `:`); `target_branch` is the branch name created on the instance and checked out at `~/source`. |

### `codebox git pull INSTANCE BRANCH`

Fetch a branch from a sandbox instance into a remote-tracking ref on
the operator's machine (`refs/remotes/INSTANCE/BRANCH`), then print a
hint showing how to check it out locally.

```
codebox git pull demo issue-1234 \
  --orchestrator=podman --remote=user@host --instance-key=~/.ssh/id_rsa
```

| Flag             | Type   | Default   | Description |
| ---------------- | ------ | --------- | ----------- |
| `--orchestrator` | enum   | `podman`  | Container orchestrator (`podman`, `docker`). |
| `--remote`       | string | *(local)* | Target a remote host (`user@host`). |
| `--instance-key` | path   | *(auto)*  | SSH key for logging into the instance. |

Positional arguments:

| Argument   | Required | Description |
| ---------- | -------- | ----------- |
| `INSTANCE` | yes      | Name of the source sandbox instance. |
| `BRANCH`   | yes      | Branch on the instance side to fetch. |

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
   two spaces. `--orchestrator`, `--remote`, and `--instance-key` are
   included only when the operator supplied a non-default value:

   ```
   Instance "demo" is ready. Open a shell:
     codebox shell demo --remote=user@host --instance-key=~/.ssh/id_rsa
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
   installed in the same layer.
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
7. Optional language/tool layers (see [Optional toolchains](#optional-toolchains)).
   Skipped entirely when no flag is set. `--codex`, `--opencode`, and
   `--podman` are flag-bound for forward compatibility but currently
   fail the command with `<flag> not yet supported` before any
   orchestrator call.
8. Install the operator's public key into
   `/home/user/.ssh/authorized_keys` (mode 0600, owned by `user`).
9. `EXPOSE 2222`, `CMD ["/usr/local/bin/codebox-init"]`.

### Optional toolchains

Each flag emits its own layer in the slot between the init script and
the public-key install. Layers that touch system paths run as root;
home-scoped installs (`uv`, `nvm`) switch to `USER user` and then
back to `USER root` so the subsequent key install retains its
permissions.

Version values are validated against the documented sets before any
Dockerfile is emitted; an out-of-set value (e.g. `--python=3.10`) fails
with `image: unsupported <flag> version "<value>" (known: ...)` and no
orchestrator command is issued.

PATH and toolchain exports are appended to the per-family login profile
so interactive shells pick them up: `/home/user/.profile` on apt-family
distros (Debian, Ubuntu) and `/home/user/.bash_profile` on dnf-family
distros (Red Hat). Below, **PROFILE** refers to whichever file applies.

| Flag       | Installs |
| ---------- | -------- |
| `--psql`   | `postgresql-client` (apt) or `postgresql` (dnf) via the distro package manager. |
| `--golang=VER` | Downloads `https://go.dev/dl/goVER.linux-${arch}.tar.gz` (arch detected from `uname -m`; `amd64` and `arm64` supported), unpacks it to `/usr/local/go`, and appends `export PATH="/usr/local/go/bin:$PATH"` to **PROFILE**. |
| `--dotnet=VER` | Runs `https://dot.net/v1/dotnet-install.sh --channel VER.0 --install-dir /usr/local/dotnet`, symlinks the runner to `/usr/local/bin/dotnet`, and appends `DOTNET_ROOT`, `PATH`, and `DOTNET_CLI_TELEMETRY_OPTOUT=1` exports to **PROFILE**. |
| `--python=VER` | Runs `https://astral.sh/uv/install.sh` as user `user`, then runs `uv python install VER && uv python pin --global VER` to download the prebuilt CPython and set it as the global default for `uv`. (The `export PATH="$HOME/.local/bin:$PATH"` line is emitted once — see `--claude`.) |
| `--node=VER` | On dnf-family distros, first installs `libatomic` (UBI omits it and recent V8 binaries link against it). Then installs nvm (pinned to `v0.40.1`) as user `user`, and runs `nvm install VER && nvm alias default VER`. |
| `--claude` | Runs Anthropic's native installer (`curl -fsSL https://claude.ai/install.sh \| bash`) as user `user`. The installer drops the `claude` binary into `$HOME/.local/bin`; the corresponding PATH export is emitted once when `--claude` or `--python` is set. When `--https-proxy` is also set, the proxy is exported inline for this RUN (`export HTTPS_PROXY='URL' && curl ...`) so the install pipeline routes through it. The same layer also drops `/home/user/.claude.json` (owned by `user`) with `{"hasCompletedOnboarding": true, "defaultMode": "bypassPermissions"}` so the CLI skips the first-run prompt inside the sandbox. Credentials are not baked in — pass `--claude-credentials` to push them after the container starts. |

### HTTPS proxy

When `--https-proxy=URL` is set, codebox appends the line

```
export HTTPS_PROXY="URL"
```

to the in-container user's login profile — **PROFILE** as defined
above (`/home/user/.profile` on apt-family distros,
`/home/user/.bash_profile` on dnf-family distros). The proxy is **not**
emitted as an `ENV` directive: image build downloads continue to use
the builder host's normal network, and the proxy only takes effect
once a login shell sources the profile (interactive `codebox shell`
sessions, ssh login sessions targeting the container).

Single quotes in the value are shell-escaped before being written so
the surrounding `echo` invocation survives an embedded apostrophe;
otherwise the value is passed through verbatim
(`http://proxy:3128`, `http://user:pw@proxy:3128`, ...).

### Claude credentials transfer

`--claude-credentials` requires `--claude`. Setting credentials
without the CLI install is rejected by the CLI parser before any
work happens (`--claude-credentials requires --claude`). When both
flags are set, the flag is honoured **after** the container's `run`
step succeeds. The use-case layer:

1. `stat`s `~/.claude/.credentials.json` on the operator's machine
   **before** any orchestrator command is issued. A missing or
   unreadable file fails the command with the underlying OS error
   (the flag name is included in the wrapper), so the operator does
   not have to wait for an image build to surface the problem.
2. Looks up the host-side port for the in-container sshd via
   `<engine> port INSTANCE 2222`, exactly like `push` / `pull`.
3. Echoes the assembled rsync command bracketed by horizontal rules
   (mirroring the Dockerfile and `push`/`pull` blocks) and runs it
   **locally** so progress streams to the operator's terminal.
4. If the first rsync fails (most often because sshd inside the
   container is still coming up), waits **2 seconds** and retries
   **exactly once**. The retry message names the original error so
   the operator can see what is being recovered from. If the retry
   also fails, that error is returned.

The rsync invocation has the shape:

```
rsync --verbose --archive --compress --update --progress \
  --mkpath --chmod=F0600 \
  -e 'ssh -o StrictHostKeyChecking=no [-i KEY] [-J Remote] -p PORT' \
  ~/.claude/.credentials.json user@localhost:/home/user/.claude/.credentials.json
```

- `--mkpath` ensures `/home/user/.claude` is created on the receiving
  side even when neither `--claude` nor a previous run laid it down.
- `--chmod=F0600` pins the file mode so the credentials land with the
  same permissions Claude expects on the host.

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
6. **Local git remote cleanup.** `git remote get-url codebox-NAME` is
   run in the operator's current directory; if it succeeds (i.e. the
   matching instance remote is still wired up from an earlier
   `codebox git push` or `pull`), codebox prints `Removing local git
   remote "codebox-NAME"...` and runs `git remote remove
   codebox-NAME`. A non-git directory, or a missing remote, is treated
   as a silent no-op — `--remote` never changes this: the cleanup is
   always local.

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

## `push` and `pull` file transfer

`push` and `pull` share an implementation: each builds an rsync
command tunnelled over ssh and runs it locally so rsync's progress
stream reaches the operator's terminal directly. The use-case layer
performs, in order:

1. **Existence check.** `<engine> ps -a --format '{{.Names}}'` is run
   against `--remote` (locally if unset). When the instance is missing,
   the command fails with `instance "NAME" not found` and exits
   non-zero before any further work.
2. **Host port lookup.** `<engine> port NAME 2222` is run on the same
   target; the first `<addr>:<port>` line is parsed and the numeric
   port retained. A stopped container produces no mapping and surfaces
   `instance "NAME" is not exposing port 2222; is it running?`.
3. **Rsync command echo.** The rsync invocation is printed to stdout
   bracketed by horizontal rules, mirroring the Dockerfile block
   `create` emits during provisioning, so the operator can audit the
   exact command before it runs.
4. **Rsync execution.** The command is executed **locally** (never via
   the orchestrator-bound ssh) so rsync's `--progress` output streams
   straight through to the operator's terminal. The shape is:

   ```
   rsync --verbose --archive --compress --update --progress \
     -e 'ssh -o StrictHostKeyChecking=no [-i KEY] [-J Remote] -p PORT' \
     SRC DST
   ```

   - `--instance-key` is `~`-expanded and passed as `-i` on the
     **inner** ssh only — the orchestrator-bound ssh that ran steps 1
     and 2 used the operator's normal ssh configuration.
   - `--remote=user@host` becomes `-J user@host` so the operator's
     bastion is interpreted on the local side and rsync connects to
     the container's published port through the jump.
   - `SRC` and `DST` are oriented per command: `push` sends
     `LOCAL → user@localhost:INSTANCE_PATH`, `pull` sends
     `user@localhost:INSTANCE_PATH → LOCAL`. The local path is
     `~`-expanded before being passed to rsync.

Both `--local-path` and `--instance-path` are required; omitting
either fails fast with a flag-name error before any orchestrator
command is issued.

## `git push` and `git pull` flow

`git push` and `git pull` share the orchestrator-level preflight
(existence + host port lookup) and the local-side remote bookkeeping.
See [`git.md`](git.md) for the user-facing walkthrough.

For each invocation the use-case layer performs, in order:

0. **Local git pre-check (CLI layer).** Before any orchestrator work,
   the CLI confirms the operator's current working directory is the
   root of a git repository — that is, it contains a `.git/`
   directory. Subdirectories of a repo, and directories that are not
   a repo at all, fail fast with
   `not a git repository: no .git directory in <cwd>`. The same
   check applies to both `git push` and `git pull` so the operator
   never reaches the orchestrator with a half-set-up local repo.
1. **Existence check.** `<engine> ps -a --format '{{.Names}}'` is run
   against `--remote` (locally if unset). When the instance is missing,
   the command fails with `instance "NAME" not found` and exits
   non-zero before any further work.
2. **Host port lookup.** `<engine> port NAME 2222` is run on the same
   target; the first `<addr>:<port>` line is parsed and the numeric
   port retained. A stopped container produces no mapping and surfaces
   `instance "NAME" is not exposing port 2222; is it running?`.
3. **Local remote refresh.** A git remote named `codebox-INSTANCE` is
   (re)pointed at `ssh://user@localhost:PORT/home/user/source` so a
   restarted container with a newly-assigned host port does not strand
   the operator with a stale URL. The `codebox-` prefix keeps codebox's
   auto-managed remotes from colliding with anything the operator
   configured by hand (e.g. `origin`). The path component is always
   `/home/user/source` — codebox uses one in-container directory per
   sandbox. The shell idiom
   `git remote set-url codebox-INSTANCE URL 2>/dev/null || git remote add codebox-INSTANCE URL`
   does both cases in one runner call.

### `git push` only

After the shared preflight, `git push` additionally:

1. **Parse the refspec.** The argument is split as
   `source_remote/source_branch:target_branch`. The slash is only the
   first one — a source branch like `feature/x` is fine.
2. **Read operator identity.** `git config --get user.name` and
   `git config --get user.email` are run locally. Unset values become
   empty strings — the init step below simply skips them.
3. **Initialise the instance source dir.** An ssh hop into the
   instance runs an idempotent script:

   ```sh
   if [ ! -d ~/source/.git ]; then
     mkdir -p ~/source && git init -q ~/source && cd ~/source &&
     git config receive.denyCurrentBranch updateInstead [&&
     git config user.name 'NAME'] [&&
     git config user.email 'EMAIL']
   fi
   ```

   The `updateInstead` setting lets subsequent pushes update the
   instance's working tree atomically when it is clean (and refuse
   when it is dirty). Operator identity is written **only at init
   time**; it is not refreshed on later pushes.
4. **Local fetch.** `git fetch source_remote` is run locally so the
   remote-tracking ref `source_remote/source_branch` reflects the
   upstream tip before it is pushed onward.
5. **Push.**
   `GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no [-i KEY] [-J Remote]'
   git push codebox-INSTANCE source_remote/source_branch:refs/heads/target_branch`
   is run locally. The remote URL stored in `.git/config` does not
   encode `-i` / `-J`; those options live on `GIT_SSH_COMMAND` so they
   apply only when codebox invokes git.
6. **Checkout.** A second ssh hop runs
   `cd /home/user/source && git checkout target_branch` so the
   instance has the freshly pushed branch checked out at
   `~/source`. The branch was just created by step 5, so a plain
   `git checkout` is enough — no `-b` (which would refuse with
   "branch already exists") and no `-B` (which would clobber a
   manually advanced branch on the instance side).
7. **Success line.** A one-line message names the branch and `~/source`.

### `git pull` only

After the shared preflight, `git pull` runs
`GIT_SSH_COMMAND=... git fetch codebox-INSTANCE BRANCH` locally,
populating `refs/remotes/codebox-INSTANCE/BRANCH` in the operator's
repository. A two-line hint then prints the exact local checkout
command:

```
Fetched "BRANCH" from instance "INSTANCE".
To check it out locally:
  git checkout codebox-INSTANCE/BRANCH -b BRANCH
```

### Transport details

- The orchestrator-bound ssh used for steps 1 and 2 honours the
  operator's normal ssh configuration; `--instance-key` is **never**
  passed to it.
- The container-bound ssh (init / checkout / push / fetch transport)
  threads `-i KEY` (when `--instance-key` is set) and `-J Remote`
  (when `--remote` is set).
- The `git push` / `git fetch` invocations are echoed to stdout
  bracketed by horizontal rules, mirroring the Dockerfile and rsync
  blocks emitted by `create` and `push`/`pull`.

## Status

`create`, `delete`, `list`, `shell`, `exec`, `pull`, `push`, and
`git push` / `git pull` are all implemented end-to-end. The
behaviours described above are the **specification** the
implementation is held against — if the two disagree, this file is
canonical and the code should be updated.
