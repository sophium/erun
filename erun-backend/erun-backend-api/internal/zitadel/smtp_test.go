package zitadel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetSMTPStatusReportsUnconfiguredOn404 locks the exact shape this issue
// started from: a real Zitadel instance with no SMTP provider answers 404
// with {"code":5,"message":"SMTP configuration not found (QUERY-fwofw)"}
// (verified live against a real v4.15.3 instance), which must read as
// SMTPStatus{Configured: false}, not propagate as an error.
func TestGetSMTPStatusReportsUnconfiguredOn404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":5, "message":"SMTP configuration not found (QUERY-fwofw)"}`))
	}))
	t.Cleanup(server.Close)
	client := newClient(server.URL, "auth.example.com", "test-pat")

	status, err := client.GetSMTPStatus(context.Background())
	if err != nil {
		t.Fatalf("GetSMTPStatus: %v", err)
	}
	if status.Configured {
		t.Fatalf("status = %+v, want Configured=false on a 404", status)
	}
}

func TestGetSMTPStatusReportsConfiguredConfig(t *testing.T) {
	client := routedTestClient(t, []jsonRoute{
		{method: http.MethodGet, path: "/admin/v1/smtp", body: map[string]any{
			"smtpConfig": map[string]any{
				"id": "smtp-1", "host": "smtp.example.com:587", "user": "erun",
				"senderAddress": "noreply@example.com", "senderName": "Erun Platform",
				"tls": true, "state": "SMTP_CONFIG_ACTIVE",
			},
		}},
	})

	status, err := client.GetSMTPStatus(context.Background())
	if err != nil {
		t.Fatalf("GetSMTPStatus: %v", err)
	}
	want := SMTPStatus{Configured: true, Config: SMTPConfig{
		Host: "smtp.example.com:587", User: "erun",
		SenderAddress: "noreply@example.com", SenderName: "Erun Platform", TLS: true,
	}}
	if status != want {
		t.Fatalf("status = %+v, want %+v", status, want)
	}
}

// TestUpdateSMTPConfigCreatesAndActivatesWhenNoneExists locks the
// first-configuration path: Zitadel has no smtp config at all (the issue's
// exact starting state), so this must create one and then explicitly
// activate it -- a created-but-inactive config never becomes deliverable
// (confirmed live: a fresh AddSMTPConfig defaults to SMTP_CONFIG_INACTIVE).
func TestUpdateSMTPConfigCreatesAndActivatesWhenNoneExists(t *testing.T) {
	var createBody map[string]any
	activated := ""
	client := routedTestClient(t, []jsonRoute{
		{method: http.MethodPost, path: "/admin/v1/smtp/_search", body: map[string]any{"result": []map[string]any{}}},
		{method: http.MethodPost, path: "/admin/v1/smtp", body: map[string]any{"id": "smtp-new"}, record: func(body map[string]any) {
			createBody = body
		}},
		{method: http.MethodPost, path: "/admin/v1/smtp/smtp-new/_activate", record: func(map[string]any) { activated = "smtp-new" }},
	})

	status, err := client.UpdateSMTPConfig(context.Background(), SetSMTPConfigParams{
		Host: "smtp.example.com:587", User: "erun", Password: "s3cret",
		SenderAddress: "noreply@example.com", SenderName: "Erun Platform", TLS: true,
	})
	if err != nil {
		t.Fatalf("UpdateSMTPConfig: %v", err)
	}
	if activated != "smtp-new" {
		t.Fatalf("activated = %q, want the newly created config to be activated", activated)
	}
	if createBody["password"] != "s3cret" {
		t.Fatalf("create body = %+v, want the password included on first create", createBody)
	}
	if !status.Configured || status.Config.Host != "smtp.example.com:587" {
		t.Fatalf("status = %+v, want the converged config reported", status)
	}
}

func TestUpdateSMTPConfigRequiresPasswordOnFirstCreate(t *testing.T) {
	client := routedTestClient(t, []jsonRoute{
		{method: http.MethodPost, path: "/admin/v1/smtp/_search", body: map[string]any{"result": []map[string]any{}}},
	})

	if _, err := client.UpdateSMTPConfig(context.Background(), SetSMTPConfigParams{
		Host: "smtp.example.com:587", SenderAddress: "noreply@example.com",
	}); err == nil {
		t.Fatal("want an error when no config exists yet and no password was supplied")
	}
}

// TestUpdateSMTPConfigUpdatesExistingConfigWithoutTouchingPassword locks that
// an update with no Password leaves Zitadel's stored password alone -- the
// only way it can be, since Zitadel never returns it to diff against.
func TestUpdateSMTPConfigUpdatesExistingConfigWithoutTouchingPassword(t *testing.T) {
	fieldPuts, passwordPuts, activates := 0, 0, 0
	client := routedTestClient(t, []jsonRoute{
		{method: http.MethodPost, path: "/admin/v1/smtp/_search", body: map[string]any{
			"result": []map[string]any{{"id": "smtp-1", "state": "SMTP_CONFIG_ACTIVE"}},
		}},
		{method: http.MethodPut, path: "/admin/v1/smtp/smtp-1", record: func(map[string]any) { fieldPuts++ }},
		{method: http.MethodPut, path: "/admin/v1/smtp/smtp-1/password", record: func(map[string]any) { passwordPuts++ }},
		{method: http.MethodPost, path: "/admin/v1/smtp/smtp-1/_activate", record: func(map[string]any) { activates++ }},
	})

	if _, err := client.UpdateSMTPConfig(context.Background(), SetSMTPConfigParams{
		Host: "smtp.example.com:587", SenderAddress: "noreply@example.com",
	}); err != nil {
		t.Fatalf("UpdateSMTPConfig: %v", err)
	}
	if fieldPuts != 1 {
		t.Fatalf("fieldPuts = %d, want the existing config's fields updated", fieldPuts)
	}
	if passwordPuts != 0 {
		t.Fatalf("passwordPuts = %d, want no password write when none was supplied", passwordPuts)
	}
	if activates != 0 {
		t.Fatalf("activates = %d, want no re-activation of a config that is already active", activates)
	}
}

// TestUpdateSMTPConfigActivatesAnInactiveExistingConfig locks the
// re-activation half: an existing-but-inactive config (e.g. deactivated by
// an operator, or left over from an earlier failed converge) must be
// activated again, not just have its fields updated.
func TestUpdateSMTPConfigActivatesAnInactiveExistingConfig(t *testing.T) {
	activated := ""
	client := routedTestClient(t, []jsonRoute{
		{method: http.MethodPost, path: "/admin/v1/smtp/_search", body: map[string]any{
			"result": []map[string]any{{"id": "smtp-2", "state": "SMTP_CONFIG_INACTIVE"}},
		}},
		{method: http.MethodPut, path: "/admin/v1/smtp/smtp-2"},
		{method: http.MethodPost, path: "/admin/v1/smtp/smtp-2/_activate", record: func(map[string]any) { activated = "smtp-2" }},
	})

	if _, err := client.UpdateSMTPConfig(context.Background(), SetSMTPConfigParams{
		Host: "smtp.example.com:587", SenderAddress: "noreply@example.com",
	}); err != nil {
		t.Fatalf("UpdateSMTPConfig: %v", err)
	}
	if activated != "smtp-2" {
		t.Fatalf("activated = %q, want the inactive existing config reactivated", activated)
	}
}
