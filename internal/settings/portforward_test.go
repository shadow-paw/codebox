package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolvePortForwards_ConfigList(t *testing.T) {
	t.Parallel()
	cfg := Config{PortForward: []string{"13000:3000", "13001:3001", "8080"}}
	got, err := ResolvePortForwards(cfg, "")
	if err != nil {
		t.Fatalf("ResolvePortForwards: %v", err)
	}
	want := []string{"13000:3000", "13001:3001", "8080:8080"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolvePortForwards_ConfigBeatsCompose(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCompose(t, dir, "docker-compose.yaml", `services:
  web:
    ports:
      - "9000:9000"
`)
	cfg := Config{PortForward: []string{"13000:3000"}}
	got, err := ResolvePortForwards(cfg, dir)
	if err != nil {
		t.Fatalf("ResolvePortForwards: %v", err)
	}
	if want := []string{"13000:3000"}; !reflect.DeepEqual(got, want) {
		t.Errorf("config list should win over compose; got %v, want %v", got, want)
	}
}

func TestResolvePortForwards_InvalidSpec(t *testing.T) {
	t.Parallel()
	if _, err := ResolvePortForwards(Config{PortForward: []string{"nope"}}, ""); err == nil {
		t.Fatal("expected error for non-numeric port spec")
	}
	if _, err := ResolvePortForwards(Config{PortForward: []string{"13000:999999"}}, ""); err == nil {
		t.Fatal("expected error for out-of-range port")
	}
}

func TestResolvePortForwards_NoSourceIsEmpty(t *testing.T) {
	t.Parallel()
	got, err := ResolvePortForwards(Config{}, t.TempDir())
	if err != nil {
		t.Fatalf("ResolvePortForwards: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no forwards, got %v", got)
	}
}

func TestResolvePortForwards_ComposeShortForms(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCompose(t, dir, "docker-compose.yaml", `services:
  web:
    ports:
      - "8080:80"
      - "3000"
  db:
    ports:
      - "127.0.0.1:5432:5432"
      - "9229:9229/tcp"
`)
	got, err := ResolvePortForwards(Config{}, dir)
	if err != nil {
		t.Fatalf("ResolvePortForwards: %v", err)
	}
	// Ordered by service name (db before web), then listing order; host
	// ports mapped to themselves.
	want := []string{"5432:5432", "9229:9229", "8080:8080", "3000:3000"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolvePortForwards_ComposeLongFormAndRange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCompose(t, dir, "compose.yaml", `services:
  app:
    ports:
      - target: 80
        published: 8080
      - "3000-3002:3000-3002"
`)
	got, err := ResolvePortForwards(Config{}, dir)
	if err != nil {
		t.Fatalf("ResolvePortForwards: %v", err)
	}
	want := []string{"8080:8080", "3000:3000", "3001:3001", "3002:3002"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestResolvePortForwards_ComposeFilePrecedence pins that compose.yaml
// is probed before the legacy docker-compose names, which in turn beat
// the podman-compose names.
func TestResolvePortForwards_ComposeFilePrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCompose(t, dir, "compose.yaml", `services:
  a:
    ports: ["1111:1111"]
`)
	writeCompose(t, dir, "docker-compose.yaml", `services:
  a:
    ports: ["2222:2222"]
`)
	got, err := ResolvePortForwards(Config{}, dir)
	if err != nil {
		t.Fatalf("ResolvePortForwards: %v", err)
	}
	if want := []string{"1111:1111"}; !reflect.DeepEqual(got, want) {
		t.Errorf("compose.yaml should take precedence; got %v, want %v", got, want)
	}
}

// TestResolvePortForwards_PodmanCompose pins that a podman-compose file
// is detected when no other compose file is present.
func TestResolvePortForwards_PodmanCompose(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCompose(t, dir, "podman-compose.yaml", `services:
  a:
    ports: ["3333:3333"]
`)
	got, err := ResolvePortForwards(Config{}, dir)
	if err != nil {
		t.Fatalf("ResolvePortForwards: %v", err)
	}
	if want := []string{"3333:3333"}; !reflect.DeepEqual(got, want) {
		t.Errorf("podman-compose.yaml should be detected; got %v, want %v", got, want)
	}
}

func writeCompose(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
