package zitadel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestClientAgainstRealZitadel verifies request/response shapes against a real
// Zitadel v4 core, which a mocked server cannot. Opt-in; to run:
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
// The SMTP path additionally needs a local SMTP sink:
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
	e2eCrossOrgListAndDeactivate(t, ctx, client)
}

// e2eCrossOrgListAndDeactivate: a user created in another org is reachable only
// by naming that org, never through the credential's own.
func e2eCrossOrgListAndDeactivate(t *testing.T, ctx context.Context, client *Client) {
	t.Helper()
	orgName := fmt.Sprintf("erun-e2e-org-%d", time.Now().UnixNano())
	org, err := client.CreateOrg(ctx, orgName)
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}

	username := fmt.Sprintf("erun-e2e-crossorg-%d", time.Now().UnixNano())
	created, err := client.CreateHumanUser(ctx, CreateHumanUserParams{
		Username: username, Email: username + "@erun.local", FirstName: "Erun", LastName: "CrossOrg",
		InitialPassword: "Er7hK2mQ9xL4nP6z!",
		OrgID:           org.ID,
	})
	if err != nil {
		t.Fatalf("CreateHumanUser(orgId=%s): %v", org.ID, err)
	}

	// The target org lists the user first, so the projection has caught up before the
	// own-org read below can claim its absence.
	_, listed, err := e2eListUsersUntilListed(t, ctx, client, org.ID, created.ID)
	if err != nil {
		t.Fatalf("ListUsers(orgId=%s): %v", org.ID, err)
	}
	if !listed {
		t.Fatalf("ListUsers(orgId=%s) did not return the user created in it within %s: %+v", org.ID, e2eListUsersTimeout, created)
	}

	ownOrgUsers, err := client.ListUsers(ctx, "")
	if err != nil {
		t.Fatalf("ListUsers (own org): %v", err)
	}
	if containsUserID(ownOrgUsers, created.ID) {
		t.Fatalf("the credential's own org listed a user created in a different org: %+v", created)
	}

	if err := client.DeactivateUser(ctx, "", created.ID); err == nil {
		t.Fatal("want DeactivateUser to fail to find a cross-org user with no org context (the bug this issue reports)")
	}
	if err := client.DeactivateUser(ctx, org.ID, created.ID); err != nil {
		t.Fatalf("DeactivateUser(orgId=%s): %v", org.ID, err)
	}
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

	users, listed, err := e2eListUsersUntilListed(t, ctx, client, "", created.ID)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if !listed {
		t.Fatalf("ListUsers did not return the just-created user %s within %s: %+v", created.ID, e2eListUsersTimeout, users)
	}
	return created
}

// Zitadel lists users from an eventually consistent projection, so a just-created
// user can be missing from the first read on any v4 core.
const e2eListUsersTimeout = 10 * time.Second

func e2eListUsersUntilListed(t *testing.T, ctx context.Context, client *Client, orgID, userID string) ([]User, bool, error) {
	t.Helper()
	var (
		users []User
		err   error
	)
	listed := e2ePollUntil(t, e2eListUsersTimeout, func() bool {
		users, err = client.ListUsers(ctx, orgID)
		return err == nil && containsUserID(users, userID)
	})
	return users, listed, err
}

// Zitadel refuses to deactivate a USER_STATE_INITIAL user ("can only be deleted").
func e2eRefusesDeactivatingAnInitialUser(t *testing.T, ctx context.Context, client *Client, userID string) {
	t.Helper()
	err := client.DeactivateUser(ctx, "", userID)
	if err == nil {
		t.Fatal("want DeactivateUser to refuse a USER_STATE_INITIAL user")
	}
	var apiErr *APIError
	ok := errors.As(err, &apiErr)
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

// e2eConfiguresSMTPAndSkipsTheEmailFlow converges SMTP config against
// ERUN_E2E_ZITADEL_SMTP_HOST; skipped when unset.
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

	// Zitadel's query side is eventually consistent: a read right after activation can 404.
	if !e2ePollUntil(t, 15*time.Second, func() bool {
		status, err := client.GetSMTPStatus(ctx)
		return err == nil && status.Configured
	}) {
		t.Fatal("GetSMTPStatus never reported the just-activated config within the deadline")
	}

	e2eVerifiesSMTPSinkDelivery(t, ctx, client)
}

// e2eVerifiesSMTPSinkDelivery checks a real invite reaches the sink; skipped when
// ERUN_E2E_ZITADEL_SMTP_SINK_API is unset.
func e2eVerifiesSMTPSinkDelivery(t *testing.T, ctx context.Context, client *Client) {
	t.Helper()
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

// e2eSinkReceivedMessageFor queries a mailhog-shaped v2 message API.
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
