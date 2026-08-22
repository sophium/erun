package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
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

// TestCreateContextRejectsInvalidName: the name becomes both a Kubernetes
// label value and a kubeconfig context name a placed Job's `erun` command
// composes via shellJoin argv (#1112), so it must be a DNS-1123 label like
// an environment name — not free text.
func TestCreateContextRejectsInvalidName(t *testing.T) {
	cases := map[string]string{
		"uppercase":           `{"name":"Primary","cloudProviderAlias":"aws-acme","region":"eu-west-2"}`,
		"space":               `{"name":"my cluster","cloudProviderAlias":"aws-acme","region":"eu-west-2"}`,
		"hyphen-bounded":      `{"name":"-primary","cloudProviderAlias":"aws-acme","region":"eu-west-2"}`,
		"shell metacharacter": `{"name":"primary$(rm)","cloudProviderAlias":"aws-acme","region":"eu-west-2"}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			contexts := &stubContextRepository{}
			rec := postCreateContext(t, contexts, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("unexpected status: %d", rec.Code)
			}
			if contexts.createCalls != 0 {
				t.Fatalf("Create should not run on an invalid name, got %d calls", contexts.createCalls)
			}
		})
	}
}

// TestCreateContextThreadsMaxEnvironments: an explicit maxEnvironments
// reaches the repository create call unchanged; omitted defaults via
// repository.DefaultContextMaxEnvironments (asserted in the repository's own
// test), not this route.
func TestCreateContextThreadsMaxEnvironments(t *testing.T) {
	contexts := &stubContextRepository{created: model.Context{ContextID: "ctx-1", Name: "primary"}}
	rec := postCreateContext(t, contexts, `{
		"name": "primary",
		"cloudProviderAlias": "aws-acme",
		"region": "eu-west-2",
		"maxEnvironments": 8
	}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if contexts.createInput.MaxEnvironments != 8 {
		t.Fatalf("createInput.MaxEnvironments = %d, want 8", contexts.createInput.MaxEnvironments)
	}
}

// TestCreateContextRejectsNegativeMaxEnvironments guards the one value a
// capacity ceiling cannot sensibly take.
func TestCreateContextRejectsNegativeMaxEnvironments(t *testing.T) {
	contexts := &stubContextRepository{}
	rec := postCreateContext(t, contexts, `{
		"name": "primary",
		"cloudProviderAlias": "aws-acme",
		"region": "eu-west-2",
		"maxEnvironments": -1
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if contexts.createCalls != 0 {
		t.Fatal("Create should not run on a negative maxEnvironments")
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
	// The preview plan is a faithful bootstrap dry-run, so it must carry the real provisioning commands.
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

type stubContextProvisioner struct {
	started []provision.ProvisionInput
}

func (s *stubContextProvisioner) Start(in provision.ProvisionInput) error {
	s.started = append(s.started, in)
	return nil
}

// TestCreateContextStartsProvisioningWhenWired: a configured provisioner flips create from an inline plan to an async provisioning workflow (202 Accepted).
func TestCreateContextStartsProvisioningWhenWired(t *testing.T) {
	contexts := &stubContextRepository{created: model.Context{ContextID: "ctx-1", Name: "primary", Provider: "aws", Status: "provisioning"}}
	prov := &stubContextProvisioner{}
	req := httptest.NewRequest(http.MethodPost, "/v1/contexts",
		bytes.NewBufferString(`{"name":"primary","cloudProviderAlias":"aws-acme","region":"eu-west-2","preview":false}`))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "t1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "u1",
	}))
	rec := httptest.NewRecorder()

	ContextRoutes{contexts: contexts, provisioner: prov}.createContext(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s), want 202 Accepted", rec.Code, rec.Body.String())
	}
	if len(prov.started) != 1 {
		t.Fatalf("Start called %d times, want 1", len(prov.started))
	}
	if got := prov.started[0]; got.ContextID != "ctx-1" || got.TenantID != "t1" ||
		got.CloudProviderAlias != "aws-acme" || got.Region != "eu-west-2" {
		t.Fatalf("provision input = %+v", got)
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
