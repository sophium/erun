package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// registrationWriteAPI serves the Registration tab's write routes, refusing
// every path in forbidden the same way reviewWriteAPI/tenantDashboardAPI do.
func registrationWriteAPI(routes map[string]func(w http.ResponseWriter, req *http.Request), forbidden map[string]bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		key := req.Method + " " + req.URL.Path
		if forbidden[key] {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		route, ok := routes[key]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		route(w, req)
	}))
}

func TestRegisterPlatformEnvironmentRegisters(t *testing.T) {
	server := registrationWriteAPI(map[string]func(w http.ResponseWriter, req *http.Request){
		"POST /v1/environments": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"environmentId":"env-1","tenantId":"tenant-1","name":"prod","type":"runtime","status":"registered"}`))
		},
	}, nil)
	defer server.Close()

	outcome, err := tenantDashboardApp(t, server.URL).RegisterPlatformEnvironment(uiRegisterPlatformEnvironmentInput{
		Tenant: "frs", Name: "prod", Type: "runtime",
	})
	if err != nil {
		t.Fatalf("RegisterPlatformEnvironment failed: %v", err)
	}
	if outcome.Kind != "accepted" || outcome.Environment == nil || outcome.Environment.Name != "prod" {
		t.Fatalf("expected an accepted registration, got %+v", outcome)
	}
}

// TestRegisterPlatformEnvironmentReportsQuotaCapAsAConflict pins rule (5): a
// quota-cap refusal is a recoverable Kind, not a raw error — the operator's
// next action (delete/stop another environment) is visible on the outcome,
// not buried in an error string.
func TestRegisterPlatformEnvironmentReportsQuotaCapAsAConflict(t *testing.T) {
	const quotaMessage = "environment quota reached: this tenant already has 3 of 3 environments"
	server := registrationWriteAPI(map[string]func(w http.ResponseWriter, req *http.Request){
		"POST /v1/environments": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, quotaMessage, http.StatusConflict)
		},
	}, nil)
	defer server.Close()

	outcome, err := tenantDashboardApp(t, server.URL).RegisterPlatformEnvironment(uiRegisterPlatformEnvironmentInput{
		Tenant: "frs", Name: "prod", Type: "runtime",
	})
	if err != nil {
		t.Fatalf("expected a conflict outcome, not an error: %v", err)
	}
	if outcome.Kind != "conflict" {
		t.Fatalf("expected Kind conflict, got %+v", outcome)
	}
	if !strings.Contains(outcome.Message, "quota reached") {
		t.Fatalf("expected the platform's own quota message, got %q", outcome.Message)
	}
}

func TestRegisterPlatformEnvironmentRequiresATenant(t *testing.T) {
	_, err := NewApp(erunUIDeps{store: stubUIStore{}}).RegisterPlatformEnvironment(uiRegisterPlatformEnvironmentInput{
		Name: "prod", Type: "runtime",
	})
	if err == nil || !errors.Is(err, ErrTenantNotGiven) {
		t.Fatalf("expected ErrTenantNotGiven, got %v", err)
	}
}

func TestRegisterPlatformEnvironmentRequiresNameAndType(t *testing.T) {
	server := registrationWriteAPI(nil, nil)
	defer server.Close()

	_, err := tenantDashboardApp(t, server.URL).RegisterPlatformEnvironment(uiRegisterPlatformEnvironmentInput{Tenant: "frs"})
	if err == nil || !strings.Contains(err.Error(), "are required") {
		t.Fatalf("expected a required-fields error, got %v", err)
	}
}

// TestDeployPlatformEnvironmentReportsUnavailable pins the other recoverable
// outcome: no deploy executor configured on the control plane is not the
// same failure as a genuine error, and the operator-facing message says so.
func TestDeployPlatformEnvironmentReportsUnavailable(t *testing.T) {
	server := registrationWriteAPI(map[string]func(w http.ResponseWriter, req *http.Request){
		"POST /v1/environments/env-1/deploy": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no deploy executor is configured on this control plane", http.StatusNotImplemented)
		},
	}, nil)
	defer server.Close()

	outcome, err := tenantDashboardApp(t, server.URL).DeployPlatformEnvironment(uiPlatformEnvironmentActionInput{
		Tenant: "frs", EnvironmentID: "env-1",
	})
	if err != nil {
		t.Fatalf("expected an unavailable outcome, not an error: %v", err)
	}
	if outcome.Kind != "unavailable" {
		t.Fatalf("expected Kind unavailable, got %+v", outcome)
	}
	if !strings.Contains(outcome.Message, "no deploy executor") {
		t.Fatalf("expected the platform's own message, got %q", outcome.Message)
	}
}

func TestDeployPlatformEnvironmentDeploys(t *testing.T) {
	server := registrationWriteAPI(map[string]func(w http.ResponseWriter, req *http.Request){
		"POST /v1/environments/env-1/deploy": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"environmentId":"env-1","tenantId":"tenant-1","name":"prod","type":"runtime","status":"provisioning"}`))
		},
	}, nil)
	defer server.Close()

	outcome, err := tenantDashboardApp(t, server.URL).DeployPlatformEnvironment(uiPlatformEnvironmentActionInput{
		Tenant: "frs", EnvironmentID: "env-1",
	})
	if err != nil {
		t.Fatalf("DeployPlatformEnvironment failed: %v", err)
	}
	if outcome.Kind != "accepted" || outcome.Environment == nil || outcome.Environment.Status != "provisioning" {
		t.Fatalf("expected an accepted deploy, got %+v", outcome)
	}
}

func TestStopPlatformEnvironmentRequiresAnEnvironmentID(t *testing.T) {
	server := registrationWriteAPI(nil, nil)
	defer server.Close()

	_, err := tenantDashboardApp(t, server.URL).StopPlatformEnvironment(uiPlatformEnvironmentActionInput{Tenant: "frs"})
	if err == nil || !strings.Contains(err.Error(), "environment id is required") {
		t.Fatalf("expected an environment-id-required error, got %v", err)
	}
}

// TestDeletePlatformEnvironmentReportsConflict pins the delete-in-progress
// case: the same recoverable Kind as a quota cap, distinguishable by its
// message.
func TestDeletePlatformEnvironmentReportsConflict(t *testing.T) {
	server := registrationWriteAPI(map[string]func(w http.ResponseWriter, req *http.Request){
		"DELETE /v1/environments/env-1": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "a delete is already in progress for this environment", http.StatusConflict)
		},
	}, nil)
	defer server.Close()

	outcome, err := tenantDashboardApp(t, server.URL).DeletePlatformEnvironment(uiPlatformEnvironmentActionInput{
		Tenant: "frs", EnvironmentID: "env-1",
	})
	if err != nil {
		t.Fatalf("expected a conflict outcome, not an error: %v", err)
	}
	if outcome.Kind != "conflict" {
		t.Fatalf("expected Kind conflict, got %+v", outcome)
	}
}

// TestCreatePlatformContextPreviewsWithoutCreating pins rule (3): a preview
// call resolves the plan without a Context in the response, and the outcome
// still reports Kind accepted (a preview succeeding is not a refusal).
func TestCreatePlatformContextPreviewsWithoutCreating(t *testing.T) {
	server := registrationWriteAPI(map[string]func(w http.ResponseWriter, req *http.Request){
		"POST /v1/contexts": func(w http.ResponseWriter, req *http.Request) {
			if !strings.Contains(readBody(t, req), `"preview":true`) {
				t.Fatalf("expected the preview flag to travel with the request")
			}
			_, _ = w.Write([]byte(`{"plan":["resolve tenant frs","bootstrap aws context prod in eu-west-2"]}`))
		},
	}, nil)
	defer server.Close()

	outcome, err := tenantDashboardApp(t, server.URL).CreatePlatformContext(uiCreatePlatformContextInput{
		Tenant: "frs", Name: "prod", CloudProviderAlias: "aws-main", Region: "eu-west-2", Preview: true,
	})
	if err != nil {
		t.Fatalf("CreatePlatformContext failed: %v", err)
	}
	if outcome.Kind != "accepted" || outcome.Context != nil || len(outcome.Plan) != 2 {
		t.Fatalf("expected a preview-only accepted outcome with no created context, got %+v", outcome)
	}
}

func TestCreatePlatformContextRequiresNameAliasAndRegion(t *testing.T) {
	server := registrationWriteAPI(nil, nil)
	defer server.Close()

	_, err := tenantDashboardApp(t, server.URL).CreatePlatformContext(uiCreatePlatformContextInput{Tenant: "frs"})
	if err == nil || !strings.Contains(err.Error(), "are required") {
		t.Fatalf("expected a required-fields error, got %v", err)
	}
}

// TestPreviewPlatformEnvironmentAlwaysReportsAPlan pins that the register
// preview never surfaces a quota conflict as an error — POST /v1/environments
// with preview:true always resolves 200, with QuotaOk naming the blocking
// decision instead.
func TestPreviewPlatformEnvironmentAlwaysReportsAPlan(t *testing.T) {
	server := registrationWriteAPI(map[string]func(w http.ResponseWriter, req *http.Request){
		"POST /v1/environments": func(w http.ResponseWriter, req *http.Request) {
			if !strings.Contains(readBody(t, req), `"preview":true`) {
				t.Fatalf("expected the preview flag to travel with the request")
			}
			_, _ = w.Write([]byte(`{"plan":["resolve tenant frs","quota: 3 of 3 environments"],"quotaOk":false}`))
		},
	}, nil)
	defer server.Close()

	result, err := tenantDashboardApp(t, server.URL).PreviewPlatformEnvironment(uiRegisterPlatformEnvironmentInput{
		Tenant: "frs", Name: "prod", Type: "runtime",
	})
	if err != nil {
		t.Fatalf("PreviewPlatformEnvironment failed: %v", err)
	}
	if result.QuotaOk || len(result.Plan) != 2 {
		t.Fatalf("expected the resolved plan with quotaOk false, got %+v", result)
	}
}

// TestPreviewPlatformEnvironmentSendsTheSameFieldsAsRegister pins the
// property the preview/register split exists for: the same input, sent to
// PreviewPlatformEnvironment, carries the exact same fields onto the wire as
// RegisterPlatformEnvironment would — a plan an operator previews can never
// diverge from what register then submits.
func TestPreviewPlatformEnvironmentSendsTheSameFieldsAsRegister(t *testing.T) {
	input := uiRegisterPlatformEnvironmentInput{
		Tenant: "frs", Name: "prod", Type: "runtime", ContextID: "ctx-1", RuntimeVersion: "1.2.3",
	}
	var registerBody, previewBody string
	server := registrationWriteAPI(map[string]func(w http.ResponseWriter, req *http.Request){
		"POST /v1/environments": func(w http.ResponseWriter, req *http.Request) {
			body := readBody(t, req)
			if strings.Contains(body, `"preview":true`) {
				previewBody = body
				_, _ = w.Write([]byte(`{"plan":["resolve tenant frs"],"quotaOk":true}`))
				return
			}
			registerBody = body
			_, _ = w.Write([]byte(`{"environmentId":"env-1","tenantId":"tenant-1","name":"prod","type":"runtime","status":"registered"}`))
		},
	}, nil)
	defer server.Close()
	app := tenantDashboardApp(t, server.URL)

	if _, err := app.PreviewPlatformEnvironment(input); err != nil {
		t.Fatalf("PreviewPlatformEnvironment failed: %v", err)
	}
	if _, err := app.RegisterPlatformEnvironment(input); err != nil {
		t.Fatalf("RegisterPlatformEnvironment failed: %v", err)
	}

	for _, field := range []string{`"name":"prod"`, `"type":"runtime"`, `"contextId":"ctx-1"`, `"runtimeVersion":"1.2.3"`} {
		if !strings.Contains(previewBody, field) {
			t.Fatalf("preview body missing %s: %s", field, previewBody)
		}
		if !strings.Contains(registerBody, field) {
			t.Fatalf("register body missing %s: %s", field, registerBody)
		}
	}
}

// TestRegisterPlatformEnvironmentSendsAdopt pins that Adopt travels onto
// the wire, so the platform's own adopt validation (kubernetesContext
// required, runtimeVersion/contextId forbidden) is what a caller actually hits.
func TestRegisterPlatformEnvironmentSendsAdopt(t *testing.T) {
	var body string
	server := registrationWriteAPI(map[string]func(w http.ResponseWriter, req *http.Request){
		"POST /v1/environments": func(w http.ResponseWriter, req *http.Request) {
			body = readBody(t, req)
			_, _ = w.Write([]byte(`{"environmentId":"env-1","tenantId":"tenant-1","name":"prod","type":"runtime","status":"registered"}`))
		},
	}, nil)
	defer server.Close()

	_, err := tenantDashboardApp(t, server.URL).RegisterPlatformEnvironment(uiRegisterPlatformEnvironmentInput{
		Tenant: "frs", Name: "prod", Type: "runtime", KubernetesContext: "primary", Adopt: true,
	})
	if err != nil {
		t.Fatalf("RegisterPlatformEnvironment failed: %v", err)
	}
	if !strings.Contains(body, `"adopt":true`) {
		t.Fatalf("expected the adopt flag to travel with the request, got %s", body)
	}
}

func readBody(t *testing.T, req *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return string(body)
}

// TestTenantDashboardRegistrationHidesTabWhenNothingReadableOrWritable pins
// the Registration tab's own hide condition: neither list is readable and
// neither write is grantable, so there is genuinely nothing to show.
func TestTenantDashboardRegistrationHidesTabWhenNothingReadableOrWritable(t *testing.T) {
	var requests []string
	capabilities := `[{"method":"GET","path":"/v1/whoami"}]`
	server := tenantDashboardAPI(t, capabilities, nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	panel := panelFor(t, dashboard, tenantDashboardTabRegistration)
	if panel.Restricted == "" {
		t.Fatalf("expected the Registration tab to be restricted, got %+v", panel)
	}
}

// TestTenantDashboardRegistrationDegradesPartially pins the "partial access
// degrades partially" rule: the caller can read environments but not
// contexts, so the tab stays visible and only the contexts list names what
// is missing.
func TestTenantDashboardRegistrationDegradesPartially(t *testing.T) {
	var requests []string
	capabilities := `[{"method":"GET","path":"/v1/whoami"},{"method":"GET","path":"/v1/environments"}]`
	server := tenantDashboardAPI(t, capabilities, nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	panel := panelFor(t, dashboard, tenantDashboardTabRegistration)
	if panel.Restricted != "" {
		t.Fatalf("expected the Registration tab to stay visible, got %+v", panel)
	}
	if dashboard.ContextsRestricted == "" {
		t.Fatalf("expected the contexts list to report its own restriction, got %+v", dashboard)
	}
	if len(dashboard.Environments) != 1 {
		t.Fatalf("expected the environments list to still load, got %+v", dashboard.Environments)
	}
}

// TestTenantDashboardRegistrationLoadsBothLists is the happy path: an
// unknown capability set (nil) leaves every read attemptable, same as every
// other panel.
func TestTenantDashboardRegistrationLoadsBothLists(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	if len(dashboard.Contexts) != 1 || dashboard.Contexts[0].Name != "prod" {
		t.Fatalf("expected the contexts list to load, got %+v", dashboard.Contexts)
	}
	if len(dashboard.Environments) != 1 || dashboard.Environments[0].Name != "prod" {
		t.Fatalf("expected the environments list to load, got %+v", dashboard.Environments)
	}
	if !dashboard.CanCreateContext || !dashboard.CanRegisterEnvironment {
		t.Fatalf("expected an unknown capability set to leave every write attemptable, got %+v", dashboard)
	}
}
