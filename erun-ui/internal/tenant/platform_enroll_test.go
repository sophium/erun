package tenant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// enrollUserAPI serves POST /v1/users with a canned status and body.
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
	encoded, err := json.Marshal(map[string]string{"code": code, "message": "denied"})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// A 401 on self-enroll is expected and must read as the administrator
// hand-off, not the raw wire error.
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

			_, err := app.EnrollERunPlatformUser(PlatformUserEnrollInput{
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

// Non-enrollment failures keep their raw error rather than hiding behind the
// hand-off.
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

			_, err := app.EnrollERunPlatformUser(PlatformUserEnrollInput{
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
