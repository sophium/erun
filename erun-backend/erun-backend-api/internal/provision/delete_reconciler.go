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

// EnvDeleteReconcilerEnvironments is the cross-tenant read/claim the
// reconciler needs: find every environment mid-teardown, then take over its
// delete attempt the same way a fresh operator request would.
type EnvDeleteReconcilerEnvironments interface {
	ListByStatuses(ctx context.Context, statuses []model.EnvironmentStatus) ([]model.Environment, error)
	ClaimDelete(ctx context.Context, environmentID string, staleAfter time.Duration) (bool, error)
}

// EnvDeleteReconcilerTenants resolves tenant identity for the reconciler's
// cross-tenant scan. List returns every tenant when called under an
// operations security context (repository.TenantRepository.List's documented
// behavior), which is the only way this reconciler can name the tenant an
// environment it found belongs to.
type EnvDeleteReconcilerTenants interface {
	List(ctx context.Context) ([]model.Tenant, error)
}

// EnvDeleteReconcilerContexts resolves a placement candidate's coordinates by
// id — the reconciler only reads back where an already-placed environment
// lives, never auto-selects or capacity-checks.
type EnvDeleteReconcilerContexts interface {
	Get(ctx context.Context, contextID string) (model.Context, error)
}

// EnvDeleteStarter kicks off one delete attempt asynchronously — satisfied by
// *EnvDeleter.
type EnvDeleteStarter interface {
	Start(input EnvDeleteInput) error
}

// EnvDeleteReconciler periodically re-attempts every environment whose
// teardown is mid-flight (deleting past its own deadline, or
// deletion-blocked) so a namespace that finishes terminating — or a solver
// that starts answering again — on its own converges the row without an
// operator noticing and re-issuing the delete (#1140). It runs as a DBOS
// scheduled workflow, the platform-wide cron primitive the deploy/provision
// side of this codebase does not otherwise need.
type EnvDeleteReconciler struct {
	environments EnvDeleteReconcilerEnvironments
	tenants      EnvDeleteReconcilerTenants
	contexts     EnvDeleteReconcilerContexts
	deleter      EnvDeleteStarter
}

// NewEnvDeleteReconciler wires and schedules the reconciler. schedule is a
// standard (6-field, second-precision) cron expression.
func NewEnvDeleteReconciler(dbosCtx dbos.DBOSContext, environments EnvDeleteReconcilerEnvironments, tenants EnvDeleteReconcilerTenants, contexts EnvDeleteReconcilerContexts, deleter EnvDeleteStarter, schedule string) *EnvDeleteReconciler {
	r := &EnvDeleteReconciler{environments: environments, tenants: tenants, contexts: contexts, deleter: deleter}
	dbos.RegisterWorkflow(dbosCtx, r.tick, dbos.WithSchedule(schedule))
	return r
}

// tick is the scheduled-workflow entrypoint DBOS calls on each cron fire; it
// exists only to supply the operations-scoped context reconcile needs, kept
// separate so reconcile itself is a plain function a test can call directly
// against fakes without a live DBOS scheduler.
func (r *EnvDeleteReconciler) tick(dctx dbos.DBOSContext, _ time.Time) (int, error) {
	ctx := security.WithContext(context.Background(), security.Context{TenantType: string(model.TenantTypeOperations)})
	return r.reconcile(ctx)
}

// reconcile scans every environment mid-teardown and re-attempts its delete.
// A stale `deleting` row (the attempt behind it can no longer be live, see
// DeleteClaimStaleAfter) and a `deletion-blocked` row (its previous attempt
// already reached a terminal outcome, see EnvironmentRepository.ClaimDelete)
// are both always reclaimable; a fresh, still-in-flight `deleting` row is
// left alone. Returns how many attempts it restarted, for the caller to log.
func (r *EnvDeleteReconciler) reconcile(ctx context.Context) (int, error) {
	environments, err := r.environments.ListByStatuses(ctx, []model.EnvironmentStatus{model.EnvironmentStatusDeleting, model.EnvironmentStatusDeletionBlocked})
	if err != nil {
		return 0, err
	}
	if len(environments) == 0 {
		return 0, nil
	}

	tenants, err := r.tenants.List(ctx)
	if err != nil {
		return 0, err
	}
	tenantsByID := make(map[string]model.Tenant, len(tenants))
	for _, tenant := range tenants {
		tenantsByID[tenant.TenantID] = tenant
	}

	restarted := 0
	for _, environment := range environments {
		didRestart, err := r.reconcileOne(ctx, environment, tenantsByID)
		if err != nil {
			log.Printf("erun api env delete reconciler: environment=%q: %v", environment.EnvironmentID, err)
			continue
		}
		if didRestart {
			restarted++
		}
	}
	return restarted, nil
}

// reconcileOne reports whether it actually restarted a delete attempt, not
// just whether it returned without error: a fresh in-flight `deleting` row is
// a legitimate no-op (err == nil, restarted == false), and must not inflate
// reconcile's count of attempts it took over.
func (r *EnvDeleteReconciler) reconcileOne(ctx context.Context, environment model.Environment, tenantsByID map[string]model.Tenant) (bool, error) {
	claimed, err := r.environments.ClaimDelete(ctx, environment.EnvironmentID, DeleteClaimStaleAfter)
	if err != nil {
		return false, err
	}
	if !claimed {
		// A fresh, still-in-flight deleting row: its own attempt has not had
		// time to finish yet, so this tick leaves it alone rather than racing it.
		return false, nil
	}

	tenant, ok := tenantsByID[environment.TenantID]
	if !ok {
		return false, fmt.Errorf("tenant %q not found", environment.TenantID)
	}
	placement, err := r.resolvePlacement(ctx, environment.ContextID)
	if err != nil {
		return false, err
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
		return false, err
	}
	return true, nil
}

// runningVersion mirrors routes.EnvironmentRoutes.lifecycleInput's choice: the
// version a delete Job targets is the last one actually deployed, falling
// back to the pinned version when the environment was never redeployed.
func runningVersion(environment model.Environment) string {
	if environment.DeployedVersion != "" {
		return environment.DeployedVersion
	}
	return environment.RuntimeVersion
}

// reconcilerPlacement is the target cluster a re-attempted delete Job
// authenticates against — the reconciler's own copy of routes.resolvedPlacement,
// since provision cannot import routes (see root AGENTS.md's module boundary
// rules) and the two are read the same way for an already-placed environment:
// no auto-select, no capacity check, just reading back where it already lives.
type reconcilerPlacement struct {
	ContextID         string
	KubernetesContext string
	ServerURL         string
}

// resolvePlacement reads back an already-placed environment's target-cluster
// coordinates. Empty contextID (the platform's own cluster) resolves to the
// zero reconcilerPlacement with no repository read, mirroring
// routes.EnvironmentRoutes.resolvePlacementCoordinates.
func (r *EnvDeleteReconciler) resolvePlacement(ctx context.Context, contextID string) (reconcilerPlacement, error) {
	if contextID == "" {
		return reconcilerPlacement{}, nil
	}
	cloudContext, err := r.contexts.Get(ctx, contextID)
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
