package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubCloudProviderAliasWriter struct {
	err            error
	calls          int
	gotAlias       string
	gotProvider    string
	gotCredentials string
}

func (s *stubCloudProviderAliasWriter) Set(_ context.Context, alias, provider, credentials string) error {
	s.calls++
	s.gotAlias, s.gotProvider, s.gotCredentials = alias, provider, credentials
	return s.err
}

func putAlias(t *testing.T, routes CloudProviderAliasRoutes, alias string, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/v1/cloud-provider-aliases/"+alias, bytes.NewReader(raw))
	req.SetPathValue("alias", alias)
	rec := httptest.NewRecorder()
	routes.setAlias(rec, req)
	return rec
}

// TestSetAliasRefusesWithNoStorageConfigured is the regression test for a
// real defect this driving-the-console pass found: with no ERUN_SECRETS_KEY
// configured, server.go used to leave this whole route unregistered, so a
// PUT here 404'd with the mux's own generic "not found" -- no diagnosis an
// operator (or the console rendering "Could not save credentials: alias
// request failed (404)") could act on. Every real deployment leaves
// ERUN_SECRETS_KEY unset today (no chart sets it), so this was not a
// hypothetical: the console's cloud-context provisioning surface was a
// complete, silent dead end in production. The route is now always
// registered and refuses with a named, actionable 501 instead.
func TestSetAliasRefusesWithNoStorageConfigured(t *testing.T) {
	routes := CloudProviderAliasRoutes{aliases: nil}
	rec := putAlias(t, routes, "acme", map[string]string{"credentials": `{"accessKeyId":"x"}`})

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	message, _ := decoded["message"].(string)
	if message == "" || message == "not found" {
		t.Fatalf("response carried no actionable diagnosis, got %q", message)
	}
}

func TestSetAliasStoresCredentialsWhenConfigured(t *testing.T) {
	writer := &stubCloudProviderAliasWriter{}
	routes := CloudProviderAliasRoutes{aliases: writer}
	rec := putAlias(t, routes, "acme", map[string]string{"credentials": `{"accessKeyId":"x"}`})

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if writer.calls != 1 {
		t.Fatalf("Set called %d times, want 1", writer.calls)
	}
	if writer.gotAlias != "acme" || writer.gotProvider != "aws" || writer.gotCredentials != `{"accessKeyId":"x"}` {
		t.Fatalf("Set called with (%q, %q, %q)", writer.gotAlias, writer.gotProvider, writer.gotCredentials)
	}
}

func TestSetAliasRejectsNonAWSProvider(t *testing.T) {
	writer := &stubCloudProviderAliasWriter{}
	routes := CloudProviderAliasRoutes{aliases: writer}
	rec := putAlias(t, routes, "acme", map[string]string{"provider": "gcp", "credentials": "x"})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if writer.calls != 0 {
		t.Fatalf("Set should not have been called, was called %d times", writer.calls)
	}
}

func TestSetAliasRejectsEmptyCredentials(t *testing.T) {
	writer := &stubCloudProviderAliasWriter{}
	routes := CloudProviderAliasRoutes{aliases: writer}
	rec := putAlias(t, routes, "acme", map[string]string{})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
