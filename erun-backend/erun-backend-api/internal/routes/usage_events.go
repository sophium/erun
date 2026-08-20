package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// UsageEventRepository lists the caller's tenant's metering events. Read-only:
// events are recorded by the provisioning/lifecycle workflows, never by a route.
type UsageEventRepository interface {
	List(ctx context.Context) ([]model.UsageEvent, error)
}

type UsageEventRoutes struct {
	events UsageEventRepository
}

func RegisterUsageEventRoutes(register ProtectedRouteRegistrar, events UsageEventRepository) {
	routes := UsageEventRoutes{events: events}
	register(http.MethodGet, "/v1/usage-events", http.HandlerFunc(routes.listUsageEvents))
}

func (r UsageEventRoutes) listUsageEvents(w http.ResponseWriter, req *http.Request) {
	events, err := r.events.List(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}
