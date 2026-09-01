package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// enrollUserAPI serves POST /v1/users with a canned status and body, the
// shape EnrollERunPlatformUser's failure classification (tenant_platform.go's
// call into enrollERunPlatformUserError, tenant_platform_error.go) has to
// tell apart.
func enrollUserAPI(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/users" || req.Method != http.MethodPost {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func enrollAuthEnvelope(code string) string {
	encoded, _ := json.Marshal(map[string]string{"code": code, "message": "denied"})
	return string(encoded)
}

// TestEnrollERunPlatformUserRendersTheAdministratorHandoff is the regression
// for erun#1820: a not-yet-enrolled caller's self-enroll attempt is refused
// by the platform's own auth middleware -- documented on
// EnrollERunPlatformUser as the expected outcome -- and used to surface as
// the raw wire error (a JSON envelope inside "http 401: ..."). It must
// instead read as the hand-off the not-enrolled card already offers
// (request an invitation, or hand the printed command to an administrator).
func TestEnrollERunPlatformUserRendersTheAdministratorHandoff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"forbidden -- enrolled but lacks user-management capability", http.StatusForbidden, "Forbidden"},
		{"unauthorized not-enrolled", http.StatusUnauthorized, enrollAuthEnvelope("NOT_ENROLLED")},
		{"unauthorized unclassified (older platform)", http.StatusUnauthorized, "unauthorized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := enrollUserAPI(t, tc.status, tc.body)
			defer server.Close()
			app := testERunPlatformAliasApp(t, server.URL)

			_, err := app.EnrollERunPlatformUser(uiPlatformUserEnrollInput{
				Username: "jane",
				Issuer:   testERunIssuer,
				Subject:  testERunSubject,
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			message := err.Error()
			if !strings.Contains(message, "do not have permission to enroll yourself") {
				t.Fatalf("expected the administrator hand-off, got %q", message)
			}
			for _, leak := range []string{"http 401", "http 403", "/v1/users", "NOT_ENROLLED"} {
				if strings.Contains(message, leak) {
					t.Errorf("hand-off message leaks the wire form %q: %s", leak, message)
				}
			}
		})
	}
}

// TestEnrollERunPlatformUserKeepsTheRawErrorForUnexpectedFailures is the
// inversion the fix must not introduce: TENANT_UNRESOLVED and
// RESOLUTION_FAILED are not enrollment answers (tenantDashboardIdentityFailure
// draws the same line on the read path), and a genuinely unclassifiable
// failure must not be folded into the friendly hand-off either -- that would
// hide real breakage behind a reassuring message.
func TestEnrollERunPlatformUserKeepsTheRawErrorForUnexpectedFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"tenant unresolved", http.StatusUnauthorized, enrollAuthEnvelope("TENANT_UNRESOLVED")},
		{"resolution failed", http.StatusUnauthorized, enrollAuthEnvelope("RESOLUTION_FAILED")},
		{"username already taken", http.StatusConflict, `{"code":"USERNAME_TAKEN","message":"a user with this username already exists"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := enrollUserAPI(t, tc.status, tc.body)
			defer server.Close()
			app := testERunPlatformAliasApp(t, server.URL)

			_, err := app.EnrollERunPlatformUser(uiPlatformUserEnrollInput{
				Username: "jane",
				Issuer:   testERunIssuer,
				Subject:  testERunSubject,
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), "do not have permission to enroll yourself") {
				t.Fatalf("an unexpected failure must not be folded into the hand-off message, got %q", err.Error())
			}
			if !strings.Contains(err.Error(), "/v1/users") {
				t.Fatalf("expected the raw wire form to stay primary for an unexpected failure, got %q", err.Error())
			}
		})
	}
}
