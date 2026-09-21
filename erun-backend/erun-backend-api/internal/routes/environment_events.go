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

// EnvironmentEventRepository is the persistence access EnvironmentEventRoutes
// needs: appending one reported event, and reading the log forward from a
// cursor.
type EnvironmentEventRepository interface {
	Append(ctx context.Context, event model.EnvironmentEvent) (model.EnvironmentEvent, error)
	Read(ctx context.Context, filter repository.EnvironmentEventFilter) (repository.EnvironmentEventPage, error)
}

type EnvironmentEventRoutes struct {
	events       EnvironmentEventRepository
	environments EnvironmentGetter
}

// RegisterEnvironmentEventRoutes wires the sequenced environment-event log:
// the append an environment's own reporter calls, and the cursor read a
// client resumes a stream from. The read is tenant-wide rather than
// per-environment because a resume position only means something relative to
// the stream it came from -- the events this phase cares about (a deploy
// finishing, an agent awaiting input) span a tenant's environments, so a
// per-environment cursor would force one stream per environment and would
// have no way to express "everything that happened while I was away".
func RegisterEnvironmentEventRoutes(register ProtectedRouteRegistrar, events EnvironmentEventRepository, environments EnvironmentGetter) {
	routes := EnvironmentEventRoutes{events: events, environments: environments}
	register(http.MethodPost, "/v1/environments/{environment_id}/events", http.HandlerFunc(routes.appendEnvironmentEvent))
	register(http.MethodGet, "/v1/events", http.HandlerFunc(routes.readEnvironmentEvents))
}

// validEnvironmentEventKinds mirrors the table's own CHECK constraint via the
// model's exported constants, so an unrecognized kind is refused with a named
// field error here rather than surfacing as an opaque constraint violation.
var validEnvironmentEventKinds = map[model.EnvironmentEventKind]bool{
	model.EnvironmentEventStatusChanged:        true,
	model.EnvironmentEventSessionAwaitingInput: true,
	model.EnvironmentEventJobFinished:          true,
}

type appendEnvironmentEventRequest struct {
	Kind model.EnvironmentEventKind `json:"kind"`
	// Detail is the event's payload, serialized by the reporter (compact JSON
	// for structured input). The log stores it uninterpreted.
	Detail string `json:"detail,omitempty"`
	// OccurredAt is when the reporter observed the event. Optional: omitting
	// it records receipt time instead, which is what a reporter that only
	// knows the event happened now has to offer.
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
	// NextCursor is the position to resume from, empty when this page reached
	// the end of the log as it stood.
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
