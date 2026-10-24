package zitadel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

// A fresh AddSMTPConfig defaults to SMTP_CONFIG_INACTIVE.
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

// Zitadel's password update refuses an inactive config, though a field update succeeds.
func TestUpdateSMTPConfigActivatesBeforeWritingThePassword(t *testing.T) {
	var order []string
	client := routedTestClient(t, []jsonRoute{
		{method: http.MethodPost, path: "/admin/v1/smtp/_search", body: map[string]any{
			"result": []map[string]any{{"id": "smtp-3", "state": "SMTP_CONFIG_INACTIVE"}},
		}},
		{method: http.MethodPost, path: "/admin/v1/smtp/smtp-3/_activate", record: func(map[string]any) { order = append(order, "activate") }},
		{method: http.MethodPut, path: "/admin/v1/smtp/smtp-3", record: func(map[string]any) { order = append(order, "fields") }},
		{method: http.MethodPut, path: "/admin/v1/smtp/smtp-3/password", record: func(map[string]any) { order = append(order, "password") }},
	})

	if _, err := client.UpdateSMTPConfig(context.Background(), SetSMTPConfigParams{
		Host: "smtp.example.com:587", SenderAddress: "noreply@example.com", Password: "s3cret",
	}); err != nil {
		t.Fatalf("UpdateSMTPConfig: %v", err)
	}
	want := []string{"activate", "fields", "password"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i, step := range want {
		if order[i] != step {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}
