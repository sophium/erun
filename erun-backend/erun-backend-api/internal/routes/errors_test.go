package routes

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

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
	for _, c := range cases {
		rec := httptest.NewRecorder()
		writeRepositoryError(rec, c.err)

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

// TestWriteErrorDetailsOmitsDetailsWhenNil: details is documented as present
// only "where useful" — a nil details must not render as a literal null.
func TestWriteErrorDetailsOmitsDetailsWhenNil(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErrorCode(rec, http.StatusBadRequest, "SOME_CODE", "message")

	if strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatalf("body = %s, want no details field when none was given", rec.Body.String())
	}
}
