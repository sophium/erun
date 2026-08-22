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

// EnvironmentStatusWriter persists an environment's provisioning-lifecycle
// transitions (the repository).
type EnvironmentStatusWriter interface {
	UpdateProvisioningStatus(ctx context.Context, environmentID string, update repository.EnvironmentStatusUpdate) error
}

// DeployRunner runs a hosted-env deploy to a terminal result — satisfied by the
// deployexec Job launcher, which the durable workflow supplies.
type DeployRunner interface {
	Run(ctx context.Context, params deployexec.DeployJobParams) (deployexec.Result, error)
}

// UsageRecorder records a per-tenant metering event (#605). Optional: a nil
// recorder simply records nothing, matching behavior before metering existed.
type UsageRecorder interface {
	Record(ctx context.Context, event model.UsageEvent) error
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
	runner      DeployRunner
	status      EnvironmentStatusWriter
	usage       UsageRecorder
	credentials deployexec.PlacementCredentialResolver
	backoff     time.Duration
}

// NewEnvironmentProvisioner wires the provisioner. usage may be nil, which
// records no metering event (Provision behaves exactly as before metering
// existed). credentials may be nil, which refuses (rather than silently
// deploying unauthenticated) any environment that names a context (#1112);
// every environment placed into the platform's own cluster is unaffected
// either way.
func NewEnvironmentProvisioner(runner DeployRunner, status EnvironmentStatusWriter, usage UsageRecorder, credentials deployexec.PlacementCredentialResolver) *EnvironmentProvisioner {
	return &EnvironmentProvisioner{runner: runner, status: status, usage: usage, credentials: credentials, backoff: statusWriteBackoff}
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
	token, err := deployexec.ResolvePlacementToken(ctx, p.credentials, params.Placement.ContextID)
	if err != nil {
		return p.fail(ctx, environmentID, err.Error(), err)
	}
	params.Placement.AdminToken = token
	result, runErr := p.runner.Run(ctx, params)
	if runErr != nil {
		return p.fail(ctx, environmentID, runErr.Error(), runErr)
	}
	if result.Outcome != deployexec.OutcomeSucceeded {
		return p.fail(ctx, environmentID, deployFailureReason(params, result), fmt.Errorf("deploy job outcome %q", result.Outcome))
	}
	// The env is now running this version, so record it here rather than after
	// any later step: a run that fails past this point still leaves the cluster
	// carrying it, and an operator recovering needs the env to name it.
	// deployexec chains the environment's exposure onto the same Job but never
	// lets its failure fail the Job (#1086) — the deploy already landed a
	// healthy workload, so a DNS/Ingress problem downstream of it must not read
	// as a failed provision. ExposeError names that failure distinctly instead,
	// leaving Status/ProvisionError to mean exactly "did the deploy land".
	if err := p.write(ctx, environmentID, repository.EnvironmentStatusUpdate{
		Status:          string(model.EnvironmentStatusRunning),
		DeployedVersion: params.Version,
		ExposeError:     deployexec.ExposeFailureFromOutput(result.Output),
	}); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}
	p.recordUsage(ctx, environmentID, params)
	return nil
}

// recordUsage is best-effort: a metering write failing must never turn a
// successful deploy into a reported failure, so it only logs.
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

// deployFailureReason is what the environment records when a deploy Job does not
// succeed. The version is named here because it is the control plane's own fact;
// everything actionable about *why* comes from the deploy itself, which already
// knows the coordinates it probed. Without that detail the record says only that
// a Job exited, which is nothing to act on.
func deployFailureReason(params deployexec.DeployJobParams, result deployexec.Result) string {
	reason := fmt.Sprintf("deploy job %s for version %s", result.Outcome, params.Version)
	if detail := strings.TrimSpace(result.Failure); detail != "" {
		return reason + ": " + detail
	}
	return reason + " and left no reason behind (its pod was already reclaimed); `kubectl -n " + params.Namespace + " logs job/" + deployexec.DeployJobName(params.Tenant, params.Environment, params.Version, params.DeployID) + "` while a deploy is in flight shows what it is doing"
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
