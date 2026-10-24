package provision

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// UsageRecorder records a per-tenant metering event.
type UsageRecorder interface {
	Record(ctx context.Context, event model.UsageEvent) error
}

// EnvLifecycleRunner runs a hosted env's stop/delete Job to a terminal result.
type EnvLifecycleRunner interface {
	RunStop(ctx context.Context, params deployexec.StopJobParams) (deployexec.Result, error)
	RunDelete(ctx context.Context, params deployexec.DeleteJobParams) (deployexec.Result, error)
}

// EnvironmentRowDeleter removes an environment's row or records why a delete
// was blocked; `running` must never survive Delete. The tenant is explicit
// because the durable workflow context carries no tenant identity.
type EnvironmentRowDeleter interface {
	Delete(ctx context.Context, tenantID, environmentID string) error
	MarkDeleteBlocked(ctx context.Context, tenantID, environmentID, reason string) error
}

// MachineIdentityRevoker removes an environment's platform identity. It must be
// idempotent (Delete retries from the top) and treat "no identity" as success.
type MachineIdentityRevoker interface {
	Revoke(ctx context.Context, environmentID string) error
}

// EnvLifecycleInput is the non-secret placement a stop or delete Job needs.
type EnvLifecycleInput struct {
	// TenantID scopes the credential read; Tenant is only a display/namespace value.
	TenantID      string
	Tenant        string
	Environment   string
	EnvironmentID string
	// RunningVersion is the last deployed version; empty means no namespace exists.
	RunningVersion string
	// ContextID is read back from the persisted row, never re-placed; empty
	// means the platform's own cluster.
	ContextID                  string
	PlacementKubernetesContext string
	PlacementServerURL         string
	DeleteID                   string
	// StopID gives each explicit stop a fresh Job rather than replaying a prior
	// attempt's outcome; the caller mints one per request.
	StopID string
}

// EnvLifecycle runs a hosted env's stop/delete Job and persists the row.
// Delete runs inside a durable workflow because namespace teardown can wedge.
type EnvLifecycle struct {
	runner       EnvLifecycleRunner
	rows         EnvironmentRowDeleter
	config       EnvDeployConfig
	usage        UsageRecorder
	imageChecker RuntimeImageChecker
	credentials  deployexec.PlacementCredentialResolver
	// identities may be nil when the deployment mints no machine identities.
	identities MachineIdentityRevoker
}

// NewEnvLifecycle wires stop/delete. usage, imageChecker and identities may be
// nil. A nil credentials resolver refuses any environment that names a context
// rather than deploying unauthenticated.
func NewEnvLifecycle(runner EnvLifecycleRunner, rows EnvironmentRowDeleter, config EnvDeployConfig, usage UsageRecorder, imageChecker RuntimeImageChecker, credentials deployexec.PlacementCredentialResolver, identities MachineIdentityRevoker) *EnvLifecycle {
	return &EnvLifecycle{runner: runner, rows: rows, config: config, usage: usage, imageChecker: imageChecker, credentials: credentials, identities: identities}
}

func (l *EnvLifecycle) placement(ctx context.Context, input EnvLifecycleInput) (deployexec.PlacementParams, error) {
	token, err := deployexec.ResolvePlacementToken(ctx, l.credentials, input.TenantID, input.ContextID)
	if err != nil {
		return deployexec.PlacementParams{}, err
	}
	return deployexec.PlacementParams{
		TenantID:          input.TenantID,
		ContextID:         input.ContextID,
		KubernetesContext: input.PlacementKubernetesContext,
		ServerURL:         input.PlacementServerURL,
		AdminToken:        token,
	}, nil
}

func (l *EnvLifecycle) recordUsage(ctx context.Context, environmentID string, eventType model.UsageEventType) {
	if l.usage == nil {
		return
	}
	if err := l.usage.Record(ctx, model.UsageEvent{EnvironmentID: environmentID, EventType: string(eventType)}); err != nil {
		log.Printf("erun api env lifecycle: recording usage event for environment=%q did not persist: %v", environmentID, err)
	}
}

// image applies the same published-image fallback as deploy, so a bootstrapped
// environment's stop/delete Jobs run the image it is actually running.
func (l *EnvLifecycle) image(ctx context.Context, tenant, version string) string {
	image, _ := ResolveRuntimeImage(ctx, l.imageChecker, l.config.Registry, tenant, version)
	return image
}

// Stop scales the runtime Deployment to zero; the environment stays "running".
func (l *EnvLifecycle) Stop(ctx context.Context, input EnvLifecycleInput) error {
	if strings.TrimSpace(input.RunningVersion) == "" {
		return fmt.Errorf("environment has never been deployed; nothing to stop")
	}
	placement, err := l.placement(ctx, input)
	if err != nil {
		return err
	}
	result, err := l.runner.RunStop(ctx, deployexec.StopJobParams{
		Tenant:         input.Tenant,
		Environment:    input.Environment,
		Namespace:      l.config.PlatformNamespace,
		Image:          l.image(ctx, input.Tenant, input.RunningVersion),
		ServiceAccount: l.config.DeployerServiceAccount,
		StopID:         input.StopID,
		Placement:      placement,
	})
	if err != nil {
		return err
	}
	if result.Outcome != deployexec.OutcomeSucceeded {
		return fmt.Errorf("stop job %s: %s", result.Outcome, lifecycleFailureDetail(result))
	}
	l.recordUsage(ctx, input.EnvironmentID, model.UsageEventEnvironmentStopped)
	return nil
}

// Delete tears down the namespace, revokes the machine identity, then removes
// the row. Any failure moves the row to deletion-blocked naming why.
func (l *EnvLifecycle) Delete(ctx context.Context, input EnvLifecycleInput) error {
	if strings.TrimSpace(input.RunningVersion) != "" {
		if err := l.runDeleteJob(ctx, input); err != nil {
			return err
		}
	}
	l.recordUsage(ctx, input.EnvironmentID, model.UsageEventEnvironmentDeleted)
	// Revoke after the pod is gone (it still uses the credential) and before
	// the row is removed (the last record naming what to revoke).
	if l.identities != nil {
		if err := l.identities.Revoke(ctx, input.EnvironmentID); err != nil {
			return l.blockDelete(ctx, input, fmt.Errorf("namespace torn down but revoking the environment's platform identity failed: %w", err))
		}
	}
	if err := l.rows.Delete(ctx, input.TenantID, input.EnvironmentID); err != nil {
		return l.blockDelete(ctx, input, fmt.Errorf("namespace torn down but removing the environment row failed: %w", err))
	}
	return nil
}

// runDeleteJob tears the environment's namespace down through a delete Job,
// returning the blockDelete error for any outcome that left it standing.
func (l *EnvLifecycle) runDeleteJob(ctx context.Context, input EnvLifecycleInput) error {
	placement, err := l.placement(ctx, input)
	if err != nil {
		return l.blockDelete(ctx, input, err)
	}
	result, err := l.runner.RunDelete(ctx, deployexec.DeleteJobParams{
		Tenant:                  input.Tenant,
		Environment:             input.Environment,
		Namespace:               l.config.PlatformNamespace,
		Image:                   l.image(ctx, input.Tenant, input.RunningVersion),
		ServiceAccount:          l.config.DeployerServiceAccount,
		DeleteID:                input.DeleteID,
		ExposeServicesZone:      l.config.ExposeServicesZone,
		ExposePlatformNamespace: l.config.ExposePlatformNamespace,
		Placement:               placement,
	})
	if err != nil {
		return l.blockDelete(ctx, input, err)
	}
	if result.Outcome != deployexec.OutcomeSucceeded {
		return l.blockDelete(ctx, input, fmt.Errorf("delete job %s: %s", result.Outcome, lifecycleFailureDetail(result)))
	}
	// `erun delete` treats namespace-teardown failure as non-fatal, so a
	// succeeded Job can still have left the namespace stuck.
	if blocker := deployexec.NamespaceDeleteFailureFromOutput(result.Output); blocker != "" {
		return l.blockDelete(ctx, input, errors.New(blocker))
	}
	// The row is about to be removed, so the log is the only place left.
	if reason := deployexec.UnexposeFailureFromOutput(result.Output); reason != "" {
		log.Printf("erun api env lifecycle: dns cleanup for %s/%s did not succeed: %s", input.Tenant, input.Environment, reason)
	}
	return nil
}

// blockDelete records why a delete was blocked and returns cause.
func (l *EnvLifecycle) blockDelete(ctx context.Context, input EnvLifecycleInput, cause error) error {
	if err := l.rows.MarkDeleteBlocked(ctx, input.TenantID, input.EnvironmentID, cause.Error()); err != nil {
		log.Printf("erun api env lifecycle: recording delete-blocked for environment=%q did not persist: %v", input.EnvironmentID, err)
	}
	return cause
}

func lifecycleFailureDetail(result deployexec.Result) string {
	if detail := strings.TrimSpace(result.Failure); detail != "" {
		return detail
	}
	return "no reason recorded (its pod was already reclaimed)"
}
