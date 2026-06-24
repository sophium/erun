package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

func postCreateEnvironment(t *testing.T, environments *stubEnvironmentRepository, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	EnvironmentRoutes{environments: environments}.createEnvironment(rec, req)
	return rec
}

func TestCreateEnvironmentRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"missing name":   `{"type":"runtime"}`,
		"missing type":   `{"name":"prod"}`,
		"unknown type":   `{"name":"prod","type":"staging"}`,
		"empty body":     `{}`,
		"malformed json": `{`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			environments := &stubEnvironmentRepository{}
			rec := postCreateEnvironment(t, environments, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("unexpected status: %d", rec.Code)
			}
			if environments.createCalls != 0 {
				t.Fatalf("Create should not run on invalid input, got %d calls", environments.createCalls)
			}
		})
	}
}

func TestCreateEnvironmentPersistsAndReturnsRow(t *testing.T) {
	for _, envType := range []string{"runtime", "remote-agent", "local-agent"} {
		t.Run(envType, func(t *testing.T) {
			environments := &stubEnvironmentRepository{created: model.Environment{EnvironmentID: "env-1", Name: "prod", Type: model.EnvironmentType(envType)}}
			rec := postCreateEnvironment(t, environments, `{
				"name": "prod",
				"type": "`+envType+`",
				"contextId": "ctx-1",
				"kubernetesContext": "primary",
				"runtimeVersion": "1.2.3"
			}`)

			if rec.Code != http.StatusCreated {
				t.Fatalf("unexpected status: %d", rec.Code)
			}
			if environments.createCalls != 1 {
				t.Fatalf("expected exactly one Create call, got %d", environments.createCalls)
			}
			// The handler must thread the operator-authored fields into the
			// persisted model; tenant binding is left to RLS, not the body.
			if environments.createInput.ContextID != "ctx-1" || environments.createInput.RuntimeVersion != "1.2.3" {
				t.Fatalf("unexpected create input: %+v", environments.createInput)
			}

			var response model.Environment
			if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.EnvironmentID != "env-1" {
				t.Fatalf("unexpected persisted environment: %+v", response)
			}
		})
	}
}

func TestCreateEnvironmentSurfacesRepositoryError(t *testing.T) {
	// A context_id from another tenant violates the composite foreign key; the
	// repository error is surfaced as a clean HTTP error, not a SQL leak.
	environments := &stubEnvironmentRepository{err: errForeignKey{}}
	rec := postCreateEnvironment(t, environments, `{"name":"prod","type":"runtime","contextId":"ctx-other"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if environments.createCalls != 1 {
		t.Fatalf("expected exactly one Create call, got %d", environments.createCalls)
	}
}

// errForeignKey stands in for a database foreign-key-violation error returned by
// the repository when the referenced context is not the caller's.
type errForeignKey struct{}

func (errForeignKey) Error() string { return "foreign key violation" }
