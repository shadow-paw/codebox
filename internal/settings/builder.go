package settings

import "strings"

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
