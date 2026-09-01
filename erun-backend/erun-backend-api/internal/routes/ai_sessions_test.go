package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type stubAISessionRepository struct {
	recorded []model.AISessionEvent
	result   model.AISessionEvent
	err      error
}

func (s *stubAISessionRepository) Record(_ context.Context, event model.AISessionEvent) (model.AISessionEvent, error) {
	s.recorded = append(s.recorded, event)
	if s.err != nil {
		return model.AISessionEvent{}, s.err
	}
	if s.result.SessionID == "" {
		return event, nil
	}
	return s.result, nil
}

type stubEnvironmentGetter struct {
	environment model.Environment
	err         error
}

func (s stubEnvironmentGetter) Get(context.Context, string) (model.Environment, error) {
	return s.environment, s.err
}

func postAISessionEvent(t *testing.T, sessions *stubAISessionRepository, environments EnvironmentGetter, environmentID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/"+environmentID+"/ai-sessions", bytes.NewBufferString(body))
	req.SetPathValue("environment_id", environmentID)
	rec := httptest.NewRecorder()
	AISessionRoutes{sessions: sessions, environments: environments}.reportAISessionEvent(rec, req)
	return rec
}

// TestReportAISessionEventRefusesAnEnvironmentTheCallerCannotSee is the
// refusal proof this route needs: an environment id that does not resolve
// for the caller's tenant (repository.Get is RLS-scoped, so a cross-tenant id
// reads as not-found exactly like every other environment sub-route) must be
// refused before any session data is ever written, not merely rendered
// differently to the caller.
func TestReportAISessionEventRefusesAnEnvironmentTheCallerCannotSee(t *testing.T) {
	sessions := &stubAISessionRepository{}
	environments := stubEnvironmentGetter{err: repository.ErrNotFound}
	rec := postAISessionEvent(t, sessions, environments, "env-1", `{"sessionId":"ai","event":"turn-start"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if len(sessions.recorded) != 0 {
		t.Fatalf("Record should not run when the environment is refused, got %d calls", len(sessions.recorded))
	}
}

func TestReportAISessionEventRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"missing sessionId": `{"event":"turn-start"}`,
		"missing event":     `{"sessionId":"ai"}`,
		"unknown event":     `{"sessionId":"ai","event":"bogus"}`,
		"malformed json":    `{`,
		"empty body":        `{}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			sessions := &stubAISessionRepository{}
			environments := stubEnvironmentGetter{environment: model.Environment{EnvironmentID: "env-1"}}
			rec := postAISessionEvent(t, sessions, environments, "env-1", body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if len(sessions.recorded) != 0 {
				t.Fatalf("Record should not run on invalid input, got %d calls", len(sessions.recorded))
			}
		})
	}
}

// TestReportAISessionEventCarriesForwardTheEnvironmentID confirms the
// environment id comes from the path, never trusted from the body, mirroring
// every other environment sub-route.
func TestReportAISessionEventCarriesForwardTheEnvironmentID(t *testing.T) {
	sessions := &stubAISessionRepository{}
	environments := stubEnvironmentGetter{environment: model.Environment{EnvironmentID: "env-1"}}
	rec := postAISessionEvent(t, sessions, environments, "env-1", `{"sessionId":"ai","tool":"claude","event":"turn-start"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(sessions.recorded) != 1 {
		t.Fatalf("Record calls = %d, want 1", len(sessions.recorded))
	}
	got := sessions.recorded[0]
	if got.EnvironmentID != "env-1" || got.SessionID != "ai" || got.Tool != "claude" || got.Event != "turn-start" {
		t.Fatalf("recorded event = %+v, unexpected", got)
	}
}

// TestReportAISessionEventResolvesTheSameStatesLocalCallersSee proves the
// response uses eruncommon's own resolution (never a second, drifting
// implementation of busy/idle/awaiting-input): a turn-end event resolves to
// awaiting-input, and an exit event resolves to exited, exactly as
// eruncommon.ResolveAISessionStatus documents.
func TestReportAISessionEventResolvesTheSameStatesLocalCallersSee(t *testing.T) {
	cases := map[string]struct {
		body  string
		state string
	}{
		"turn-end resolves to awaiting-input": {`{"sessionId":"ai","event":"turn-end"}`, "awaiting-input"},
		"turn-start resolves to busy":         {`{"sessionId":"ai","event":"turn-start"}`, "busy"},
		"exit resolves to exited":             {`{"sessionId":"ai","event":"exit"}`, "exited"},
	}
	environments := stubEnvironmentGetter{environment: model.Environment{EnvironmentID: "env-1"}}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			sessions := &stubAISessionRepository{}
			rec := postAISessionEvent(t, sessions, environments, "env-1", tc.body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"state":"`+tc.state+`"`) {
				t.Fatalf("body = %q, want state %q", rec.Body.String(), tc.state)
			}
		})
	}
}

func TestReportAISessionEventReportsRepositoryFailures(t *testing.T) {
	sessions := &stubAISessionRepository{err: repository.ErrConflict}
	environments := stubEnvironmentGetter{environment: model.Environment{EnvironmentID: "env-1"}}
	rec := postAISessionEvent(t, sessions, environments, "env-1", `{"sessionId":"ai","event":"turn-start"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}
