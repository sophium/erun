package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// EnvironmentStatusWriter persists an environment's provisioning-lifecycle transitions.
type EnvironmentStatusWriter interface {
	// UpdateProvisioningStatus takes tenantID explicitly: the durable workflow's context does not reliably name the owning tenant.
	UpdateProvisioningStatus(ctx context.Context, tenantID, environmentID string, update repository.EnvironmentStatusUpdate) error
}

// DeployRunner runs a hosted-env deploy to a terminal result.
type DeployRunner interface {
	Run(ctx context.Context, params deployexec.DeployJobParams) (deployexec.Result, error)
}

// UsageRecorder records a per-tenant metering event; a nil recorder records nothing.
type UsageRecorder interface {
	Record(ctx context.Context, event model.UsageEvent) error
}

const (
	// A lost lifecycle write would strand the env in `provisioning` under a finished workflow.
	statusWriteAttempts = 3
	statusWriteBackoff  = 200 * time.Millisecond
)

// EnvironmentProvisioner holds a hosted env's lifecycle transitions outside the DBOS workflow so they are unit-testable.
type EnvironmentProvisioner struct {
	runner      DeployRunner
	status      EnvironmentStatusWriter
	usage       UsageRecorder
	credentials deployexec.PlacementCredentialResolver
	backoff     time.Duration
}

// NewEnvironmentProvisioner wires the provisioner. A nil credentials resolver refuses any environment naming a
// context rather than deploying unauthenticated.
func NewEnvironmentProvisioner(runner DeployRunner, status EnvironmentStatusWriter, usage UsageRecorder, credentials deployexec.PlacementCredentialResolver) *EnvironmentProvisioner {
	return &EnvironmentProvisioner{runner: runner, status: status, usage: usage, credentials: credentials, backoff: statusWriteBackoff}
}

// Provision moves the env to provisioning, then running or failed; only a succeeded Job marks it running.
func (p *EnvironmentProvisioner) Provision(ctx context.Context, environmentID string, params deployexec.DeployJobParams) error {
	if err := p.write(ctx, params.Placement.TenantID, environmentID, repository.EnvironmentStatusUpdate{
		Status: string(model.EnvironmentStatusProvisioning),
	}); err != nil {
		return fmt.Errorf("mark provisioning: %w", err)
	}
	token, err := deployexec.ResolvePlacementToken(ctx, p.credentials, params.Placement.TenantID, params.Placement.ContextID)
	if err != nil {
		return p.fail(ctx, params.Placement.TenantID, environmentID, err.Error(), err)
	}
	params.Placement.AdminToken = token
	result, runErr := p.runner.Run(ctx, params)
	if runErr != nil {
		return p.fail(ctx, params.Placement.TenantID, environmentID, runErr.Error(), runErr)
	}
	if result.Outcome != deployexec.OutcomeSucceeded {
		return p.fail(ctx, params.Placement.TenantID, environmentID, deployFailureReason(params, result), fmt.Errorf("deploy job outcome %q", result.Outcome))
	}
	// The chained expose never fails the Job, so its failure is recorded separately in ExposeError.
	exposeError := deployexec.ExposeFailureFromOutput(result.Output)
	var exposedHostname string
	if exposeError == "" {
		exposedHostname = deployexec.ExposeHostnameFromParams(params)
	}
	if err := p.write(ctx, params.Placement.TenantID, environmentID, repository.EnvironmentStatusUpdate{
		Status:          string(model.EnvironmentStatusRunning),
		DeployedVersion: params.Version,
		ExposeError:     exposeError,
		ExposedHostname: exposedHostname,
	}); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}
	p.recordUsage(ctx, environmentID, params)
	return nil
}

// recordUsage is best-effort: metering must never fail a successful deploy.
func (p *EnvironmentProvisioner) recordUsage(ctx context.Context, environmentID string, params deployexec.DeployJobParams) {
	if p.usage == nil {
		return
	}
	err := p.usage.Record(ctx, model.UsageEvent{
		EnvironmentID: environmentID,
		EventType:     string(model.UsageEventEnvironmentProvisioned),
		CPUMillicores: params.MaxCPUMillicores,
		MemoryMB:      params.MaxMemoryMB,
		StorageGB:     params.MaxStorageGB,
	})
	if err != nil {
		log.Printf("erun api env deploy: recording usage event for environment=%q did not persist: %v", environmentID, err)
	}
}

func deployFailureReason(params deployexec.DeployJobParams, result deployexec.Result) string {
	reason := fmt.Sprintf("deploy job %s for version %s", result.Outcome, params.Version)
	if detail := strings.TrimSpace(result.Failure); detail != "" {
		return reason + ": " + detail
	}
	return reason + " and left no reason behind (its pod was already reclaimed); `kubectl -n " + params.Namespace + " logs job/" + deployexec.DeployJobName(params.Tenant, params.Environment, params.Version, params.DeployID) + "` while a deploy is in flight shows what it is doing"
}

// fail returns the real cause; a failed status write is logged, not allowed to mask it.
func (p *EnvironmentProvisioner) fail(ctx context.Context, tenantID, environmentID, reason string, cause error) error {
	update := repository.EnvironmentStatusUpdate{
		Status:         string(model.EnvironmentStatusFailed),
		ProvisionError: reason,
	}
	if err := p.write(ctx, tenantID, environmentID, update); err != nil {
		log.Printf("erun api env deploy: recording failed status for environment=%q did not persist: %v (deploy failure: %v)", environmentID, err, cause)
	}
	return cause
}

func (p *EnvironmentProvisioner) write(ctx context.Context, tenantID, environmentID string, update repository.EnvironmentStatusUpdate) error {
	var err error
	for attempt := range statusWriteAttempts {
		if err = p.status.UpdateProvisioningStatus(ctx, tenantID, environmentID, update); err == nil {
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
