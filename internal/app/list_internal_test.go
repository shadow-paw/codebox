package app

import (
	"testing"
	"time"
)

func TestFormatAge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		label string
		ago   time.Duration
		want  string
	}{
		{"under a minute", 30 * time.Second, "<1 min"},
		{"five minutes", 5 * time.Minute, "5 min"},
		{"fifty-nine minutes", 59 * time.Minute, "59 min"},
		{"one hour", time.Hour, "1 hr"},
		{"three hours", 3 * time.Hour, "3 hr"},
		{"twenty-three hours", 23 * time.Hour, "23 hr"},
		{"one day", 24 * time.Hour, "1 day"},
		{"five days", 5 * 24 * time.Hour, "5 day"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			got := formatAge(now, now.Add(-tc.ago))
			if got != tc.want {
				t.Errorf("formatAge(now-%s) = %q, want %q", tc.ago, got, tc.want)
			}
		})
	}
}

func TestFormatAge_ZeroAndFuture(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	if got := formatAge(now, time.Time{}); got != "?" {
		t.Errorf("zero time should render as ?, got %q", got)
	}
	if got := formatAge(now, now.Add(time.Hour)); got != "?" {
		t.Errorf("future time should render as ?, got %q", got)
	}
}

func TestParseHostPort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"0.0.0.0:33000->2222/tcp", "33000"},
		{"[::]:33000->2222/tcp", "33000"},
		{"0.0.0.0:33000->2222/tcp, [::]:33000->2222/tcp", "33000"},
		// Mapping that does not target the codebox sshd port.
		{"0.0.0.0:5432->5432/tcp", ""},
		{"", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := parseHostPort(tc.in); got != tc.want {
				t.Errorf("parseHostPort(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseCreatedAt(t *testing.T) {
	t.Parallel()
	// Both engines emit time.Time.String() — with or without fractional
	// seconds. Both must parse to the same instant.
	want := time.Date(2026, 4, 1, 9, 30, 0, 0, time.UTC)
	for _, in := range []string{
		"2026-04-01 09:30:00 +0000 UTC",
		"2026-04-01 09:30:00.000000000 +0000 UTC",
	} {
		got := parseCreatedAt(in)
		if !got.Equal(want) {
			t.Errorf("parseCreatedAt(%q) = %v, want %v", in, got, want)
		}
	}
	if got := parseCreatedAt("not a timestamp"); !got.IsZero() {
		t.Errorf("invalid timestamp should yield zero time, got %v", got)
	}
}
