package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

func postCreateContext(t *testing.T, contexts ContextRepository, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/contexts", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	ContextRoutes{contexts: contexts}.createContext(rec, req)
	return rec
}

func TestCreateContextRejectsMissingRequiredFields(t *testing.T) {
	cases := map[string]string{
		"missing name":   `{"cloudProviderAlias":"aws-acme","region":"eu-west-2"}`,
		"missing alias":  `{"name":"primary","region":"eu-west-2"}`,
		"missing region": `{"name":"primary","cloudProviderAlias":"aws-acme"}`,
		"empty body":     `{}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			contexts := &stubContextRepository{}
			rec := postCreateContext(t, contexts, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("unexpected status: %d", rec.Code)
			}
			if contexts.createCalls != 0 {
				t.Fatalf("Create should not run on invalid input, got %d calls", contexts.createCalls)
			}
		})
	}
}

func TestCreateContextPreviewReturnsPlanWithoutPersisting(t *testing.T) {
	contexts := &stubContextRepository{}
	rec := postCreateContext(t, contexts, `{
		"name": "primary",
		"cloudProviderAlias": "aws-acme",
		"region": "eu-west-2",
		"preview": true
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if contexts.createCalls != 0 {
		t.Fatalf("preview must not call Create, got %d calls", contexts.createCalls)
	}

	var response createContextResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Context != nil {
		t.Fatalf("preview response must omit the context, got %+v", response.Context)
	}
	if len(response.Plan) == 0 {
		t.Fatalf("preview must return a non-empty bootstrap plan")
	}
	// The plan is the InitCloudContext dry-run trace; it must include the EC2
	// run-instances command the real bootstrap would issue.
	if !planContains(response.Plan, "ec2 run-instances") {
		t.Fatalf("plan missing the EC2 run-instances step: %v", response.Plan)
	}
}

func TestCreateContextPersistsAndReturnsPlan(t *testing.T) {
	contexts := &stubContextRepository{created: model.Context{ContextID: "ctx-1", Name: "primary", Provider: "aws"}}
	rec := postCreateContext(t, contexts, `{
		"name": "primary",
		"cloudProviderAlias": "aws-acme",
		"region": "eu-west-2",
		"instanceType": "c8gd.2xlarge",
		"diskType": "gp3",
		"diskSizeGb": 100
	}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if contexts.createCalls != 1 {
		t.Fatalf("expected exactly one Create call, got %d", contexts.createCalls)
	}

	var response createContextResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Context == nil || response.Context.ContextID != "ctx-1" {
		t.Fatalf("unexpected persisted context: %+v", response.Context)
	}
	if len(response.Plan) == 0 {
		t.Fatalf("non-preview create must still return the bootstrap plan")
	}
}

func planContains(plan []string, substr string) bool {
	for _, line := range plan {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}
