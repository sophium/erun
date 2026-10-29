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

// EnvironmentEventRepository appends events and reads the log from a cursor.
type EnvironmentEventRepository interface {
	Append(ctx context.Context, event model.EnvironmentEvent) (model.EnvironmentEvent, error)
	Read(ctx context.Context, filter repository.EnvironmentEventFilter) (repository.EnvironmentEventPage, error)
}

type EnvironmentEventRoutes struct {
	events       EnvironmentEventRepository
	environments EnvironmentGetter
}

// RegisterEnvironmentEventRoutes wires the sequenced environment-event log. The
// read is tenant-wide: a resume cursor must cover every environment's events.
func RegisterEnvironmentEventRoutes(register ProtectedRouteRegistrar, events EnvironmentEventRepository, environments EnvironmentGetter) {
	routes := EnvironmentEventRoutes{events: events, environments: environments}
	register(http.MethodPost, "/v1/environments/{environment_id}/events", http.HandlerFunc(routes.appendEnvironmentEvent))
	register(http.MethodGet, "/v1/events", http.HandlerFunc(routes.readEnvironmentEvents))
}

// validEnvironmentEventKinds mirrors the table's CHECK constraint so an unknown
// kind gets a named field error.
var validEnvironmentEventKinds = map[model.EnvironmentEventKind]bool{
	model.EnvironmentEventStatusChanged:        true,
	model.EnvironmentEventSessionAwaitingInput: true,
	model.EnvironmentEventJobFinished:          true,
}

type appendEnvironmentEventRequest struct {
	Kind model.EnvironmentEventKind `json:"kind"`
	// Detail is stored uninterpreted.
	Detail string `json:"detail,omitempty"`
	// OccurredAt defaults to receipt time.
	OccurredAt time.Time `json:"occurredAt,omitempty"`
}

func (r EnvironmentEventRoutes) appendEnvironmentEvent(w http.ResponseWriter, req *http.Request) {
	environmentID := req.PathValue("environment_id")
	if _, err := r.environments.Get(req.Context(), environmentID); err != nil {
		writeRepositoryError(w, req, err)
		return
	}

	var body appendEnvironmentEventRequest
	if err := decodeJSON(req, &body); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	body.Kind = model.EnvironmentEventKind(strings.TrimSpace(string(body.Kind)))
	if !validEnvironmentEventKinds[body.Kind] {
		writeErrorDetails(w, http.StatusBadRequest, "INVALID_BODY", "unknown environment event kind", map[string]any{"field": "kind"})
		return
	}

	event, err := r.events.Append(req.Context(), model.EnvironmentEvent{
		EnvironmentID: environmentID,
		Kind:          body.Kind,
		Detail:        body.Detail,
		OccurredAt:    body.OccurredAt,
	})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, event)
}

type environmentEventsResponse struct {
	Events []model.EnvironmentEvent `json:"events"`
	// NextCursor is empty when the page reached the end of the log.
	NextCursor string `json:"nextCursor,omitempty"`
}

func (r EnvironmentEventRoutes) readEnvironmentEvents(w http.ResponseWriter, req *http.Request) {
	filter, err := parseEnvironmentEventFilter(req.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := r.events.Read(req.Context(), filter)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	if page.Events == nil {
		page.Events = []model.EnvironmentEvent{}
	}
	writeJSON(w, http.StatusOK, environmentEventsResponse{Events: page.Events, NextCursor: page.NextCursor})
}

func parseEnvironmentEventFilter(query url.Values) (repository.EnvironmentEventFilter, error) {
	get := func(key string) string { return strings.TrimSpace(query.Get(key)) }

	filter := repository.EnvironmentEventFilter{EnvironmentID: get("environmentId")}
	cursor, err := repository.ParseEnvironmentEventCursor(get("cursor"))
	if err != nil {
		return repository.EnvironmentEventFilter{}, err
	}
	filter.Cursor = cursor
	if limit := get("limit"); limit != "" {
		parsed, err := strconv.Atoi(limit)
		if err != nil {
			return repository.EnvironmentEventFilter{}, repository.ErrInvalidInput
		}
		filter.Limit = parsed
	}
	return filter, nil
}
