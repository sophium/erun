package routes

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

// AISessionRepository persists and lists environments' self-reported AI sessions.
type AISessionRepository interface {
	Record(ctx context.Context, event model.AISessionEvent) (model.AISessionEvent, error)
	List(ctx context.Context, environmentID string) ([]model.AISessionEvent, error)
}

// EnvironmentGetter confirms an environment belongs to the caller's tenant (RLS-scoped).
type EnvironmentGetter interface {
	Get(ctx context.Context, environmentID string) (model.Environment, error)
}

type AISessionRoutes struct {
	sessions     AISessionRepository
	environments EnvironmentGetter
}

// RegisterAISessionRoutes wires the AI-session self-report and its read-back.
func RegisterAISessionRoutes(register ProtectedRouteRegistrar, sessions AISessionRepository, environments EnvironmentGetter) {
	routes := AISessionRoutes{sessions: sessions, environments: environments}
	register(http.MethodPost, "/v1/environments/{environment_id}/ai-sessions", http.HandlerFunc(routes.reportAISessionEvent))
	register(http.MethodGet, "/v1/environments/{environment_id}/ai-sessions", http.HandlerFunc(routes.listAISessions))
}

// The server stamps occurred_at; a caller's clock is not trusted for lastActivity.
type reportAISessionEventRequest struct {
	SessionID  string `json:"sessionId"`
	Tool       string `json:"tool"`
	Event      string `json:"event"`
	ExitCode   *int   `json:"exitCode"`
	ExitReason string `json:"exitReason"`
}

// Unknown events are refused rather than resolving to Idle.
var validAISessionEvents = map[string]bool{
	string(modelpkg.AISessionEventTurnStart): true,
	string(modelpkg.AISessionEventToolUse):   true,
	string(modelpkg.AISessionEventTurnEnd):   true,
	string(modelpkg.AISessionEventNotify):    true,
	string(modelpkg.AISessionEventExit):      true,
}

func (r AISessionRoutes) reportAISessionEvent(w http.ResponseWriter, req *http.Request) {
	environmentID := req.PathValue("environment_id")
	if _, err := r.environments.Get(req.Context(), environmentID); err != nil {
		writeRepositoryError(w, req, err)
		return
	}

	var body reportAISessionEventRequest
	if err := decodeJSON(req, &body); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	sessionID := strings.TrimSpace(body.SessionID)
	if sessionID == "" {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", "sessionId is required")
		return
	}
	event := strings.TrimSpace(body.Event)
	if !validAISessionEvents[event] {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", fmt.Sprintf("event must be one of turn-start, tool-use, turn-end, notify, exit; got %q", event))
		return
	}

	recorded, err := r.sessions.Record(req.Context(), model.AISessionEvent{
		EnvironmentID: environmentID,
		SessionID:     sessionID,
		Tool:          strings.TrimSpace(body.Tool),
		Event:         event,
		ExitCode:      body.ExitCode,
		ExitReason:    strings.TrimSpace(body.ExitReason),
	})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, resolveAISessionStatus(recorded))
}

func (r AISessionRoutes) listAISessions(w http.ResponseWriter, req *http.Request) {
	environmentID := req.PathValue("environment_id")
	if _, err := r.environments.Get(req.Context(), environmentID); err != nil {
		writeRepositoryError(w, req, err)
		return
	}

	events, err := r.sessions.List(req.Context(), environmentID)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	statuses := make([]modelpkg.AISessionStatus, len(events))
	for i, event := range events {
		statuses[i] = resolveAISessionStatus(event)
	}
	writeJSON(w, http.StatusOK, statuses)
}

// resolveAISessionStatus reuses erun-common's resolution so callers see the
// same states as the desktop and MCP.
func resolveAISessionStatus(event model.AISessionEvent) modelpkg.AISessionStatus {
	return modelpkg.ResolveAISessionStatus(modelpkg.AISessionRecord{
		SessionID:  event.SessionID,
		Tool:       event.Tool,
		Event:      modelpkg.AISessionEventKind(event.Event),
		At:         event.OccurredAt,
		ExitCode:   event.ExitCode,
		ExitReason: event.ExitReason,
	})
}
