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

// resolvePlacementCoordinates reads back an environment's create-time placement,
// with no capacity check. tenantID is explicit because erun_operations' RLS on
// contexts is unconditional.
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

// placementOwnerTenant is the row's tenant, or the caller's resolved (already
// target) tenant on create, where the row has none yet.
func placementOwnerTenant(environment model.Environment, securityContext security.Context) string {
	if owner := strings.TrimSpace(environment.TenantID); owner != "" {
		return owner
	}
	return securityContext.TenantID
}

func (r EnvironmentRoutes) placementOwnerTenantFor(ctx context.Context, environment model.Environment) string {
	securityContext, ok := security.FromContext(ctx)
	if !ok {
		return ""
	}
	return placementOwnerTenant(environment, securityContext)
}

// errCrossClusterPlacementUnsupported refuses a raw kubernetesContext: only a
// registered context has the server URL and credential a deploy needs.
var errCrossClusterPlacementUnsupported = errors.New(
	"deploying into a raw kubernetesContext name is not supported: register the cluster with POST /v1/contexts and reference it by contextId, or leave both unset to place into the platform's own cluster",
)

// errPlacementContextNotFound covers other tenants' contexts too, since reads are RLS-scoped.
var errPlacementContextNotFound = errors.New("named context was not found for this tenant")

// placementCapacityError marks a full candidate (409).
type placementCapacityError struct{ detail string }

func (e *placementCapacityError) Error() string { return e.detail }

// resolvedPlacement's zero value is the platform's own cluster.
type resolvedPlacement struct {
	ContextID         string
	KubernetesContext string
	ServerURL         string
}

const contextStatusRunning = "running"

// placementServerURL is the k3s API server every bootstrapped context answers on.
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

// resolvePlacement places runtime environments only; other types pass their
// context through untouched.
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

// autoSelectPlacement falls back to the platform cluster only when the tenant has
// no registered contexts; once it has some, exhausting them is an error.
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

// writePlacementError maps caller mistakes to 400 and no-room to 409.
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
