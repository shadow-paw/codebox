# AGENTS.md

Contribution contract for AI coding agents working on **codebox** — a Go CLI for managing sandboxes for coding agents.

## Project overview

- **Language / runtime:** Go **1.26.x**. `go.mod` declares `go 1.26`; pair it with a matching `toolchain` directive when the minor version is bumped.
- **Type:** Single-binary command-line application.
- **Goal:** Keep the codebase small, idiomatic, and easy to reason about. Default to the standard library.

## Best practices

Write idiomatic Go. Follow Effective Go and the Go Code Review Comments.

- **Errors:** return errors, do not log-and-return. Wrap with `fmt.Errorf("...: %w", err)` so callers can `errors.Is` / `errors.As`. Never `panic` for an expected failure.
- **Context:** every function that performs IO, blocks, or could be cancelled takes `ctx context.Context` as its first parameter. Propagate it; never drop it on the floor.
- **Concurrency:** prefer simple, blocking code. Reach for goroutines only when there is a real concurrency requirement, and always propagate cancellation via `ctx`.
- **Public API:** export the minimum surface callers need. Prefer unexported types; return concrete types unless multiple implementations actually exist.
- **No globals.** No package-level mutable state. Pass dependencies explicitly via constructors or function arguments.
- **No `init()`** for anything beyond local registration with a package whose API requires it.
- **Naming:** short, lowercase package names; no stutter (`app.Run`, not `app.AppRun`).
- **No premature abstraction.** Wait until there are two concrete callers before introducing an interface or a generic helper.

## CLI help text — keep every path identical

The five user-facing help entrypoints must all render the same body
for the root command, and the same body for each subcommand:

- `codebox`
- `codebox help`
- `codebox --help`
- `codebox <cmd> --help`
- `codebox help <cmd>`

This means:

- Help is dispatched through cobra (`root.ExecuteContext`), never via
  a side-channel `root.Help()` call that skips cobra's
  default-subcommand registration — bypassing that path drops
  `completion` and `help` from the command list.
- The banner is suppressed only for commands whose stdout is consumed
  by another program (`exec`, `completion`, `__complete`,
  `__completeNoDesc`). The suppression is cancelled when `--help` /
  `-h` is anywhere on the command line, so the banner stays on every
  help path regardless of the subcommand.
- When you add a subcommand, the test `TestRun_SubcommandHelpFormsAreIdentical`
  in `internal/cli/cli_test.go` automatically pins the two-form
  equivalence — keep new commands in that test's command list.

## Persistent root flags

`--orchestrator`, `--remote` and `--instance-key` are declared **once**
as `PersistentFlags()` on the root command (`internal/cli/root.go`)
and inherited by every subcommand. As a result:

- Operators can place them in any position before `--`, including
  before the subcommand name:
  `codebox --remote=ops@bastion shell demo` and
  `codebox shell demo --remote=ops@bastion` are equivalent. For
  `exec`, anything after the `--` separator is forwarded to the
  inner command and is not parsed by codebox.
- Subcommands read the values with `readCommonOpts(cmd)` (defined in
  `root.go`); they do **not** re-declare these flags. Re-declaration
  would shadow the persistent definition and break the
  before-the-command placement.
- In subcommand help these flags appear under `Global Flags:`
  (alphabetised by cobra), not in the local `Flags:` block. The
  layout is pinned by `TestPullPush_HelpListsFlags`,
  `TestGitPushPull_HelpListsFlags` and
  `TestCreate_FlagOrderMatchesSpec`. `TestRootFlags_AcceptedBeforeOrAfterCommand`
  pins the before/after placement contract.

## Shell completion

When wiring shell-completion candidates, set
`ValidArgsFunction: completeInstances` on every command whose first
positional is `INSTANCE`. The helper lives in
`internal/cli/completion.go` and queries `app.ListInstanceNames`,
honouring `--orchestrator` and `--remote` from the partial command
line — including a `--remote` placed before the subcommand, because
cobra inherits persistent root flags. `--instance-key` is read off
the command line but not used by the lookup (the listing path uses
the operator's normal ssh config to reach the orchestrator host,
never the per-instance key).

`TestComplete_WiredOnInstanceCommands` pins the wiring for every
INSTANCE-positional subcommand, and
`TestComplete_HonoursRemoteBeforeCommand` pins that completion
honours `--remote` regardless of where the operator placed it.

### Flag value completion

Fixed-enum flag values are wired with cobra's
`RegisterFlagCompletionFunc`. The helper
`staticCompletion(values)` (in `internal/cli/completion.go`) returns
the supplied list with `ShellCompDirectiveNoFileComp` so the shell
does not fall back to path completion on an unrecognised prefix.

Wired flags today:

- `--orchestrator` — registered on the root persistent flag.
- `--os`, `--python`, `--node`, `--golang`, `--dotnet` — registered
  on `create`.

The candidate lists come through `internal/app` (the
`app.Supported*` accessors re-export the domain-layer enums). Do
**not** import `internal/image` or `internal/container` from
`internal/cli`; the layering rule below makes that a build break.
When a new enum is added to the domain layer, expose it through
`internal/app/catalog.go` and wire the completion alongside the
flag definition. `TestComplete_FlagValueCandidates` pins the wired
candidate sets.

## Modular architecture — decouple presentation from business logic

Maintain a strict layering. Presentation concerns must not leak into business logic, and business logic must not import CLI/IO types.

Target layout:

```
cmd/codebox/        # main entrypoint — wiring only, zero logic
internal/cli/       # presentation: flag parsing, command tree, prompts, output formatting
internal/app/       # application/use-case layer — orchestrates domain operations
internal/<domain>/  # domain packages (pure business logic, no IO)
internal/adapters/  # IO adapters: filesystem, exec, network, etc. behind interfaces
internal/platform/  # cross-cutting helpers (logging, config, errors)
```

Rules:

1. `cmd/` may import `internal/cli` and `internal/app` only.
2. `internal/cli` may depend on `internal/app`. It **must not** import domain or adapter packages directly.
3. `internal/app` and domain packages **must not** import `internal/cli`, reference `os.Stdout` / `os.Stderr`, or call `fmt.Print*`. They take `io.Writer`, `context.Context`, and interfaces as inputs.
4. Side effects (filesystem, process exec, network, time, env, args) live behind interfaces in `internal/adapters/` and are injected into the app layer.
5. Prefer many small, focused packages over one large `internal/core`. A package should have a single reason to change.

## Testability

Write code that is easy to test before reaching for elaborate test infrastructure.

- Depend on interfaces, not concrete adapters, at module boundaries.
- Keep functions small; prefer pure functions where practical.
- Inject anything that varies with the environment: `time.Now`, `os.Getenv`, `os.Args`, filesystem roots, random sources. Do not call them transitively from business logic.
- Use `t.TempDir()` and `testing/fstest` instead of touching the real filesystem.

Tests:

- Write **unit tests** for every non-trivial function. Place them next to the code as `*_test.go` in the same package; use the `_test` package suffix for black-box tests of the public API.
- Use **table-driven tests** for multiple cases.
- Write **doctests** (`Example*` functions with `// Output:` comments) for any exported function whose usage benefits from a short, runnable example — `go test` verifies the output.
- Aim for meaningful coverage of business logic. Do not chase 100% on glue code.
- Run with the race detector for anything concurrent: `go test -race ./...`.

## Workflow — after **every** code change

Run, in order:

```sh
make format   # gofmt/goimports/golines at 120 cols — must produce no diff
make lint     # static analysis (golangci-lint)
make test     # full test suite
```

Do not commit, open a PR, or report the task complete until all three pass cleanly. If `make format` rewrites files, re-run `make lint` and `make test`.

## Package management

Treat every new dependency as a supply-chain risk. Default to the standard library; inline a few lines rather than pull in a tree.

Before adding a package:

1. **Reputation check.** Prefer well-known, actively maintained modules (e.g. `github.com/spf13/cobra`, `github.com/stretchr/testify`, `golang.org/x/...`). Avoid single-author packages, packages with very low star counts, or packages whose import path you cannot verify.
2. **Recency check.** Use the **latest available version at install time**, but **never adopt a version published within the last 2 weeks**. Pick the most recent version whose release date is ≥ 14 days ago. This window mitigates the risk of a malicious release being caught and yanked before you depend on it.
3. **Pin the exact version.** Add it with `go get example.com/mod@vX.Y.Z` — never `@latest`, never `@master`, never a branch name. Confirm the pinned version is reflected in both `go.mod` and `go.sum`.
4. **Audit.** Run `make audit` immediately (`govulncheck` + `go mod verify`). If anything fails, fix it or back the dependency out.
5. **Justify in the PR description.** State which package, which version, the publish date, and why a stdlib solution was insufficient.

Removing a dependency is always cheaper than keeping one that turns out to be a problem.

## Git hygiene

- **Do not inspect other branches or the stash.** Work only with the current working tree. No `git checkout <other-branch>`, no `git stash show`, no `git log <other-branch>`, no cherry-picking from elsewhere. If context from another branch is genuinely required, ask first.
- Keep commits focused — one logical change per commit.
- Commit messages: imperative mood; explain *why* in the body when the *what* is non-obvious.
- Do not commit generated files, build artefacts, or anything matched by `.gitignore`.
- Never commit secrets, credentials, or `.env*` files.

## Make targets the agent relies on

The `Makefile` exposes:

- `make deps`   — `go mod download` + `go mod tidy`.
- `make format` — formats all Go source at 120 cols; no-op if already formatted.
- `make lint`   — runs the configured linters; non-zero exit on any finding.
- `make test`   — runs `go test -race -cover ./...`.
- `make audit`  — `govulncheck ./...` + `go mod verify`.
- `make build`  — produces `./bin/codebox` (default target).
- `make clean`  — removes build artefacts.

If a target is missing or broken, fix the `Makefile` rather than working around it.

## What NOT to do

- Do not introduce a framework or abstraction "for future flexibility" — wait until there are two concrete callers.
- Do not catch errors only to re-log and continue; propagate them.
- Do not print directly to `os.Stdout` / `os.Stderr` outside the `internal/cli` layer.
- Do not bypass `make format` / `make lint` / `make test`.
- Do not pull in a dependency you cannot justify against the stdlib.
- Do not pin to `@latest` or a version less than 14 days old.
- Do not inspect other git branches or the stash.
