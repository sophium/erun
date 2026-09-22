package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type stubEnvironmentEventRepository struct {
	appended model.EnvironmentEvent
	filter   repository.EnvironmentEventFilter
	page     repository.EnvironmentEventPage
	err      error
}

func (r *stubEnvironmentEventRepository) Append(_ context.Context, event model.EnvironmentEvent) (model.EnvironmentEvent, error) {
	r.appended = event
	if r.err != nil {
		return model.EnvironmentEvent{}, r.err
	}
	event.EnvironmentEventID = "event-1"
	event.EnvironmentEventSeq = 7
	return event, nil
}

func (r *stubEnvironmentEventRepository) Read(_ context.Context, filter repository.EnvironmentEventFilter) (repository.EnvironmentEventPage, error) {
	r.filter = filter
	return r.page, r.err
}

func getEnvironmentEvents(t *testing.T, route EnvironmentEventRoutes, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/events"+query, nil)
	rec := httptest.NewRecorder()
	route.readEnvironmentEvents(rec, req)
	return rec
}

func postEnvironmentEvent(t *testing.T, route EnvironmentEventRoutes, environmentID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/"+environmentID+"/events", strings.NewReader(body))
	req.SetPathValue("environment_id", environmentID)
	rec := httptest.NewRecorder()
	route.appendEnvironmentEvent(rec, req)
	return rec
}

func TestReadEnvironmentEventsParsesTheCursorAndFilter(t *testing.T) {
	stub := &stubEnvironmentEventRepository{}
	route := EnvironmentEventRoutes{events: stub}
	rec := getEnvironmentEvents(t, route, "?cursor=42&environmentId=env-1&limit=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if stub.filter.Cursor.Seq != 42 {
		t.Fatalf("cursor = %+v, want seq 42", stub.filter.Cursor)
	}
	if stub.filter.EnvironmentID != "env-1" {
		t.Fatalf("environmentId = %q, want env-1", stub.filter.EnvironmentID)
	}
	if stub.filter.Limit != 5 {
		t.Fatalf("limit = %d, want 5", stub.filter.Limit)
	}
}

// TestReadEnvironmentEventsRefusesAnUnparsableCursor is the guard that a
// resume position is never silently reinterpreted. Treating `cursor` as
// optional would parse a junk token as the zero cursor and answer with the
// whole log from the beginning -- replaying every event to a client that
// asked only for what it missed, which is the one thing a resume cursor
// exists to prevent.
func TestReadEnvironmentEventsRefusesAnUnparsableCursor(t *testing.T) {
	for _, cursor := range []string{"not-a-cursor", "1.5", "-1", "0x10"} {
		stub := &stubEnvironmentEventRepository{}
		route := EnvironmentEventRoutes{events: stub}
		rec := getEnvironmentEvents(t, route, "?cursor="+cursor)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("cursor %q: status = %d, want 400", cursor, rec.Code)
		}
		if stub.filter.Cursor.Seq != 0 || stub.filter.EnvironmentID != "" {
			t.Fatalf("cursor %q: the repository was read anyway: %+v", cursor, stub.filter)
		}
	}
}

func TestReadEnvironmentEventsRefusesAnUnparsableLimit(t *testing.T) {
	route := EnvironmentEventRoutes{events: &stubEnvironmentEventRepository{}}
	rec := getEnvironmentEvents(t, route, "?limit=many")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestReadEnvironmentEventsReportsAnEmptyPageAsAnEmptyArray guards the
// client-side contract: a stream with nothing new to hand back must render as
// an empty list, not a missing field a client would have to guess at.
func TestReadEnvironmentEventsReportsAnEmptyPageAsAnEmptyArray(t *testing.T) {
	route := EnvironmentEventRoutes{events: &stubEnvironmentEventRepository{}}
	rec := getEnvironmentEvents(t, route, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Events     []model.EnvironmentEvent `json:"events"`
		NextCursor string                   `json:"nextCursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body %q)", err, rec.Body.String())
	}
	if body.Events == nil {
		t.Fatalf("events = null, want []")
	}
	if body.NextCursor != "" {
		t.Fatalf("nextCursor = %q, want empty", body.NextCursor)
	}
}

func TestAppendEnvironmentEventRejectsAnUnknownKind(t *testing.T) {
	stub := &stubEnvironmentEventRepository{}
	route := EnvironmentEventRoutes{events: stub, environments: stubEnvironmentGetter{environment: model.Environment{EnvironmentID: "env-1"}}}
	rec := postEnvironmentEvent(t, route, "env-1", `{"kind":"something-new"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
	if stub.appended.Kind != "" {
		t.Fatalf("the repository was written anyway: %+v", stub.appended)
	}
}

func TestAppendEnvironmentEventRefusesAnEnvironmentTheCallerCannotSee(t *testing.T) {
	stub := &stubEnvironmentEventRepository{}
	route := EnvironmentEventRoutes{events: stub, environments: stubEnvironmentGetter{err: repository.ErrNotFound}}
	rec := postEnvironmentEvent(t, route, "env-1", `{"kind":"job-finished"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %q)", rec.Code, rec.Body.String())
	}
	if stub.appended.Kind != "" {
		t.Fatalf("the repository was written anyway: %+v", stub.appended)
	}
}

func TestAppendEnvironmentEventRecordsTheReportedEvent(t *testing.T) {
	stub := &stubEnvironmentEventRepository{}
	route := EnvironmentEventRoutes{events: stub, environments: stubEnvironmentGetter{environment: model.Environment{EnvironmentID: "env-1"}}}
	rec := postEnvironmentEvent(t, route, "env-1", `{"kind":"session-awaiting-input","detail":"{\"sessionId\":\"s1\"}"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %q)", rec.Code, rec.Body.String())
	}
	if stub.appended.EnvironmentID != "env-1" || stub.appended.Kind != model.EnvironmentEventSessionAwaitingInput {
		t.Fatalf("appended = %+v", stub.appended)
	}
	if stub.appended.Detail != `{"sessionId":"s1"}` {
		t.Fatalf("detail = %q, want it stored uninterpreted", stub.appended.Detail)
	}
	// The position is the database's to assign: a request body has no way to
	// claim one.
	if stub.appended.EnvironmentEventSeq != 0 {
		t.Fatalf("seq = %d, want the caller unable to set it", stub.appended.EnvironmentEventSeq)
	}
}
