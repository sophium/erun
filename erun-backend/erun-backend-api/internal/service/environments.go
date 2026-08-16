package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// EnvironmentStatusWriter persists an environment's provisioning-lifecycle
// transitions (the repository).
type EnvironmentStatusWriter interface {
	UpdateProvisioningStatus(ctx context.Context, environmentID string, update repository.EnvironmentStatusUpdate) error
}

// DeployRunner runs a hosted-env deploy to a terminal outcome — satisfied by the
// deployexec Job launcher, which the durable workflow supplies.
type DeployRunner interface {
	Run(ctx context.Context, params deployexec.DeployJobParams) (deployexec.Outcome, error)
}

const (
	// A lifecycle write that loses to a transient database blip would strand the
	// env in `provisioning` under a workflow that has already run to a terminal
	// state, so the write is worth a few attempts before giving up.
	statusWriteAttempts = 3
	statusWriteBackoff  = 200 * time.Millisecond
)

// EnvironmentProvisioner drives a hosted env's deploy: it marks the env
// provisioning, runs the deploy (a Job in the runtime image), and records the
// terminal running/failed status. The durable DBOS workflow wraps this; keeping
// the state transitions here makes them unit-testable without a cluster.
type EnvironmentProvisioner struct {
	runner  DeployRunner
	status  EnvironmentStatusWriter
	backoff time.Duration
}

func NewEnvironmentProvisioner(runner DeployRunner, status EnvironmentStatusWriter) *EnvironmentProvisioner {
	return &EnvironmentProvisioner{runner: runner, status: status, backoff: statusWriteBackoff}
}

// Provision moves the env → provisioning → running/failed. A run error or a
// non-succeeded deploy Job both land it in failed with the reason; only a
// succeeded Job marks it running, recording the version that actually deployed.
func (p *EnvironmentProvisioner) Provision(ctx context.Context, environmentID string, params deployexec.DeployJobParams) error {
	if err := p.write(ctx, environmentID, repository.EnvironmentStatusUpdate{
		Status: string(model.EnvironmentStatusProvisioning),
	}); err != nil {
		return fmt.Errorf("mark provisioning: %w", err)
	}
	outcome, runErr := p.runner.Run(ctx, params)
	if runErr != nil {
		return p.fail(ctx, environmentID, runErr.Error(), runErr)
	}
	if outcome != deployexec.OutcomeSucceeded {
		return p.fail(ctx, environmentID, fmt.Sprintf("deploy job %s", outcome), fmt.Errorf("deploy job outcome %q", outcome))
	}
	// The env is now running this version, so record it here rather than after
	// any later step: a run that fails past this point still leaves the cluster
	// carrying it, and an operator recovering needs the env to name it.
	if err := p.write(ctx, environmentID, repository.EnvironmentStatusUpdate{
		Status:          string(model.EnvironmentStatusRunning),
		DeployedVersion: params.Version,
	}); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}
	return nil
}

// fail records the failed status best-effort and returns the underlying cause,
// so a status-write hiccup never masks the real provisioning failure. The write
// error is logged rather than dropped, because a lost failure write is what
// leaves an env stranded in `provisioning`.
func (p *EnvironmentProvisioner) fail(ctx context.Context, environmentID, reason string, cause error) error {
	update := repository.EnvironmentStatusUpdate{
		Status:         string(model.EnvironmentStatusFailed),
		ProvisionError: reason,
	}
	if err := p.write(ctx, environmentID, update); err != nil {
		log.Printf("erun api env deploy: recording failed status for environment=%q did not persist: %v (deploy failure: %v)", environmentID, err, cause)
	}
	return cause
}

// write applies one lifecycle transition, retrying a transient failure a bounded
// number of times.
func (p *EnvironmentProvisioner) write(ctx context.Context, environmentID string, update repository.EnvironmentStatusUpdate) error {
	var err error
	for attempt := range statusWriteAttempts {
		if err = p.status.UpdateProvisioningStatus(ctx, environmentID, update); err == nil {
			return nil
		}
		if attempt == statusWriteAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.backoff):
		}
	}
	return err
}
