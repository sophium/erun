package provision

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/google/uuid"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// EnvDeleteReconcilerEnvironments is the reconciler's cross-tenant read and claim.
type EnvDeleteReconcilerEnvironments interface {
	ListByStatuses(ctx context.Context, statuses []model.EnvironmentStatus) ([]model.Environment, error)
	// The tenant is explicit: the reconciler's context names none.
	ClaimDelete(ctx context.Context, tenantID, environmentID string, staleAfter time.Duration) (bool, error)
	// MarkDeleteBlocked records why a claimed row's workflow never started, so it
	// is not left `deleting` with no attempt behind it.
	MarkDeleteBlocked(ctx context.Context, tenantID, environmentID, reason string) error
}

// EnvDeleteReconcilerTenants lists every tenant under an operations context.
type EnvDeleteReconcilerTenants interface {
	List(ctx context.Context) ([]model.Tenant, error)
}

// EnvDeleteReconcilerContexts reads back an already-placed environment's context.
type EnvDeleteReconcilerContexts interface {
	// Get takes the row's tenant explicitly; the caller identity is empty.
	Get(ctx context.Context, tenantID, contextID string) (model.Context, error)
}

type EnvDeleteStarter interface {
	Start(input EnvDeleteInput) error
}

// MaxDeleteAttempts caps teardown retries; past it the row stays
// `deletion-blocked` as the "needs intervention" state.
const MaxDeleteAttempts = 8

// Blocked teardowns back off exponentially; a wedged finalizer will not clear
// on every tick.
const (
	deleteRetryFirstBackoff = 5 * time.Minute
	deleteRetryMaxBackoff   = 2 * time.Hour
)

func deleteRetryBackoff(attempts int) time.Duration {
	if attempts < 1 {
		return 0
	}
	backoff := deleteRetryFirstBackoff
	for i := 1; i < attempts; i++ {
		backoff *= 2
		if backoff >= deleteRetryMaxBackoff {
			return deleteRetryMaxBackoff
		}
	}
	return backoff
}

// Only `deletion-blocked` rows back off: a stale `deleting` row's attempt is
// already dead.
func deleteRetryDue(environment model.Environment, now time.Time) bool {
	if environment.Status != model.EnvironmentStatusDeletionBlocked {
		return true
	}
	return !now.Before(environment.UpdatedAt.Add(deleteRetryBackoff(environment.DeleteAttempts)))
}

// EnvDeleteReconciler periodically re-attempts stale `deleting` and
// `deletion-blocked` environments as a DBOS scheduled workflow.
type EnvDeleteReconciler struct {
	environments EnvDeleteReconcilerEnvironments
	tenants      EnvDeleteReconcilerTenants
	contexts     EnvDeleteReconcilerContexts
	deleter      EnvDeleteStarter
}

// NewEnvDeleteReconciler takes a 6-field, second-precision cron schedule.
func NewEnvDeleteReconciler(dbosCtx dbos.DBOSContext, environments EnvDeleteReconcilerEnvironments, tenants EnvDeleteReconcilerTenants, contexts EnvDeleteReconcilerContexts, deleter EnvDeleteStarter, schedule string) *EnvDeleteReconciler {
	r := &EnvDeleteReconciler{environments: environments, tenants: tenants, contexts: contexts, deleter: deleter}
	dbos.RegisterWorkflow(dbosCtx, r.tick, dbos.WithSchedule(schedule))
	return r
}

// tick supplies the operations context so reconcile stays testable without DBOS.
func (r *EnvDeleteReconciler) tick(dctx dbos.DBOSContext, _ time.Time) (int, error) {
	// Rooted in the DBOS context so a shutdown cancels the scan.
	ctx := security.WithContext(dctx, security.Context{TenantType: string(model.TenantTypeOperations)})
	return r.reconcile(ctx)
}

// reconcile re-attempts stale `deleting` and `deletion-blocked` rows, leaving
// in-flight deletes alone, and returns how many it restarted.
func (r *EnvDeleteReconciler) reconcile(ctx context.Context) (int, error) {
	environments, err := r.environments.ListByStatuses(ctx, []model.EnvironmentStatus{model.EnvironmentStatusDeleting, model.EnvironmentStatusDeletionBlocked})
	if err != nil {
		log.Printf("erun api env delete reconciler: scan failed: %v", err)
		return 0, err
	}
	if len(environments) == 0 {
		return 0, nil
	}

	tenants, err := r.tenants.List(ctx)
	if err != nil {
		log.Printf("erun api env delete reconciler: tenant scan failed with %d environment(s) mid-teardown: %v", len(environments), err)
		return 0, err
	}
	tenantsByID := make(map[string]model.Tenant, len(tenants))
	for _, tenant := range tenants {
		tenantsByID[tenant.TenantID] = tenant
	}

	counts := r.reconcileEach(ctx, environments, tenantsByID)
	if counts.restarted > 0 || counts.failed > 0 || counts.exhausted > 0 {
		log.Printf("erun api env delete reconciler: %d mid-teardown environment(s): %d re-attempted, %d failed to start, %d exhausted, %d waiting on backoff",
			len(environments), counts.restarted, counts.failed, counts.exhausted, counts.waiting)
	}
	return counts.restarted, nil
}

type reconcileCounts struct {
	restarted int
	failed    int
	exhausted int
	waiting   int
}

func (r *EnvDeleteReconciler) reconcileEach(ctx context.Context, environments []model.Environment, tenantsByID map[string]model.Tenant) reconcileCounts {
	var counts reconcileCounts
	now := time.Now()
	for _, environment := range environments {
		if environment.DeleteAttempts >= MaxDeleteAttempts {
			counts.exhausted++
			log.Printf("erun api env delete reconciler: environment=%q has failed %d delete attempts and needs intervention; not re-attempting. Recorded blocker: %s",
				environment.EnvironmentID, environment.DeleteAttempts, firstLine(environment.DeleteError))
			continue
		}
		if !deleteRetryDue(environment, now) {
			counts.waiting++
			continue
		}
		didRestart, err := r.reconcileOne(ctx, environment, tenantsByID)
		switch {
		case err != nil:
			counts.failed++
			log.Printf("erun api env delete reconciler: environment=%q: %v", environment.EnvironmentID, err)
		case didRestart:
			counts.restarted++
		}
	}
	return counts
}

func firstLine(s string) string {
	if s == "" {
		return "(none recorded)"
	}
	if i := strings.IndexByte(s, 0x0a); i >= 0 {
		return s[:i]
	}
	return s
}

// restarted is false for a fresh in-flight row, which is not an error.
func (r *EnvDeleteReconciler) reconcileOne(ctx context.Context, environment model.Environment, tenantsByID map[string]model.Tenant) (bool, error) {
	claimed, err := r.environments.ClaimDelete(ctx, environment.TenantID, environment.EnvironmentID, DeleteClaimStaleAfter)
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, nil
	}

	// The row is now claimed: every exit must start a workflow or record why not.
	tenant, ok := tenantsByID[environment.TenantID]
	if !ok {
		return false, r.unclaim(ctx, environment.TenantID, environment.EnvironmentID, fmt.Errorf("tenant %q not found", environment.TenantID))
	}
	placement, err := r.resolvePlacement(ctx, environment.TenantID, environment.ContextID)
	if err != nil {
		return false, r.unclaim(ctx, environment.TenantID, environment.EnvironmentID, fmt.Errorf("resolve placement: %w", err))
	}

	if err := r.deleter.Start(EnvDeleteInput{
		TenantID:                   environment.TenantID,
		TenantType:                 string(tenant.Type),
		EnvironmentID:              environment.EnvironmentID,
		Tenant:                     tenant.Name,
		Environment:                environment.Name,
		RunningVersion:             runningVersion(environment),
		ContextID:                  placement.ContextID,
		PlacementKubernetesContext: placement.KubernetesContext,
		PlacementServerURL:         placement.ServerURL,
		DeleteID:                   uuid.NewString(),
	}); err != nil {
		return false, r.unclaim(ctx, environment.TenantID, environment.EnvironmentID, fmt.Errorf("start delete workflow: %w", err))
	}
	return true, nil
}

// unclaim moves a claimed row to `deletion-blocked` with a reason and returns cause.
func (r *EnvDeleteReconciler) unclaim(ctx context.Context, tenantID, environmentID string, cause error) error {
	if err := r.environments.MarkDeleteBlocked(ctx, tenantID, environmentID, cause.Error()); err != nil {
		return fmt.Errorf("%w (and recording it did not persist: %w)", cause, err)
	}
	return cause
}

// runningVersion is the last deployed version, else the pinned one (as routes does).
func runningVersion(environment model.Environment) string {
	if environment.DeployedVersion != "" {
		return environment.DeployedVersion
	}
	return environment.RuntimeVersion
}

// reconcilerPlacement mirrors routes.resolvedPlacement; provision cannot import routes.
type reconcilerPlacement struct {
	ContextID         string
	KubernetesContext string
	ServerURL         string
}

// resolvePlacement reads back an already-placed environment's cluster; an empty
// contextID is the platform's own cluster.
func (r *EnvDeleteReconciler) resolvePlacement(ctx context.Context, tenantID, contextID string) (reconcilerPlacement, error) {
	if contextID == "" {
		return reconcilerPlacement{}, nil
	}
	cloudContext, err := r.contexts.Get(ctx, tenantID, contextID)
	if err != nil {
		return reconcilerPlacement{}, err
	}
	kubernetesContext := cloudContext.KubernetesContext
	if kubernetesContext == "" {
		kubernetesContext = cloudContext.Name
	}
	return reconcilerPlacement{
		ContextID:         cloudContext.ContextID,
		KubernetesContext: kubernetesContext,
		ServerURL:         "https://" + strings.TrimSpace(cloudContext.PublicIP) + ":6443",
	}, nil
}
