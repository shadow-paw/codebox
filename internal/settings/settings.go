// Package settings loads .codebox.conf files and injects their entries
// into the CLI argument slice before Cobra parses it.
package settings

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds the parsed content of a .codebox.conf file.
type Config struct {
	Args struct {
		All    []string `yaml:"all"`
		Create []string `yaml:"create"`
	} `yaml:"args"`
}

// Load reads ~/.codebox.conf (global) and ./.codebox.conf (project).
// Missing files are silently ignored. Either homeDir or workDir may be
// empty, in which case the corresponding file is skipped.
func Load(homeDir, workDir string) (global, project Config, err error) {
	if homeDir != "" {
		if err = loadFile(filepath.Join(homeDir, ".codebox.conf"), &global); err != nil {
			return
		}
	}
	if workDir != "" {
		err = loadFile(filepath.Join(workDir, ".codebox.conf"), &project)
	}
	return
}

func loadFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, cfg)
}

// InjectArgs merges settings into cliArgs respecting precedence:
// CLI args > project settings > global settings.
//
// Entries in the "all" section become persistent root flags and are
// prepended before the subcommand. Entries in the "create" section are
// inserted immediately after the "create" token. In both cases, flags
// already present in cliArgs are never duplicated.
func InjectArgs(cliArgs []string, global, project Config) []string {
	allMerged := mergeArgLists(global.Args.All, project.Args.All)
	createMerged := mergeArgLists(global.Args.Create, project.Args.Create)

	explicit := explicitFlags(cliArgs)

	extraAll := filterArgs(allMerged, explicit)

	var extraCreate []string
	if detectSubcmd(cliArgs) == "create" {
		extraCreate = filterArgs(createMerged, explicit)
	}

	if len(extraAll) == 0 && len(extraCreate) == 0 {
		return cliArgs
	}

	return inject(cliArgs, extraAll, extraCreate)
}

// mergeArgLists returns the union of global and project arg lists.
// When both lists contain the same flag name, the project value wins.
func mergeArgLists(global, project []string) []string {
	var order []string
	m := make(map[string]string)

	for _, a := range global {
		name := argName(a)
		if _, seen := m[name]; !seen {
			order = append(order, name)
		}
		m[name] = a
	}
	for _, a := range project {
		name := argName(a)
		if _, seen := m[name]; !seen {
			order = append(order, name)
		}
		m[name] = a // project overrides global
	}

	out := make([]string, 0, len(order))
	for _, name := range order {
		out = append(out, m[name])
	}
	return out
}

// filterArgs returns entries from args whose flag name is not in explicit,
// converted to --flag or --flag=value form.
func filterArgs(args []string, explicit map[string]bool) []string {
	var out []string
	for _, a := range args {
		if !explicit[argName(a)] {
			out = append(out, "--"+a)
		}
	}
	return out
}

// inject prepends extraAll before cliArgs and inserts extraCreate
// immediately after the "create" token in cliArgs.
func inject(cliArgs, extraAll, extraCreate []string) []string {
	result := make([]string, 0, len(cliArgs)+len(extraAll)+len(extraCreate))
	result = append(result, extraAll...)

	if len(extraCreate) == 0 {
		return append(result, cliArgs...)
	}

	createIdx := -1
	for i, a := range cliArgs {
		if a == "create" {
			createIdx = i
			break
		}
	}
	if createIdx < 0 {
		return append(result, cliArgs...)
	}

	result = append(result, cliArgs[:createIdx+1]...)
	result = append(result, extraCreate...)
	result = append(result, cliArgs[createIdx+1:]...)
	return result
}

// explicitFlags returns the set of flag names present in args.
// Only long flags (--name or --name=value) are recognised.
func explicitFlags(args []string) map[string]bool {
	m := make(map[string]bool)
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			continue
		}
		name := a[2:]
		if idx := strings.Index(name, "="); idx >= 0 {
			name = name[:idx]
		}
		m[name] = true
	}
	return m
}

// detectSubcmd returns the first non-flag token in args (the subcommand
// name). It skips root-level persistent flags and, when those flags appear
// as separate tokens (--flag value), their value tokens as well.
func detectSubcmd(args []string) string {
	// Root persistent flags that take a separate value token.
	valueFlags := map[string]bool{
		"--orchestrator": true,
		"--remote":       true,
		"--instance-key": true,
	}
	skipNext := false
	for _, a := range args {
		if a == "--" {
			break
		}
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "--") {
			if !strings.Contains(a, "=") && valueFlags[a] {
				skipNext = true
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

// argName extracts the flag name from a settings entry ("key=value" → "key",
// "key" → "key").
func argName(s string) string {
	if idx := strings.Index(s, "="); idx >= 0 {
		return s[:idx]
	}
	return s
}
