package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeNames are the compose file names probed, in order, when the
// project config carries no explicit port-forward list. The first one
// present in the working directory wins. The Compose-spec names
// (compose.yaml/.yml) take precedence over the legacy docker-compose
// names, and the podman-compose names are probed last.
var composeNames = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
	"podman-compose.yaml",
	"podman-compose.yml",
}

// ResolvePortForwards returns the normalized "LOCAL:REMOTE" forward
// specs for `codebox port-forward`. When the project config carries a
// port-forward: list those entries are used (each normalized so a bare
// "PORT" becomes "PORT:PORT"). Otherwise, when a compose file is
// present in workDir, its published ports are auto-detected and each is
// mapped to itself ("PORT:PORT") so localhost:PORT reaches the port the
// container publishes inside the instance. Returns an empty slice when
// neither source yields a port; the caller decides whether that is an
// error.
func ResolvePortForwards(project Config, workDir string) ([]string, error) {
	if len(project.PortForward) > 0 {
		return normalizePortSpecs(project.PortForward)
	}
	if workDir == "" {
		return nil, nil
	}
	return detectComposePorts(workDir)
}

// normalizePortSpecs canonicalizes config-supplied entries into
// "LOCAL:REMOTE" form, expanding a bare "PORT" to "PORT:PORT" and
// rejecting anything that is not a port number (or pair). Duplicates
// are dropped while preserving first-seen order.
func normalizePortSpecs(specs []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, s := range specs {
		local, remote, err := splitPortSpec(s)
		if err != nil {
			return nil, err
		}
		spec := local + ":" + remote
		if seen[spec] {
			continue
		}
		seen[spec] = true
		out = append(out, spec)
	}
	return out, nil
}

// splitPortSpec parses a single config entry. "LOCAL:REMOTE" yields its
// two halves; a bare "PORT" yields ("PORT", "PORT"). Both halves must be
// valid port numbers.
func splitPortSpec(s string) (local, remote string, err error) {
	s = strings.TrimSpace(s)
	l, r, ok := strings.Cut(s, ":")
	if !ok {
		if !isPort(s) {
			return "", "", fmt.Errorf("invalid port-forward %q: expected a port number or LOCAL:REMOTE", s)
		}
		return s, s, nil
	}
	l, r = strings.TrimSpace(l), strings.TrimSpace(r)
	if !isPort(l) || !isPort(r) {
		return "", "", fmt.Errorf("invalid port-forward %q: expected LOCAL:REMOTE port numbers", s)
	}
	return l, r, nil
}

// detectComposePorts reads the first compose file present in workDir
// and returns its published ports mapped to themselves. A missing file
// is not an error (returns an empty slice); a present but unparseable
// file is.
func detectComposePorts(workDir string) ([]string, error) {
	for _, name := range composeNames {
		data, err := os.ReadFile(filepath.Join(workDir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		ports, err := composePorts(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return ports, nil
	}
	return nil, nil
}

// composeFile and composeService model just enough of the compose
// schema to reach each service's published ports.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Ports []composePort `yaml:"ports"`
}

// composePort captures the published (host-side) port numbers of one
// compose `ports:` entry, expanded from any range. A port mapping has
// no published port (e.g. it relies on a random host port) when the
// slice is empty.
type composePort struct {
	published []string
}

// UnmarshalYAML handles the two compose port forms: the short string
// form ("8080:80", "127.0.0.1:8080:80", "3000", "3000-3005:3000-3005",
// optionally with a "/tcp" suffix) and the long mapping form
// (target/published keys). The published (host) port is extracted in
// both cases; ranges expand to one entry per port.
func (p *composePort) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		p.published = publishedPorts(value.Value)
	case yaml.MappingNode:
		var m struct {
			Published string `yaml:"published"`
			Target    string `yaml:"target"`
		}
		if err := value.Decode(&m); err != nil {
			return err
		}
		src := m.Published
		if src == "" {
			src = m.Target
		}
		p.published = expandPortRange(stripProto(src))
	}
	return nil
}

// composePorts parses a compose document and returns its published
// ports as "PORT:PORT" forwards, deduplicated and ordered by service
// name (then by listing order) for deterministic output.
func composePorts(data []byte) ([]string, error) {
	var f composeFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(f.Services))
	for name := range f.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []string
	seen := map[string]bool{}
	for _, name := range names {
		for _, port := range f.Services[name].Ports {
			for _, pub := range port.published {
				if pub == "" || seen[pub] {
					continue
				}
				seen[pub] = true
				out = append(out, pub+":"+pub)
			}
		}
	}
	return out, nil
}

// publishedPorts extracts the host-published port(s) from a short-form
// compose port string. The host port is the second-to-last
// colon-separated field ("ip:host:container" or "host:container") or
// the lone field ("container") — short form without an explicit host
// port still binds that port number on the host.
func publishedPorts(s string) []string {
	parts := strings.Split(stripProto(strings.TrimSpace(s)), ":")
	var host string
	switch len(parts) {
	case 1:
		host = parts[0]
	case 2:
		host = parts[0]
	default:
		host = parts[len(parts)-2]
	}
	return expandPortRange(host)
}

// stripProto drops a trailing "/tcp" or "/udp" protocol suffix.
func stripProto(s string) string {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i]
	}
	return s
}

// expandPortRange returns the port(s) named by s: a single "PORT" yields
// one entry, a "LO-HI" range yields one entry per port. Non-numeric,
// reversed, or out-of-range inputs yield nothing, and a range wider than
// 1024 ports is rejected as a likely mistake rather than expanded.
func expandPortRange(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	lo, hi, ok := strings.Cut(s, "-")
	if !ok {
		if isPort(s) {
			return []string{s}
		}
		return nil
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(lo))
	end, err2 := strconv.Atoi(strings.TrimSpace(hi))
	if err1 != nil || err2 != nil || start < 1 || end > 65535 || end < start || end-start > 1024 {
		return nil
	}
	out := make([]string, 0, end-start+1)
	for port := start; port <= end; port++ {
		out = append(out, strconv.Itoa(port))
	}
	return out
}

// isPort reports whether s is a decimal port number in [1, 65535].
func isPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535
}
