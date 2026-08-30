package routes

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// AuditEventRepository lists the caller's tenant audit trail. Read-only: audit
// events are written by authentication middleware for every authorized
// request, never by a route.
type AuditEventRepository interface {
	List(ctx context.Context, filter repository.AuditEventFilter) (repository.AuditEventPage, error)
}

type AuditEventRoutes struct {
	events AuditEventRepository
}

func RegisterAuditEventRoutes(register ProtectedRouteRegistrar, events AuditEventRepository) {
	routes := AuditEventRoutes{events: events}
	register(http.MethodGet, "/v1/audit-events", http.HandlerFunc(routes.listAuditEvents))
}

type auditEventsResponse struct {
	Events     []model.AuditEvent `json:"events"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

func (r AuditEventRoutes) listAuditEvents(w http.ResponseWriter, req *http.Request) {
	filter, err := parseAuditEventFilter(req.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := r.events.List(req.Context(), filter)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, auditEventsResponse{Events: page.Events, NextCursor: page.NextCursor})
}

func parseAuditEventFilter(query url.Values) (repository.AuditEventFilter, error) {
	get := func(key string) string { return strings.TrimSpace(query.Get(key)) }

	filter := repository.AuditEventFilter{
		ErunUserID: get("erunUserId"),
		Type:       model.AuditEventType(get("type")),
		APIMethod:  get("apiMethod"),
		APIPath:    get("apiPath"),
	}

	var err error
	if filter.Since, err = parseAuditEventTime(get("since")); err != nil {
		return repository.AuditEventFilter{}, err
	}
	if filter.Until, err = parseAuditEventTime(get("until")); err != nil {
		return repository.AuditEventFilter{}, err
	}
	if filter.Cursor, err = repository.ParseAuditEventCursor(get("cursor")); err != nil {
		return repository.AuditEventFilter{}, err
	}
	if limit := get("limit"); limit != "" {
		parsed, err := strconv.Atoi(limit)
		if err != nil {
			return repository.AuditEventFilter{}, repository.ErrInvalidInput
		}
		filter.Limit = parsed
	}
	return filter, nil
}

func parseAuditEventTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, repository.ErrInvalidInput
	}
	return parsed, nil
}
