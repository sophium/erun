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

type stubAuditEventRepository struct {
	filter repository.AuditEventFilter
	page   repository.AuditEventPage
	err    error
}

func (r *stubAuditEventRepository) List(_ context.Context, filter repository.AuditEventFilter) (repository.AuditEventPage, error) {
	r.filter = filter
	return r.page, r.err
}

func getAuditEvents(t *testing.T, routes AuditEventRoutes, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/audit-events"+query, nil)
	rec := httptest.NewRecorder()
	routes.listAuditEvents(rec, req)
	return rec
}

func TestListAuditEventsParsesEveryFilter(t *testing.T) {
	stub := &stubAuditEventRepository{}
	routes := AuditEventRoutes{events: stub}
	rec := getAuditEvents(t, routes,
		"?since=2026-01-01T00:00:00Z&until=2026-01-02T00:00:00Z&erunUserId=user-1"+
			"&type=API&apiMethod=GET&apiPath=%2Fv1%2Freviews&cursor=2026-01-01T00%3A00%3A00Z%2Ce1&limit=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if stub.filter.ErunUserID != "user-1" {
		t.Fatalf("erunUserId = %q", stub.filter.ErunUserID)
	}
	if stub.filter.Type != model.AuditEventTypeAPI {
		t.Fatalf("type = %q", stub.filter.Type)
	}
	if stub.filter.APIMethod != "GET" || stub.filter.APIPath != "/v1/reviews" {
		t.Fatalf("apiMethod/apiPath = %q/%q", stub.filter.APIMethod, stub.filter.APIPath)
	}
	if stub.filter.Limit != 10 {
		t.Fatalf("limit = %d, want 10", stub.filter.Limit)
	}
	if stub.filter.Cursor.AuditEventID != "e1" {
		t.Fatalf("cursor = %+v, want audit event id e1", stub.filter.Cursor)
	}
	if stub.filter.Since.IsZero() || stub.filter.Until.IsZero() {
		t.Fatalf("since/until not parsed: %+v", stub.filter)
	}
}

func TestListAuditEventsRejectsAnUnparsableCursor(t *testing.T) {
	stub := &stubAuditEventRepository{}
	routes := AuditEventRoutes{events: stub}
	rec := getAuditEvents(t, routes, "?cursor=not-a-cursor")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListAuditEventsRejectsAnUnparsableTimeRange(t *testing.T) {
	stub := &stubAuditEventRepository{}
	routes := AuditEventRoutes{events: stub}
	rec := getAuditEvents(t, routes, "?since=not-a-time")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestListAuditEventsResponseNeverSerializesParameterPayloads guards the
// response shape even if a future bug ever got a parameter payload as far as
// an in-memory model.AuditEvent: a tool such as cloud_inject_aws_credentials
// takes credentials as arguments, and the JSON the caller receives must not
// carry them regardless of how they got into the struct.
func TestListAuditEventsResponseNeverSerializesParameterPayloads(t *testing.T) {
	stub := &stubAuditEventRepository{page: repository.AuditEventPage{Events: []model.AuditEvent{{
		AuditEventID: "e1",
		Type:         model.AuditEventTypeMCP,
		MCPTool:      "cloud_inject_aws_credentials",
		// Set directly on the struct (bypassing the DB) to prove the JSON
		// encoding itself is the guard, not just "List never populates it".
		MCPToolParameters: `{"secretAccessKey":"super-secret"}`,
		CLIParameters:     `{"token":"also-secret"}`,
	}}}}
	routes := AuditEventRoutes{events: stub}
	rec := getAuditEvents(t, routes, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "secret") {
		t.Fatalf("response body leaked a parameter payload: %s", body)
	}

	var decoded auditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(decoded.Events))
	}
	if decoded.Events[0].MCPTool != "cloud_inject_aws_credentials" {
		t.Fatalf("mcpTool = %q, want the tool name preserved", decoded.Events[0].MCPTool)
	}
}
