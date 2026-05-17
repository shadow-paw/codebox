package app

import (
	"context"
	"fmt"
	"io"

	"codebox/internal/image"
)

// CreateRequest is the use-case input for App.Create.
type CreateRequest struct {
	Instance     string
	Orchestrator string
	OS           string
	// InstanceKey is the path to the operator's SSH key on the host;
	// empty means the resolver should auto-detect.
	InstanceKey string
}

// Create renders the Dockerfile for a sandbox instance to w. This is
// the partial implementation requested by the spec: the resolver and
// generator are called, but the orchestrator is not invoked. The
// rendered Dockerfile is the contract under test until provisioning
// lands.
func (a *App) Create(_ context.Context, w io.Writer, req CreateRequest) error {
	if err := validateOrchestrator(req.Orchestrator); err != nil {
		return err
	}
	authKey, err := a.keys.Resolve(req.InstanceKey)
	if err != nil {
		return err
	}
	return image.Generate(w, image.Options{
		OS:            req.OS,
		AuthorizedKey: authKey,
	})
}

func validateOrchestrator(o string) error {
	switch o {
	case "podman", "docker":
		return nil
	default:
		return fmt.Errorf("unsupported orchestrator %q (known: podman, docker)", o)
	}
}
