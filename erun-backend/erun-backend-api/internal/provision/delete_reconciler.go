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
	// MarkDeleteBlocked is what keeps a claim from stranding a row: the claim
	// has already moved it to `deleting`, so any failure between the claim and
	// the workflow actually starting must record why, or the row is left
	// claiming an in-flight delete that does not exist (#1166).
	MarkDeleteBlocked(ctx context.Context, environmentID, reason string) error
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

// MaxDeleteAttempts bounds how many times the reconciler re-attempts one
// environment's teardown. Past it the row is left alone: a teardown that has
// failed this many times is not going to succeed on its own, and re-attempting
// it forever buries that fact under identical ticks instead of surfacing it.
// The row stays `deletion-blocked` with its recorded blocker and a visible
// DeleteAttempts count, which together are the terminal "needs intervention"
// state.
const MaxDeleteAttempts = 8

// deleteRetryFirstBackoff and deleteRetryMaxBackoff shape the wait between
// re-attempts of a blocked teardown, doubling per attempt. Without a backoff
// every blocked row was re-attempted on every tick, which for a namespace
// wedged on an unsatisfiable finalizer is pure load with no chance of
// progress.
const (
	deleteRetryFirstBackoff = 5 * time.Minute
	deleteRetryMaxBackoff   = 2 * time.Hour
)

// deleteRetryBackoff is how long a row must sit after its previous attempt
// before another is worth making: exponential in the attempt count, capped.
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

// deleteRetryDue reports whether a blocked row has waited out its backoff.
// Only `deletion-blocked` rows are held back: a stale `deleting` row means the
// attempt behind it can no longer be live (DeleteClaimStaleAfter is already
// longer than the Job's own deadline), so delaying that one further would just
// leave a dead attempt sitting.
func deleteRetryDue(environment model.Environment, now time.Time) bool {
	if environment.Status != model.EnvironmentStatusDeletionBlocked {
		return true
	}
	return !now.Before(environment.UpdatedAt.Add(deleteRetryBackoff(environment.DeleteAttempts)))
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
	// Rooted in the DBOS context, not context.Background(): a scan on a
	// background context is uncancellable and keeps running straight through a
	// shutdown (#1166).
	ctx := security.WithContext(dctx, security.Context{TenantType: string(model.TenantTypeOperations)})
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
		// A reconciler failing every tick must not be indistinguishable from
		// one with nothing to do -- silence is what let the original bug hide
		// behind a successful-looking deploy (#1166).
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
	// One line per tick that did something, so a reconciler doing work and a
	// reconciler silently achieving nothing are distinguishable in the log.
	if counts.restarted > 0 || counts.failed > 0 || counts.exhausted > 0 {
		log.Printf("erun api env delete reconciler: %d mid-teardown environment(s): %d re-attempted, %d failed to start, %d exhausted, %d waiting on backoff",
			len(environments), counts.restarted, counts.failed, counts.exhausted, counts.waiting)
	}
	return counts.restarted, nil
}

// reconcileCounts is one tick's tally, so the summary log can distinguish work
// done from work deliberately skipped.
type reconcileCounts struct {
	restarted int
	failed    int
	exhausted int
	waiting   int
}

// reconcileEach walks one tick's environments, applying the attempt cap and the
// backoff before spending a claim on any of them. Split out of reconcile so
// that function stays a readable scan-then-report.
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

// firstLine trims a recorded blocker to its first line for a log message; the
// full text stays on the row for anyone who needs it.
func firstLine(s string) string {
	if s == "" {
		return "(none recorded)"
	}
	if i := strings.IndexByte(s, 0x0a); i >= 0 {
		return s[:i]
	}
	return s
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

	// Past this point the row is claimed and reads `deleting`, so every exit
	// must either start a workflow or record why it could not (#1166).
	tenant, ok := tenantsByID[environment.TenantID]
	if !ok {
		return false, r.unclaim(ctx, environment.EnvironmentID, fmt.Errorf("tenant %q not found", environment.TenantID))
	}
	placement, err := r.resolvePlacement(ctx, environment.ContextID)
	if err != nil {
		return false, r.unclaim(ctx, environment.EnvironmentID, fmt.Errorf("resolve placement: %w", err))
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
		return false, r.unclaim(ctx, environment.EnvironmentID, fmt.Errorf("start delete workflow: %w", err))
	}
	return true, nil
}

// unclaim records why a claimed row's attempt never started, moving it back out
// of `deleting` to `deletion-blocked` with a reason. Without this the reconciler
// leaves the row claiming an in-flight delete that does not exist -- strictly
// worse than not having ticked at all, and exactly the misreporting #1140 was
// about. Returns the original cause so the caller still logs it; a failure to
// record is folded in rather than replacing it.
func (r *EnvDeleteReconciler) unclaim(ctx context.Context, environmentID string, cause error) error {
	if err := r.environments.MarkDeleteBlocked(ctx, environmentID, cause.Error()); err != nil {
		return fmt.Errorf("%w (and recording it did not persist: %v)", cause, err)
	}
	return cause
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
