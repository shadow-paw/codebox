package app_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"codebox/internal/app"
)

type stubResolver struct {
	key string
	err error
	got string
}

func (s *stubResolver) Resolve(keyPath string) (string, error) {
	s.got = keyPath
	return s.key, s.err
}

func TestCreate_RendersDockerfile(t *testing.T) {
	t.Parallel()
	keys := &stubResolver{key: "ssh-ed25519 AAAA test"}
	a := app.NewWith(keys)

	var buf bytes.Buffer
	err := a.Create(context.Background(), &buf, app.CreateRequest{
		Instance:     "demo",
		Orchestrator: "podman",
		OS:           "debian_13",
		InstanceKey:  "/home/op/.ssh/id_rsa",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if keys.got != "/home/op/.ssh/id_rsa" {
		t.Errorf("resolver got %q, want %q", keys.got, "/home/op/.ssh/id_rsa")
	}
	out := buf.String()
	for _, want := range []string{"FROM docker.io/debian:13.4", "ssh-ed25519 AAAA test", "EXPOSE 2222"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestCreate_RejectsUnknownOrchestrator(t *testing.T) {
	t.Parallel()
	a := app.NewWith(&stubResolver{key: "k"})
	var buf bytes.Buffer
	err := a.Create(context.Background(), &buf, app.CreateRequest{
		Orchestrator: "containerd",
		OS:           "debian_13",
	})
	if err == nil {
		t.Fatal("expected error for unknown orchestrator")
	}
	if !strings.Contains(err.Error(), "unsupported orchestrator") {
		t.Errorf("error %q should mention unsupported orchestrator", err.Error())
	}
	if buf.Len() != 0 {
		t.Errorf("nothing should be written on validation failure; got %q", buf.String())
	}
}

func TestCreate_PropagatesResolverError(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	a := app.NewWith(&stubResolver{err: want})
	var buf bytes.Buffer
	err := a.Create(context.Background(), &buf, app.CreateRequest{
		Orchestrator: "podman",
		OS:           "debian_13",
	})
	if !errors.Is(err, want) {
		t.Fatalf("Create err = %v, want wrap of %v", err, want)
	}
}
