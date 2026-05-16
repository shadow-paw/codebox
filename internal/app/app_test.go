package app_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"codebox/internal/app"
)

func TestRun_PrintsHelloWorld(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := app.Run(context.Background(), nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got, want := stdout.String(), "hello world\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRun_IgnoresArgs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{"nil", nil},
		{"empty", []string{}},
		{"single", []string{"foo"}},
		{"many", []string{"foo", "bar", "baz"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			if code := app.Run(context.Background(), tc.args, &stdout, &stderr); code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}
			if got, want := stdout.String(), "hello world\n"; got != want {
				t.Errorf("stdout = %q, want %q", got, want)
			}
		})
	}
}

// ExampleRun demonstrates invoking the CLI programmatically.
// It also serves as a doctest: the // Output: comment is verified by `go test`.
func ExampleRun() {
	app.Run(context.Background(), nil, os.Stdout, os.Stderr)
	// Output: hello world
}
