package settings

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// ResolveAdditionalRun returns the custom build commands for the image,
// drawn from the builder.additional-run lists. Both configs contribute:
// the project steps run first, then the global steps, so a project's
// own tooling lands before the org-wide ~/.codebox.conf steps that
// follow it. Blank entries (empty or whitespace-only) are dropped so a
// stray list item cannot emit an empty RUN. Steps are not deduplicated —
// a command that legitimately appears twice runs twice.
func ResolveAdditionalRun(global, project Config) []string {
	var out []string
	for _, cmd := range append(append([]string{}, project.Builder.AdditionalRun...), global.Builder.AdditionalRun...) {
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		out = append(out, cmd)
	}
	return out
}

// CopyStep is one builder.additional-copy entry. From is a file,
// directory, or glob pattern on the operator's machine (a leading "~"
// is the operator's home); To is the destination directory or pathname
// inside the instance. Exclude lists rsync exclude patterns (e.g.
// "*.log", "node_modules/") for files and directories to skip; it may be
// written as a single pattern or a list.
type CopyStep struct {
	From    string     `yaml:"from"`
	To      string     `yaml:"to"`
	Exclude StringList `yaml:"exclude"`
}

// ResolveAdditionalCopy returns the copy steps drawn from the
// builder.additional-copy lists, merged project-first then global — the
// same order as ResolveAdditionalRun. Entries whose from and to are both
// blank are dropped; a half-filled entry is kept so the create flow can
// reject it with a clear error instead of silently ignoring it. Values
// are trimmed of surrounding whitespace and blank exclude patterns are
// dropped.
func ResolveAdditionalCopy(global, project Config) []CopyStep {
	var out []CopyStep
	steps := append(append([]CopyStep{}, project.Builder.AdditionalCopy...), global.Builder.AdditionalCopy...)
	for _, s := range steps {
		s.From = strings.TrimSpace(s.From)
		s.To = strings.TrimSpace(s.To)
		s.Exclude = trimNonBlank(s.Exclude)
		if s.From == "" && s.To == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// trimNonBlank returns the entries of in trimmed of surrounding
// whitespace, dropping any that are blank.
func trimNonBlank(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// StringList is a []string that also accepts a single YAML scalar, so a
// config key can be written as `key: value` or as a list of values.
type StringList []string

// UnmarshalYAML decodes either a scalar (one entry) or a sequence.
func (l *StringList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*l = StringList{node.Value}
		return nil
	}
	var items []string
	if err := node.Decode(&items); err != nil {
		return err
	}
	*l = items
	return nil
}
