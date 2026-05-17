package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"codebox/internal/container"
)

// instanceUser is the unprivileged login codebox creates inside every
// image. instancePort is the host-side container port codebox's sshd
// listens on (see image.Generate's `EXPOSE 2222`); the host port the
// engine publishes for it varies per instance and is parsed from the
// engine's `{{.Ports}}` field.
const (
	instanceUser = "user"
	instancePort = "2222"
)

// ListRequest is the use-case input for App.List. Fields mirror the
// `codebox list` flags.
type ListRequest struct {
	Orchestrator string
	Remote       string
}

// List prints a table of codebox-managed containers on the target host
// (local or via ssh). Each row shows the instance name, a coarse age
// ("5 min" / "3 hr" / "2 day") computed from `{{.CreatedAt}}`, and the
// ssh command an operator can paste to open a shell. When the host has
// no codebox containers, a single human-readable message is printed.
func (a *App) List(ctx context.Context, w io.Writer, req ListRequest) error {
	eng, err := container.New(req.Orchestrator)
	if err != nil {
		return err
	}
	rnr := a.runners(req.Remote)

	var out, errBuf bytes.Buffer
	if err := rnr.Run(ctx, eng.ListCodeboxInstances(), nil, &out, &errBuf); err != nil {
		return wrapRunErr("list instances", err, &errBuf)
	}

	rows := parseInstanceRows(out.String())
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(w, "No codebox instances found.")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "INSTANCE\tAGE\tSSH COMMAND")
	now := time.Now()
	for _, row := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
			row.name,
			formatAge(now, row.createdAt),
			sshCommand(req.Remote, row.hostPort),
		)
	}
	return tw.Flush()
}

// instanceRow is one parsed line of the engine's list output.
type instanceRow struct {
	name      string
	createdAt time.Time
	hostPort  string
}

// parseInstanceRows splits the `<name>|<createdAt>|<ports>` lines
// emitted by Engine.ListCodeboxInstances. Malformed lines are skipped.
func parseInstanceRows(out string) []instanceRow {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil
	}
	var rows []instanceRow
	for _, line := range strings.Split(trimmed, "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		rows = append(rows, instanceRow{
			name:      name,
			createdAt: parseCreatedAt(parts[1]),
			hostPort:  parseHostPort(parts[2]),
		})
	}
	return rows
}

// createdAtLayout matches `time.Time.String()`, which is what both
// podman and docker emit for `{{.CreatedAt}}`. The `.999999999`
// pattern makes the fractional-seconds component optional so both
// truncated and full timestamps parse.
const createdAtLayout = "2006-01-02 15:04:05.999999999 -0700 MST"

// parseCreatedAt returns the zero time when the engine output cannot
// be parsed; formatAge renders the zero time as "?".
func parseCreatedAt(s string) time.Time {
	t, err := time.Parse(createdAtLayout, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

// hostPortRe captures the host-side port from a `<addr>:<port>->2222/tcp`
// mapping in `{{.Ports}}` output. The leading address is tolerated in
// IPv4 (`0.0.0.0:`), IPv6 (`[::]:`), or interface-name form.
var hostPortRe = regexp.MustCompile(`(\d+)->` + instancePort + `/tcp`)

// parseHostPort returns the first host port published for the codebox
// sshd port, or an empty string when the container is stopped (no
// mapping is reported in that case).
func parseHostPort(s string) string {
	m := hostPortRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

// formatAge renders a duration with the largest unit that yields a
// non-zero value: "min" under an hour, "hr" under a day, "day"
// otherwise. The zero time or a future timestamp renders as "?".
func formatAge(now, created time.Time) string {
	if created.IsZero() || created.After(now) {
		return "?"
	}
	d := now.Sub(created)
	switch {
	case d < time.Minute:
		return "<1 min"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr", int(d/time.Hour))
	default:
		return fmt.Sprintf("%d day", int(d/(24*time.Hour)))
	}
}

// sshCommand renders the copy-paste shell hint shown in the SSH
// COMMAND column. A remote host adds a `-J` jump so `localhost`
// resolves against the orchestrator host, not the operator's machine.
// A stopped container has no published port and surfaces a placeholder
// in place of a hint that would never work.
func sshCommand(remote, port string) string {
	if port == "" {
		return "(stopped)"
	}
	if remote == "" {
		return fmt.Sprintf("ssh -o StrictHostKeyChecking=no %s@localhost -p %s",
			instanceUser, port)
	}
	return fmt.Sprintf("ssh -o StrictHostKeyChecking=no -J %s %s@localhost -p %s",
		remote, instanceUser, port)
}
