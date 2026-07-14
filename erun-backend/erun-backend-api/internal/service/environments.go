package service

import (
	"context"
	"fmt"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// EnvironmentStatusWriter persists an environment's provisioning-lifecycle
// transitions (the repository).
type EnvironmentStatusWriter interface {
	UpdateProvisioningStatus(ctx context.Context, environmentID, status, provisionError string) error
}

// DeployRunner runs a hosted-env deploy to a terminal outcome — satisfied by the
// deployexec Job launcher, which the durable workflow supplies.
type DeployRunner interface {
	Run(ctx context.Context, params deployexec.DeployJobParams) (deployexec.Outcome, error)
}

// EnvironmentProvisioner drives a hosted env's deploy: it marks the env
// provisioning, runs the deploy (a Job in the runtime image), and records the
// terminal running/failed status. The durable DBOS workflow wraps this; keeping
// the state transitions here makes them unit-testable without a cluster.
type EnvironmentProvisioner struct {
	runner DeployRunner
	status EnvironmentStatusWriter
}

func NewEnvironmentProvisioner(runner DeployRunner, status EnvironmentStatusWriter) *EnvironmentProvisioner {
	return &EnvironmentProvisioner{runner: runner, status: status}
}

// Provision moves the env → provisioning → running/failed. A run error or a
// non-succeeded deploy Job both land it in failed with the reason; only a
// succeeded Job marks it running.
func (p *EnvironmentProvisioner) Provision(ctx context.Context, environmentID string, params deployexec.DeployJobParams) error {
	if err := p.status.UpdateProvisioningStatus(ctx, environmentID, string(model.EnvironmentStatusProvisioning), ""); err != nil {
		return fmt.Errorf("mark provisioning: %w", err)
	}
	outcome, runErr := p.runner.Run(ctx, params)
	if runErr != nil {
		return p.fail(ctx, environmentID, runErr.Error(), runErr)
	}
	if outcome != deployexec.OutcomeSucceeded {
		return p.fail(ctx, environmentID, fmt.Sprintf("deploy job %s", outcome), fmt.Errorf("deploy job outcome %q", outcome))
	}
	if err := p.status.UpdateProvisioningStatus(ctx, environmentID, string(model.EnvironmentStatusRunning), ""); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}
	return nil
}

// fail records the failed status best-effort and returns the underlying cause,
// so a status-write hiccup never masks the real provisioning failure.
func (p *EnvironmentProvisioner) fail(ctx context.Context, environmentID, reason string, cause error) error {
	_ = p.status.UpdateProvisioningStatus(ctx, environmentID, string(model.EnvironmentStatusFailed), reason)
	return cause
}
