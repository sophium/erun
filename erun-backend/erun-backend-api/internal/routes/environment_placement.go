package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// resolvePlacementCoordinates reads back an already-placed environment's
// target-cluster coordinates: no capacity check, no auto-select. Placement is
// decided once, at create time (resolvePlacement); a redeploy, stop, or
// delete always targets the cluster the environment was already placed on.
// Empty contextID (the platform's own cluster) resolves to the zero
// resolvedPlacement with no repository read.
//
// tenantID owns the context being read back, and is an explicit argument
// rather than the caller's own tenant read out of ctx: a context read that
// named only an id would return whichever tenant's context that id happened
// to name, which erun_operations' unconditional RLS policy does nothing to
// prevent.
func (r EnvironmentRoutes) resolvePlacementCoordinates(ctx context.Context, tenantID, contextID string) (resolvedPlacement, error) {
	contextID = strings.TrimSpace(contextID)
	if contextID == "" {
		return resolvedPlacement{}, nil
	}
	cloudContext, err := r.contexts.Get(ctx, tenantID, contextID)
	if err != nil {
		return resolvedPlacement{}, err
	}
	return placementFromContext(cloudContext), nil
}

// placementOwnerTenant names the tenant a placement's coordinates and
// credential are resolved for: the environment row's own tenant_id, which is
// what owns the context it references. An environment that carries no row yet
// — the create path, where the database mints tenant_id from the scoped
// security context — falls back to the caller's resolved tenant, which is
// already the target tenant on that path (see
// resolveCreateEnvironmentTenantScope).
func placementOwnerTenant(environment model.Environment, securityContext security.Context) string {
	if owner := strings.TrimSpace(environment.TenantID); owner != "" {
		return owner
	}
	return securityContext.TenantID
}

// placementOwnerTenantFor resolves placementOwnerTenant from ctx, for the
// route handlers that hold an environment row and no separately-read security
// context.
func (r EnvironmentRoutes) placementOwnerTenantFor(ctx context.Context, environment model.Environment) string {
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return ""
	}
	return placementOwnerTenant(environment, securityContext)
}

// errCrossClusterPlacementUnsupported is the actionable refusal for a runtime
// environment that names a raw kubernetesContext string rather than a
// registered contextId: this platform's placement machinery (#1112) resolves
// a cluster's server URL and admin-token credential from a `contexts` row —
// a bare context name has neither, so honoring it would still silently
// deploy to the wrong place (or nowhere real). Register the cluster with
// POST /v1/contexts first, then reference it by contextId.
var errCrossClusterPlacementUnsupported = errors.New(
	"deploying into a raw kubernetesContext name is not supported: register the cluster with POST /v1/contexts and reference it by contextId, or leave both unset to place into the platform's own cluster",
)

// errPlacementContextNotFound is the request-time refusal for a contextId
// that does not resolve for the caller's tenant. PlacementContextRepository's
// reads are RLS-scoped, so a context belonging to another tenant already
// reads as not-found here — the same enforcement environments.context_id's
// composite FK gives Create, surfaced synchronously instead of as an insert
// error.
var errPlacementContextNotFound = errors.New("named context was not found for this tenant")

// placementCapacityError names a placement candidate that has no room, so a
// caller can tell "this context is full" (409, actionable: raise its
// capacity or pick another) apart from every other placement failure.
type placementCapacityError struct{ detail string }

func (e *placementCapacityError) Error() string { return e.detail }

// resolvedPlacement is the target cluster a runtime environment's deploy/
// stop/delete Job authenticates against. The zero value places into the
// platform's own cluster — the only option before #1112, and still the
// default for a tenant that has registered no context of its own.
type resolvedPlacement struct {
	ContextID         string
	KubernetesContext string
	ServerURL         string
}

// contextStatusRunning is the only status a placement candidate may be in;
// a context still provisioning or one that failed to bootstrap has no live
// cluster or custodied credential to place into.
const contextStatusRunning = "running"

// placementServerURL is the k3s API server a bootstrapped context answers on
// (eruncommon.configureCloudKubeContext targets every cloud context the same
// way).
func placementServerURL(publicIP string) string {
	return "https://" + strings.TrimSpace(publicIP) + ":6443"
}

func placementFromContext(cloudContext model.Context) resolvedPlacement {
	kubernetesContext := strings.TrimSpace(cloudContext.KubernetesContext)
	if kubernetesContext == "" {
		kubernetesContext = cloudContext.Name
	}
	return resolvedPlacement{
		ContextID:         cloudContext.ContextID,
		KubernetesContext: kubernetesContext,
		ServerURL:         placementServerURL(cloudContext.PublicIP),
	}
}

// resolvePlacement decides which cluster (if any) a runtime environment's
// deploy targets (#1112). Only runtime environments are ever server-side
// deployed, so a non-runtime environment's context/kubernetesContext are
// opaque references with no placement decision to make — passed through
// exactly as the caller sent them, unchanged from before #1112 existed. A
// runtime environment naming a raw kubernetesContext is refused outright
// (see errCrossClusterPlacementUnsupported); one naming a contextId
// validates it resolves for the caller's tenant and has room. A runtime
// environment naming neither auto-selects the tenant's own registered
// contexts, preserving the pre-#1112 default (the platform's own cluster)
// for the common case of a tenant that has registered none.
func (r EnvironmentRoutes) resolvePlacement(ctx context.Context, environment model.Environment) (resolvedPlacement, error) {
	if environment.Type != model.EnvironmentTypeRuntime {
		return resolvedPlacement{ContextID: strings.TrimSpace(environment.ContextID)}, nil
	}
	if strings.TrimSpace(environment.KubernetesContext) != "" {
		return resolvedPlacement{}, errCrossClusterPlacementUnsupported
	}
	contextID := strings.TrimSpace(environment.ContextID)
	if contextID == "" {
		return r.autoSelectPlacement(ctx)
	}
	// Name the context's owner explicitly: a context id on its own is not a
	// tenant boundary, because erun_operations' RLS policy is unconditional.
	cloudContext, err := r.contexts.Get(ctx, r.placementOwnerTenantFor(ctx, environment), contextID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return resolvedPlacement{}, errPlacementContextNotFound
		}
		return resolvedPlacement{}, err
	}
	if err := r.validatePlacementCapacity(ctx, cloudContext); err != nil {
		return resolvedPlacement{}, err
	}
	return placementFromContext(cloudContext), nil
}

// autoSelectPlacement picks the first of the tenant's own running contexts
// with room, in PlacementContextRepository.List's own deterministic order.
// A tenant with no registered contexts places into the platform's own
// cluster — a real, working placement, not a failure. A tenant that HAS
// registered contexts but none currently qualify (none running, or all at
// capacity) fails clearly instead of silently falling back, matching #1112's
// "or fail clearly (naming why) when none qualifies": once a tenant has
// opted into multi-cluster placement, a request that cannot honor it must
// say so rather than land somewhere the caller did not ask for.
func (r EnvironmentRoutes) autoSelectPlacement(ctx context.Context) (resolvedPlacement, error) {
	contexts, err := r.contexts.List(ctx)
	if err != nil {
		return resolvedPlacement{}, err
	}
	if len(contexts) == 0 {
		return resolvedPlacement{}, nil
	}
	running := 0
	for _, cloudContext := range contexts {
		if cloudContext.Status != contextStatusRunning {
			continue
		}
		running++
		if err := r.validatePlacementCapacity(ctx, cloudContext); err != nil {
			var capacityErr *placementCapacityError
			if errors.As(err, &capacityErr) {
				continue
			}
			return resolvedPlacement{}, err
		}
		return placementFromContext(cloudContext), nil
	}
	if running == 0 {
		return resolvedPlacement{}, &placementCapacityError{detail: fmt.Sprintf("no registered context is running yet; %d registered but still provisioning or failed", len(contexts))}
	}
	return resolvedPlacement{}, &placementCapacityError{detail: fmt.Sprintf("no registered context has room for a new environment: all %d running context(s) are at capacity", running)}
}

// validatePlacementCapacity fails clearly, naming the context and its
// ceiling, when a placement candidate has no room — the "or fail clearly"
// half of #1112's ask, rather than accepting the request and failing inside
// the Job.
func (r EnvironmentRoutes) validatePlacementCapacity(ctx context.Context, cloudContext model.Context) error {
	count, err := r.environments.CountByContext(ctx, cloudContext.ContextID)
	if err != nil {
		return err
	}
	if count >= cloudContext.MaxEnvironments {
		return &placementCapacityError{detail: fmt.Sprintf("context %q is at capacity: %d of %d environments already placed; raise its maxEnvironments or choose another context", cloudContext.Name, count, cloudContext.MaxEnvironments)}
	}
	return nil
}

// writePlacementError maps a resolvePlacement failure to its HTTP status: a
// caller mistake (unknown context, a raw kubernetesContext) is a 400; no
// candidate having room (an explicit context at capacity, or the whole
// auto-select inventory exhausted) is a 409, matching the existing
// quota-exceeded shape; anything else is a repository-layer failure.
func writePlacementError(w http.ResponseWriter, req *http.Request, err error) {
	var capacityErr *placementCapacityError
	switch {
	case errors.As(err, &capacityErr):
		writeError(w, http.StatusConflict, capacityErr.Error())
	case errors.Is(err, errPlacementContextNotFound), errors.Is(err, errCrossClusterPlacementUnsupported):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeRepositoryError(w, req, err)
	}
}
