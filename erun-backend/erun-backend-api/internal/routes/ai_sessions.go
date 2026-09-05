package routes

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// AISessionRepository is the persistence access AISessionRoutes needs:
// upserting the environment's own self-reported event, and listing every
// session last reported for one environment.
type AISessionRepository interface {
	Record(ctx context.Context, event model.AISessionEvent) (model.AISessionEvent, error)
	List(ctx context.Context, environmentID string) ([]model.AISessionEvent, error)
}

// EnvironmentGetter is the narrow read access AISessionRoutes needs to
// confirm an environment id names one that belongs to the caller's tenant
// (RLS-scoped) before accepting a report against it — the same check
// deploy/stop already run before acting on a path-supplied environment id.
type EnvironmentGetter interface {
	Get(ctx context.Context, environmentID string) (model.Environment, error)
}

type AISessionRoutes struct {
	sessions     AISessionRepository
	environments EnvironmentGetter
}

// RegisterAISessionRoutes wires the environment's own AI-session self-report
// and its read-back: the structured busy/idle/awaiting-input status model
// erun-common already resolves locally for the desktop and per-env MCP (see
// erun-common/ai_session_status.go), now reportable and readable over the
// authenticated edge so a caller with no local kubeconfig/port-forward can
// see it too.
func RegisterAISessionRoutes(register ProtectedRouteRegistrar, sessions AISessionRepository, environments EnvironmentGetter) {
	routes := AISessionRoutes{sessions: sessions, environments: environments}
	register(http.MethodPost, "/v1/environments/{environment_id}/ai-sessions", http.HandlerFunc(routes.reportAISessionEvent))
	register(http.MethodGet, "/v1/environments/{environment_id}/ai-sessions", http.HandlerFunc(routes.listAISessions))
}

// reportAISessionEventRequest carries only what the tool's own hook actually
// observed. There is no client-supplied timestamp: the server stamps its own
// receipt time (occurred_at = NOW() in the repository), since trusting a
// caller's clock for the field ResolveAISessionStatus surfaces as
// "lastActivity" would let a stale or skewed report read as current.
type reportAISessionEventRequest struct {
	SessionID  string `json:"sessionId"`
	Tool       string `json:"tool"`
	Event      string `json:"event"`
	ExitCode   *int   `json:"exitCode"`
	ExitReason string `json:"exitReason"`
}

// validAISessionEvents mirrors erun-common's unexported validAISessionEventKind
// set (erun-common/ai_session_status.go) via its exported constants, so an
// unrecognized event is refused here rather than silently resolving to Idle —
// the "unknown must not render as a definite value" property
// eruncommon.ResolveAISessionStatus's own default case protects locally.
var validAISessionEvents = map[string]bool{
	string(eruncommon.AISessionEventTurnStart): true,
	string(eruncommon.AISessionEventToolUse):   true,
	string(eruncommon.AISessionEventTurnEnd):   true,
	string(eruncommon.AISessionEventNotify):    true,
	string(eruncommon.AISessionEventExit):      true,
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

// listAISessions returns the resolved status of every session last reported
// for this environment, sorted by session id (AISessionRepository.List's own
// order) — the read-back half of reportAISessionEvent, for a caller with no
// local kubeconfig/port-forward to poll instead.
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
	statuses := make([]eruncommon.AISessionStatus, len(events))
	for i, event := range events {
		statuses[i] = resolveAISessionStatus(event)
	}
	writeJSON(w, http.StatusOK, statuses)
}

// resolveAISessionStatus converts the persisted row to the same
// eruncommon.AISessionStatus shape LoadAISessionStatuses resolves locally, so
// a caller reading this contract (once a read route exists) sees exactly the
// desktop/MCP view, never a second, drifting resolution of the same states.
func resolveAISessionStatus(event model.AISessionEvent) eruncommon.AISessionStatus {
	return eruncommon.ResolveAISessionStatus(eruncommon.AISessionRecord{
		SessionID:  event.SessionID,
		Tool:       event.Tool,
		Event:      eruncommon.AISessionEventKind(event.Event),
		At:         event.OccurredAt,
		ExitCode:   event.ExitCode,
		ExitReason: event.ExitReason,
	})
}
