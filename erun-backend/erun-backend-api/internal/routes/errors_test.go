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

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	original := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(original)
	fn()
	return buf.String()
}

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

func TestWriteErrorDetailsOmitsDetailsWhenNil(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErrorCode(rec, http.StatusBadRequest, "SOME_CODE", "message")

	if strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatalf("body = %s, want no details field when none was given", rec.Body.String())
	}
}
