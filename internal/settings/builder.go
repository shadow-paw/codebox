package settings

import "strings"

// ResolveAdditionalRun returns the custom build commands for the image,
// drawn from the builder.additional-run lists. Unlike port-forward and
// git.push — which are project-only — both configs contribute here: the
// global steps run first, then the project steps, so an org-wide
// ~/.codebox.conf can seed common tooling while a project adds its own
// on top. Blank entries (empty or whitespace-only) are dropped so a
// stray list item cannot emit an empty RUN.
func ResolveAdditionalRun(global, project Config) []string {
	var out []string
	for _, cmd := range append(append([]string{}, global.Builder.AdditionalRun...), project.Builder.AdditionalRun...) {
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		out = append(out, cmd)
	}
	return out
}
