package app

import (
	"context"
	"io"

	"codebox/internal/container"
)

// WorkflowRequest is the use-case input for App.Workflow. Refspec is
// the same shape accepted by GitPush: either
// `source_remote/source_branch:target_branch` or
// `local_branch:target_branch`. The target_branch doubles as the
// instance name, so it must satisfy validateInstanceName.
//
// All other fields mirror CreateRequest — workflow internally chains
// Create -> GitPush -> Shell, so the create-time knobs (OS, language
// toolchains, agents, tools, proxy, rebuild) reach the build step.
type WorkflowRequest struct {
	Orchestrator string
	Remote       string
	InstanceKey  string
	Refspec      string

	OS         string
	Rebuild    bool
	HTTPSProxy string
	Python     string
	Node       string
	Golang     string
	Dotnet     string

	Claude            bool
	ClaudeCredentials bool
	Psql              bool
	Tmux              bool
	Podman            bool
}

// Workflow is a shortcut that chains create, git push, and shell.
//
// All argument formats are validated upfront so a bad refspec (or
// otherwise un-runnable input) is reported before any container is
// created. Once validation passes, the three steps are executed in
// order; any failure aborts the chain immediately.
func (a *App) Workflow(
	ctx context.Context,
	stdin io.Reader,
	stdout, stderr io.Writer,
	req WorkflowRequest,
) error {
	_, _, targetBranch, err := parsePushRefspec(req.Refspec)
	if err != nil {
		return err
	}
	if err := validateInstanceName(targetBranch); err != nil {
		return err
	}
	if _, err := container.New(req.Orchestrator); err != nil {
		return err
	}

	if err := a.Create(ctx, stdout, CreateRequest{
		Instance:          targetBranch,
		Orchestrator:      req.Orchestrator,
		OS:                req.OS,
		InstanceKey:       req.InstanceKey,
		Remote:            req.Remote,
		Rebuild:           req.Rebuild,
		HTTPSProxy:        req.HTTPSProxy,
		Python:            req.Python,
		Node:              req.Node,
		Golang:            req.Golang,
		Dotnet:            req.Dotnet,
		Claude:            req.Claude,
		ClaudeCredentials: req.ClaudeCredentials,
		Psql:              req.Psql,
		Tmux:              req.Tmux,
		Podman:            req.Podman,
	}); err != nil {
		return err
	}

	if err := a.GitPush(ctx, stdout, stderr, GitPushRequest{
		Instance:     targetBranch,
		Orchestrator: req.Orchestrator,
		Remote:       req.Remote,
		InstanceKey:  req.InstanceKey,
		Refspec:      req.Refspec,
	}); err != nil {
		return err
	}

	return a.Shell(ctx, stdin, stdout, stderr, ShellRequest{
		Instance:     targetBranch,
		Orchestrator: req.Orchestrator,
		Remote:       req.Remote,
		InstanceKey:  req.InstanceKey,
	})
}
