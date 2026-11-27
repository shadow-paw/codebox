package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebox/internal/app"
	"codebox/internal/settings"
)

type createOpts struct {
	commonOpts
	instance            string
	rebuild             bool
	httpsProxy          string
	osImage             string
	python              string
	node                string
	golang              string
	dotnet              string
	claude              bool
	claudeCredentials   bool
	codex               bool
	codexCredentials    bool
	opencode            bool
	opencodeCredentials bool
	podman              bool
	psql                bool
	tmux                bool
}

const createHelpFooter = `
Orchestrators:
  podman, docker

OS:
  debian_12   Debian 12
  debian_13   Debian 13
  ubuntu_24   Ubuntu 24 LTS
  ubuntu_26   Ubuntu 26 LTS
  redhat_10   Red Hat 10

Languages:
  --python   3.12, 3.13, 3.14, 3.15
  --node     24, 25, 26
  --golang   1.26, 1.27 (or exact 1.X.N)
  --dotnet   8, 10

Agents:
  --claude                 Claude Code (also writes /home/user/.claude.json onboarding flag)
  --claude-credentials     Copy ~/.claude/.credentials.json into the instance (ignored unless --claude)
  --codex                  OpenAI Codex CLI
  --codex-credentials      Copy ~/.codex/auth.json into the instance (ignored unless --codex)
  --opencode               opencode
  --opencode-credentials   Copy ~/.local/share/opencode/auth.json into the instance (ignored unless --opencode)

  Agent flags are on/off toggles: --claude (or --claude=true) enables,
  --claude=false disables (likewise --codex / --opencode). Pass =true|false
  to override a default set in .codebox.conf.

Tools:
  --podman   Rootless Podman (inside the instance)
  --psql     psql PostgreSQL client
  --tmux     tmux terminal multiplexer (codebox shell launches it on connect)

Network:
  --https-proxy=URL   Export HTTPS_PROXY (and a matching HTTP_PROXY, plus
                      NO_PROXY=localhost,127.0.0.1) from the in-container user login profile
                      (also exported during agent installs so their curl pipelines route through it)

Config (.codebox.conf, project and/or ~/.codebox.conf):
  builder:
    additional-run:      # extra RUN steps appended to the image build (as root)
      - apt-get update && apt-get install -y graphviz
    additional-copy:     # copied into the instance right after it starts (like file push)
      - from: ~/.claude/skills/
        to: .claude/skills/
        exclude:         # optional: one pattern or a list
          - synced/
          - find-skills/

  additional-copy: from is a local file, directory or glob (*, ?, [...]); a leading
  ~ is your home. A trailing / on from copies the directory's contents. to is a
  directory or pathname inside the instance (relative to /home/user; missing
  parent directories are created). exclude takes rsync --exclude patterns:
  "synced/" skips that directory at any depth, "/synced/" only at the top of
  the copy, "*.log" matching files anywhere. Symlinks pointing outside the copy
  are followed, like file push (rsync --copy-unsafe-links).
  Project entries run before global ones.
`

const createHelpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}` + createHelpFooter

func newCreateCmd() *cobra.Command {
	var opts createOpts

	cmd := &cobra.Command{
		Use:   "create INSTANCE",
		Short: "Create a new sandbox instance",
		Long: "Create a new sandbox instance.\n\n" +
			"Builds an image from a Dockerfile generated on the fly (no files\n" +
			"from the working directory are sent to the orchestrator), then\n" +
			"creates and starts the container labelled codebox=true. Pass\n" +
			"--remote to provision on another host over ssh.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.commonOpts = readCommonOpts(cmd)
			opts.instance = args[0]
			return runCreate(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}

	f := cmd.Flags()
	f.SortFlags = false
	f.BoolVar(&opts.rebuild, "rebuild", false,
		"Force a rebuild of the base image even if a cached one exists")
	f.StringVar(&opts.httpsProxy, "https-proxy", "",
		"Export HTTPS_PROXY=URL (with matching HTTP_PROXY and NO_PROXY=localhost,127.0.0.1) "+
			"from the in-container user login profile (also used for agent installs)")
	f.StringVar(&opts.osImage, "os", "debian_13",
		"Base OS image (debian_12, debian_13, ubuntu_24, ubuntu_26, redhat_10)")
	f.StringVar(&opts.python, "python", "",
		"Install Python at this version (3.12, 3.13, 3.14, 3.15)")
	f.StringVar(&opts.node, "node", "",
		"Install Node.js at this major version (24, 25, 26)")
	f.StringVar(&opts.golang, "golang", "",
		"Install Go: a minor line (1.26, 1.27) for its latest patch, or an exact patch (1.27.N)")
	f.StringVar(&opts.dotnet, "dotnet", "",
		"Install .NET at this version (8, 10)")
	f.BoolVar(&opts.claude, "claude", false,
		"Install Claude Code")
	f.BoolVar(&opts.claudeCredentials, "claude-credentials", false,
		"Copy ~/.claude/.credentials.json into the instance after it starts (ignored unless --claude)")
	f.BoolVar(&opts.codex, "codex", false,
		"Install OpenAI Codex CLI")
	f.BoolVar(&opts.codexCredentials, "codex-credentials", false,
		"Copy ~/.codex/auth.json into the instance after it starts (ignored unless --codex)")
	f.BoolVar(&opts.opencode, "opencode", false,
		"Install opencode")
	f.BoolVar(&opts.opencodeCredentials, "opencode-credentials", false,
		"Copy ~/.local/share/opencode/auth.json into the instance after it starts (ignored unless --opencode)")
	f.BoolVar(&opts.podman, "podman", false,
		"Install rootless Podman inside the instance")
	f.BoolVar(&opts.psql, "psql", false,
		"Install the psql PostgreSQL client")
	f.BoolVar(&opts.tmux, "tmux", false,
		"Install tmux; codebox shell launches it on connect")

	registerCreateValueCompletions(cmd)

	cmd.SetHelpTemplate(createHelpTemplate)
	return cmd
}

// registerCreateValueCompletions wires fixed-enum value completion for
// the create flags whose accepted values are known at build time. The
// candidate sets come through internal/app (which re-exports the
// domain-layer enums) so the CLI stays decoupled from internal/image.
//
// RegisterFlagCompletionFunc only fails when the flag does not exist
// — a programmer error this file would catch on the first build — so
// the returned errors are intentionally discarded.
func registerCreateValueCompletions(cmd *cobra.Command) {
	pairs := []struct {
		flag   string
		values []string
	}{
		{"os", app.SupportedOS()},
		{"python", app.SupportedPython()},
		{"node", app.SupportedNode()},
		{"golang", app.SupportedGolang()},
		{"dotnet", app.SupportedDotnet()},
	}
	for _, p := range pairs {
		_ = cmd.RegisterFlagCompletionFunc(p.flag, staticCompletion(p.values))
	}
}

// builderConfig is the merged builder section of the .codebox.conf files,
// in the shape the app layer consumes.
type builderConfig struct {
	additionalRun  []string
	additionalCopy []app.CopyStep
}

// loadBuilderConfig loads the global and project .codebox.conf files and
// returns their merged builder.additional-run and builder.additional-copy
// lists (both project first, then global).
// It is the create/workflow counterpart to configuredPushSource: the
// builder section is structured data, not flags, so it cannot ride the
// args-injection path and is read here instead.
func loadBuilderConfig(home string) (builderConfig, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return builderConfig{}, fmt.Errorf("locate current directory: %w", err)
	}
	global, project, err := settings.Load(home, workDir)
	if err != nil {
		return builderConfig{}, err
	}
	cfg := builderConfig{additionalRun: settings.ResolveAdditionalRun(global, project)}
	for _, s := range settings.ResolveAdditionalCopy(global, project) {
		cfg.additionalCopy = append(cfg.additionalCopy, app.CopyStep{From: s.From, To: s.To, Exclude: s.Exclude})
	}
	return cfg, nil
}

func runCreate(ctx context.Context, out io.Writer, opts createOpts) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	builder, err := loadBuilderConfig(home)
	if err != nil {
		return err
	}
	return app.New(home).Create(ctx, out, app.CreateRequest{
		Instance:            opts.instance,
		Orchestrator:        opts.orchestrator,
		OS:                  opts.osImage,
		InstanceKeys:        opts.instanceKeys,
		Remote:              opts.remote,
		Rebuild:             opts.rebuild,
		HTTPSProxy:          opts.httpsProxy,
		Python:              opts.python,
		Node:                opts.node,
		Golang:              opts.golang,
		Dotnet:              opts.dotnet,
		Claude:              opts.claude,
		ClaudeCredentials:   opts.claudeCredentials,
		Codex:               opts.codex,
		CodexCredentials:    opts.codexCredentials,
		Opencode:            opts.opencode,
		OpencodeCredentials: opts.opencodeCredentials,
		Psql:                opts.psql,
		Tmux:                opts.tmux,
		Podman:              opts.podman,
		AdditionalRun:       builder.additionalRun,
		AdditionalCopy:      builder.additionalCopy,
	})
}
