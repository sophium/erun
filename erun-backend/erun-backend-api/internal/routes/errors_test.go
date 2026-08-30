package routes

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// captureLog redirects the standard logger for the duration of fn and
// returns what it wrote, so a test can assert on the one place an operator
// would see why a request 500'd (erun#1722).
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	original := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(original)
	fn()
	return buf.String()
}

// TestWriteErrorAlwaysCarriesACode is the property the whole envelope exists
// for: a client branching on `code` must never see it absent, even when the
// call site supplies only a status and a message.
func TestWriteErrorAlwaysCarriesACode(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusTeapot, "I am a teapot")

	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Code == "" {
		t.Fatalf("code is empty, want a status-derived default")
	}
	if body.Code != "I_M_A_TEAPOT" {
		t.Fatalf("code = %q, want I_M_A_TEAPOT", body.Code)
	}
	if body.Message != "I am a teapot" {
		t.Fatalf("message = %q, want the original message preserved", body.Message)
	}
}

// TestWriteRepositoryErrorMapsEverySentinelToACode covers the generic path
// every route without a business-specific code falls through to.
func TestWriteRepositoryErrorMapsEverySentinelToACode(t *testing.T) {
	cases := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{repository.ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{repository.ErrForbidden, http.StatusForbidden, "FORBIDDEN"},
		{repository.ErrInvalidInput, http.StatusBadRequest, "BAD_REQUEST"},
		{repository.ErrMissingSecurityContext, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR"},
		{repository.ErrConflict, http.StatusConflict, "CONFLICT"},
		{errors.New("unrecognized"), http.StatusInternalServerError, "INTERNAL_SERVER_ERROR"},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)
	for _, c := range cases {
		rec := httptest.NewRecorder()
		writeRepositoryError(rec, req, c.err)

		if rec.Code != c.wantStatus {
			t.Fatalf("err=%v: status = %d, want %d", c.err, rec.Code, c.wantStatus)
		}
		var body errorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("err=%v: response body is not JSON: %v (%s)", c.err, err, rec.Body.String())
		}
		if body.Code != c.wantCode {
			t.Fatalf("err=%v: code = %q, want %q", c.err, body.Code, c.wantCode)
		}
	}
}

// TestWriteRepositoryErrorLogsRouteActorAndErrorOn500 is erun#1722's
// observability requirement: a 500 on an authenticated route must leave a
// server-side trace naming the route, the actor, and the real underlying
// error — none of which reaches the client-facing response body.
func TestWriteRepositoryErrorLogsRouteActorAndErrorOn500(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/invite-requests/req-1/approve", nil)
	ctx := security.WithContext(req.Context(), security.Context{TenantID: "tenant-ops", ErunUserID: "user-42"})
	req = req.WithContext(ctx)
	underlying := errors.New("tenants_name_key already claimed by a different row")

	output := captureLog(t, func() {
		rec := httptest.NewRecorder()
		writeRepositoryError(rec, req, underlying)
	})

	for _, want := range []string{"POST", "/v1/invite-requests/req-1/approve", "tenant-ops", "user-42", underlying.Error()} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output = %q, want it to contain %q", output, want)
		}
	}
}

// TestWriteRepositoryErrorDoesNotLogClientFaults keeps the log free of
// routine 4xx noise: only a 500 needs an operator's attention.
func TestWriteRepositoryErrorDoesNotLogClientFaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)

	output := captureLog(t, func() {
		rec := httptest.NewRecorder()
		writeRepositoryError(rec, req, repository.ErrNotFound)
	})

	if output != "" {
		t.Fatalf("log output = %q, want nothing logged for a client-fault 404", output)
	}
}

// TestWriteErrorDetailsOmitsDetailsWhenNil: details is documented as present
// only "where useful" — a nil details must not render as a literal null.
func TestWriteErrorDetailsOmitsDetailsWhenNil(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErrorCode(rec, http.StatusBadRequest, "SOME_CODE", "message")

	if strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatalf("body = %s, want no details field when none was given", rec.Body.String())
	}
}
