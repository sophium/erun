package zitadel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestClientAgainstRealZitadel proves this client's request/response shapes
// against a real Zitadel v4 core instance -- the exact concern a mocked
// httptest server cannot settle, since every field name here (userName,
// forceMfa, minLength as a JSON *string*, the _deactivate/_reactivate path
// shape, the human/machine "state initial can only be deleted" business
// rule) was originally guessed from Zitadel's public API and then verified
// live before being committed.
//
// Opt-in and skipped by default (this repository's convention for a gate
// that needs real infrastructure, e.g. ERUN_E2E_REVIEWS_DATABASE_URL). Stand
// up a Zitadel core + Postgres exactly like
// erun-console/playwright/zitadel/stack.sh does (that script is the
// console's real-IdP e2e; this test only needs core, not the separate Login
// V2 container, since it drives the Management API directly):
//
//	docker network create erun-zitadel-e2e-net
//	docker volume create erun-zitadel-e2e-bootstrap
//	docker run -d --name erun-zitadel-e2e-pg --network erun-zitadel-e2e-net \
//	  -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=zitadel postgres:18
//	docker run -d --name erun-zitadel-e2e-core --network erun-zitadel-e2e-net --user 0 \
//	  -p 18081:8080 -v erun-zitadel-e2e-bootstrap:/zitadel/bootstrap:rw \
//	  -e ZITADEL_PORT=8080 -e ZITADEL_EXTERNALDOMAIN=localhost -e ZITADEL_EXTERNALPORT=18081 \
//	  -e ZITADEL_EXTERNALSECURE=false -e ZITADEL_TLS_ENABLED=false \
//	  -e ZITADEL_DATABASE_POSTGRES_HOST=erun-zitadel-e2e-pg -e ZITADEL_DATABASE_POSTGRES_PORT=5432 \
//	  -e ZITADEL_DATABASE_POSTGRES_DATABASE=zitadel \
//	  -e ZITADEL_DATABASE_POSTGRES_USER_USERNAME=postgres -e ZITADEL_DATABASE_POSTGRES_USER_PASSWORD=postgres \
//	  -e ZITADEL_DATABASE_POSTGRES_USER_SSL_MODE=disable \
//	  -e ZITADEL_DATABASE_POSTGRES_ADMIN_USERNAME=postgres -e ZITADEL_DATABASE_POSTGRES_ADMIN_PASSWORD=postgres \
//	  -e ZITADEL_DATABASE_POSTGRES_ADMIN_SSL_MODE=disable \
//	  -e ZITADEL_FIRSTINSTANCE_ORG_NAME=erun -e ZITADEL_FIRSTINSTANCE_ORG_HUMAN_USERNAME=zadmin \
//	  -e ZITADEL_FIRSTINSTANCE_ORG_HUMAN_PASSWORD='Password1!' \
//	  -e ZITADEL_FIRSTINSTANCE_ORG_HUMAN_PASSWORDCHANGEREQUIRED=false \
//	  -e ZITADEL_FIRSTINSTANCE_ORG_HUMAN_EMAIL_ADDRESS=zadmin@erun.local \
//	  -e ZITADEL_FIRSTINSTANCE_ORG_HUMAN_EMAIL_VERIFIED=true \
//	  -e ZITADEL_FIRSTINSTANCE_PATPATH=/zitadel/bootstrap/admin-sa.pat \
//	  -e ZITADEL_FIRSTINSTANCE_ORG_MACHINE_MACHINE_USERNAME=admin-sa \
//	  -e ZITADEL_FIRSTINSTANCE_ORG_MACHINE_MACHINE_NAME='Admin Service Account' \
//	  -e ZITADEL_FIRSTINSTANCE_ORG_MACHINE_PAT_EXPIRATIONDATE='2030-01-01T00:00:00Z' \
//	  -e ZITADEL_FIRSTINSTANCE_LOGINCLIENTPATPATH=/zitadel/bootstrap/login-client.pat \
//	  -e ZITADEL_FIRSTINSTANCE_ORG_LOGINCLIENT_MACHINE_USERNAME=login-client \
//	  -e ZITADEL_FIRSTINSTANCE_ORG_LOGINCLIENT_MACHINE_NAME='Login Client' \
//	  -e ZITADEL_FIRSTINSTANCE_ORG_LOGINCLIENT_PAT_EXPIRATIONDATE='2030-01-01T00:00:00Z' \
//	  ghcr.io/zitadel/zitadel:v4.15.3 start-from-init --masterkey 'MasterkeyNeedsToHave32Characters' --tlsMode disabled
//	# wait for http://localhost:18081/debug/healthz to return 200, then:
//	docker run --rm -v erun-zitadel-e2e-bootstrap:/b alpine cat /b/admin-sa.pat > /tmp/admin-sa.pat
//	ERUN_E2E_ZITADEL_BASE_URL=http://localhost:18081 \
//	ERUN_E2E_ZITADEL_EXTERNAL_DOMAIN=localhost \
//	ERUN_E2E_ZITADEL_PAT_PATH=/tmp/admin-sa.pat \
//	  go test ./internal/zitadel/... -run TestClientAgainstRealZitadel -v
//
// Two more variables (issue #1168) additionally exercise the SMTP
// configuration path against a real local SMTP sink, e.g. mailhog on the
// same docker network as the Zitadel core container above:
//
//	docker run -d --name mailhog --network erun-zitadel-e2e-net mailhog/mailhog
//	ERUN_E2E_ZITADEL_SMTP_HOST=mailhog:1025 \
//	ERUN_E2E_ZITADEL_SMTP_SINK_API=http://localhost:8025 \
//	  go test ./internal/zitadel/... -run TestClientAgainstRealZitadel -v
func TestClientAgainstRealZitadel(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("ERUN_E2E_ZITADEL_BASE_URL"))
	externalDomain := strings.TrimSpace(os.Getenv("ERUN_E2E_ZITADEL_EXTERNAL_DOMAIN"))
	patPath := strings.TrimSpace(os.Getenv("ERUN_E2E_ZITADEL_PAT_PATH"))
	if baseURL == "" || externalDomain == "" || patPath == "" {
		t.Skip("set ERUN_E2E_ZITADEL_BASE_URL, ERUN_E2E_ZITADEL_EXTERNAL_DOMAIN and ERUN_E2E_ZITADEL_PAT_PATH to run against a real Zitadel (see this test's doc comment)")
	}

	client, err := NewClientFromFile(Config{BaseURL: baseURL, ExternalDomain: externalDomain, PATPath: patPath})
	if err != nil || client == nil {
		t.Fatalf("NewClientFromFile: client=%v err=%v", client, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	created := e2eCreateAndListUser(t, ctx, client)
	e2eRefusesDeactivatingAnInitialUser(t, ctx, client, created.ID)
	e2eUpdatesOrgSettings(t, ctx, client)
	e2eConfiguresSMTPAndSkipsTheEmailFlow(t, ctx, client)
}

func e2eCreateAndListUser(t *testing.T, ctx context.Context, client *Client) User {
	t.Helper()
	username := fmt.Sprintf("erun-e2e-%d", time.Now().UnixNano())
	created, err := client.CreateHumanUser(ctx, CreateHumanUserParams{
		Username: username, Email: username + "@erun.local", FirstName: "Erun", LastName: "E2E",
	})
	if err != nil {
		t.Fatalf("CreateHumanUser: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateHumanUser returned no id")
	}

	users, err := client.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if !containsUserID(users, created.ID) {
		t.Fatalf("ListUsers did not return the just-created user %s: %+v", created.ID, users)
	}
	return created
}

// e2eRefusesDeactivatingAnInitialUser locks a real business rule: a freshly
// invited user is USER_STATE_INITIAL, which Zitadel refuses to deactivate
// ("can only be deleted not deactivated").
func e2eRefusesDeactivatingAnInitialUser(t *testing.T, ctx context.Context, client *Client, userID string) {
	t.Helper()
	err := client.DeactivateUser(ctx, userID)
	if err == nil {
		t.Fatal("want DeactivateUser to refuse a USER_STATE_INITIAL user")
	}
	apiErr, ok := err.(*APIError)
	if !ok || !strings.Contains(apiErr.Body, "initial") {
		t.Fatalf("DeactivateUser error = %v, want Zitadel's state-precondition message", err)
	}
}

func e2eUpdatesOrgSettings(t *testing.T, ctx context.Context, client *Client) {
	t.Helper()
	settings, err := client.GetOrgSettings(ctx)
	if err != nil {
		t.Fatalf("GetOrgSettings: %v", err)
	}
	if settings.MinPasswordLength == 0 {
		t.Fatalf("GetOrgSettings = %+v, want a non-zero default password length", settings)
	}

	forceMFA := true
	updated, err := client.UpdateOrgSettings(ctx, UpdateOrgSettingsParams{ForceMFA: &forceMFA})
	if err != nil {
		t.Fatalf("UpdateOrgSettings: %v", err)
	}
	if !updated.ForceMFA {
		t.Fatalf("UpdateOrgSettings did not persist forceMfa=true: %+v", updated)
	}
	// Restore, so a re-run against the same long-lived instance starts clean.
	forceMFA = false
	if _, err := client.UpdateOrgSettings(ctx, UpdateOrgSettingsParams{ForceMFA: &forceMFA}); err != nil {
		t.Fatalf("UpdateOrgSettings (restore): %v", err)
	}
}

// e2eConfiguresSMTPAndSkipsTheEmailFlow proves GetSMTPStatus/UpdateSMTPConfig
// against a real Zitadel Admin API (issue #1168): the instance this test
// runs against starts with no SMTP config at all (Zitadel's own 404, the
// issue's exact starting point), so this converges one against
// ERUN_E2E_ZITADEL_SMTP_HOST (point it at a local SMTP sink such as
// mailhog, e.g. "localhost:1025") and confirms GetSMTPStatus reports it
// active. When ERUN_E2E_ZITADEL_SMTP_SINK_API also names that sink's own
// message API (mailhog's "http://localhost:8025"), it additionally proves an
// actual message was produced and accepted -- not merely that a config
// object was written -- by creating a real invite and polling the sink for
// the resulting message. Both are optional and skip cleanly when unset,
// matching this repository's convention for a gate that needs real
// infrastructure.
func e2eConfiguresSMTPAndSkipsTheEmailFlow(t *testing.T, ctx context.Context, client *Client) {
	t.Helper()
	smtpHost := strings.TrimSpace(os.Getenv("ERUN_E2E_ZITADEL_SMTP_HOST"))
	if smtpHost == "" {
		t.Log("ERUN_E2E_ZITADEL_SMTP_HOST unset; skipping the SMTP configuration e2e step")
		return
	}

	if _, err := client.GetSMTPStatus(ctx); err != nil {
		t.Fatalf("GetSMTPStatus (initial): %v", err)
	}

	updated, err := client.UpdateSMTPConfig(ctx, SetSMTPConfigParams{
		Host: smtpHost, User: "erun", Password: "unused-by-a-local-sink",
		SenderAddress: "noreply@erun.local", SenderName: "Erun Platform E2E", TLS: false,
	})
	if err != nil {
		t.Fatalf("UpdateSMTPConfig: %v", err)
	}
	if !updated.Configured || updated.Config.Host != smtpHost {
		t.Fatalf("UpdateSMTPConfig result = %+v, want Configured with host %q", updated, smtpHost)
	}

	// Zitadel's query side is eventually consistent with the command that
	// just activated this config (confirmed live: a read immediately after
	// activation can still 404 for a few seconds), so this polls rather than
	// asserting on the first read.
	if !e2ePollUntil(t, 15*time.Second, func() bool {
		status, err := client.GetSMTPStatus(ctx)
		return err == nil && status.Configured
	}) {
		t.Fatal("GetSMTPStatus never reported the just-activated config within the deadline")
	}

	sinkAPI := strings.TrimSpace(os.Getenv("ERUN_E2E_ZITADEL_SMTP_SINK_API"))
	if sinkAPI == "" {
		t.Log("ERUN_E2E_ZITADEL_SMTP_SINK_API unset; not verifying actual message delivery")
		return
	}
	username := fmt.Sprintf("erun-e2e-smtp-%d", time.Now().UnixNano())
	recipient := username + "@erun.local"
	if _, err := client.CreateHumanUser(ctx, CreateHumanUserParams{
		Username: username, Email: recipient, FirstName: "Erun", LastName: "SMTP",
	}); err != nil {
		t.Fatalf("CreateHumanUser: %v", err)
	}
	if !e2ePollUntil(t, 20*time.Second, func() bool { return e2eSinkReceivedMessageFor(t, sinkAPI, recipient) }) {
		t.Fatalf("no message for %s arrived at the SMTP sink within the deadline", recipient)
	}
}

// e2ePollUntil polls condition every 500ms until it reports true or timeout
// elapses, returning whether it ever did.
func e2ePollUntil(t *testing.T, timeout time.Duration, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if condition() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// e2eSinkReceivedMessageFor queries a mailhog-shaped sink's v2 message API
// for any message addressed to recipient.
func e2eSinkReceivedMessageFor(t *testing.T, sinkAPI string, recipient string) bool {
	t.Helper()
	resp, err := http.Get(strings.TrimRight(sinkAPI, "/") + "/api/v2/messages")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	var decoded struct {
		Items []struct {
			To []struct {
				Mailbox string `json:"Mailbox"`
				Domain  string `json:"Domain"`
			} `json:"To"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return false
	}
	for _, item := range decoded.Items {
		for _, to := range item.To {
			if to.Mailbox+"@"+to.Domain == recipient {
				return true
			}
		}
	}
	return false
}

func containsUserID(users []User, id string) bool {
	for _, u := range users {
		if u.ID == id {
			return true
		}
	}
	return false
}
