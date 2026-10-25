package clouddefaults

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

func TestFetchHelpersReturnErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := defaultFetchPlatformInfo(core.Context{}, srv.URL); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if _, err := defaultFetchOIDCDiscovery(core.Context{}, srv.URL); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestDefaultStartERunDeviceAuthorizationRequiresEndpoint(t *testing.T) {
	if _, err := defaultStartERunDeviceAuthorization(core.Context{}, cloud.OIDCDiscovery{Issuer: "https://auth.example.test"}, "cli-1", ErunOAuthScope); err == nil {
		t.Fatal("expected an error when no device authorization endpoint is advertised")
	}
}

// deviceTokenServer answers authorization_pending N times, optionally slow_down once, then success.
type deviceTokenServer struct {
	pendingCount int
	slowDownOnce bool
	calls        int
}

func (s *deviceTokenServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.calls++
		if s.pendingCount > 0 {
			s.pendingCount--
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		if s.slowDownOnce {
			s.slowDownOnce = false
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.FormValue("device_code") != "dev-code" {
			t.Fatalf("device_code = %q", r.FormValue("device_code"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"expires_in":    3600,
		})
	}
}

func TestPollERunDeviceTokenSucceedsAfterPendingAndSlowDown(t *testing.T) {
	sim := &deviceTokenServer{pendingCount: 2, slowDownOnce: true}
	srv := httptest.NewServer(sim.handler(t))
	defer srv.Close()

	auth := cloud.ERunDeviceAuthorization{
		DeviceCode: "dev-code",
		Interval:   time.Millisecond,
		ExpiresIn:  10 * time.Second,
	}
	tokens, err := pollERunDeviceToken(core.Context{}, cloud.OIDCDiscovery{TokenEndpoint: srv.URL}, "cli-1", auth)
	if err != nil {
		t.Fatalf("pollERunDeviceToken: %v", err)
	}
	if tokens.AccessToken != "access-1" || tokens.RefreshToken != "refresh-1" || tokens.ExpiresIn != time.Hour {
		t.Fatalf("tokens = %+v", tokens)
	}
	if sim.calls != 4 { // 2 pending + 1 slow_down + 1 success
		t.Fatalf("calls = %d, want 4", sim.calls)
	}
}

func TestPollERunDeviceTokenFailsOnExpiredToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "expired_token"})
	}))
	defer srv.Close()

	auth := cloud.ERunDeviceAuthorization{DeviceCode: "dev-code", Interval: time.Millisecond, ExpiresIn: 10 * time.Second}
	if _, err := pollERunDeviceToken(core.Context{}, cloud.OIDCDiscovery{TokenEndpoint: srv.URL}, "cli-1", auth); err == nil {
		t.Fatal("expected an error for expired_token")
	} else if !strings.Contains(err.Error(), "expired_token") {
		t.Fatalf("error = %v, want to mention expired_token", err)
	}
}

func TestPollERunDeviceTokenTimesOutWhenAlwaysPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
	}))
	defer srv.Close()

	auth := cloud.ERunDeviceAuthorization{DeviceCode: "dev-code", Interval: time.Millisecond, ExpiresIn: 5 * time.Millisecond}
	if _, err := pollERunDeviceToken(core.Context{}, cloud.OIDCDiscovery{TokenEndpoint: srv.URL}, "cli-1", auth); err == nil {
		t.Fatal("expected the poll to time out")
	} else if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("error = %v, want to mention expiry", err)
	}
}

func TestDefaultRefreshERunTokensPropagatesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "refresh token revoked"})
	}))
	defer srv.Close()

	_, err := defaultRefreshERunTokens(core.Context{}, cloud.OIDCDiscovery{TokenEndpoint: srv.URL}, "cli-1", "refresh-1")
	if err == nil || !strings.Contains(err.Error(), "refresh token revoked") {
		t.Fatalf("err = %v, want to mention the server's error description", err)
	}
}

// Without Basic auth and the org-claim scope a machine token authenticates but resolves to no tenant.
func TestDefaultClientCredentialsERunTokensSendsBasicAuthAndTheOrgClaimScope(t *testing.T) {
	var gotUser, gotSecret string
	var gotBasic bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotSecret, gotBasic = r.BasicAuth()
		assertClientCredentialsRequest(t, r)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "machine-access", "expires_in": 3600})
	}))
	defer srv.Close()

	tokens, err := defaultClientCredentialsERunTokens(core.Context{}, cloud.OIDCDiscovery{TokenEndpoint: srv.URL}, "client-1", "secret-1")
	if err != nil {
		t.Fatalf("defaultClientCredentialsERunTokens: %v", err)
	}
	if tokens.AccessToken != "machine-access" || tokens.ExpiresIn != time.Hour {
		t.Fatalf("tokens = %+v", tokens)
	}
	if !gotBasic || gotUser != "client-1" || gotSecret != "secret-1" {
		t.Fatalf("basic auth = %v (%q/%q)", gotBasic, gotUser, gotSecret)
	}
}

func assertClientCredentialsRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Fatalf("parse form: %v", err)
	}
	if r.FormValue("grant_type") != "client_credentials" {
		t.Fatalf("grant_type = %q", r.FormValue("grant_type"))
	}
	if r.FormValue("scope") != ErunOrgClaimScope {
		t.Fatalf("scope = %q, want %q", r.FormValue("scope"), ErunOrgClaimScope)
	}
	if r.FormValue("client_secret") != "" {
		t.Fatal("client secret travelled in the request body")
	}
}

func TestDefaultClientCredentialsERunTokensRetriesAnUnknownScope(t *testing.T) {
	scopes := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		scopes = append(scopes, r.FormValue("scope"))
		if r.FormValue("scope") != "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_scope"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "machine-access", "expires_in": 3600})
	}))
	defer srv.Close()

	tokens, err := defaultClientCredentialsERunTokens(core.Context{}, cloud.OIDCDiscovery{TokenEndpoint: srv.URL}, "client-1", "secret-1")
	if err != nil {
		t.Fatalf("defaultClientCredentialsERunTokens: %v", err)
	}
	if tokens.AccessToken != "machine-access" {
		t.Fatalf("tokens = %+v", tokens)
	}
	if len(scopes) != 2 || scopes[0] != ErunOrgClaimScope || scopes[1] != "" {
		t.Fatalf("requested scopes = %q, want the org claim then none", scopes)
	}
}

func TestDefaultClientCredentialsERunTokensDryRunPerformsNoRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("dry run reached the issuer")
	}))
	defer srv.Close()

	if _, err := defaultClientCredentialsERunTokens(core.Context{DryRun: true}, cloud.OIDCDiscovery{TokenEndpoint: srv.URL}, "client-1", "secret-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPKCEChallengeMatchesVerifierSHA256(t *testing.T) {
	verifier, err := erunPKCEVerifier()
	if err != nil {
		t.Fatalf("erunPKCEVerifier: %v", err)
	}
	if len(verifier) < 43 {
		t.Fatalf("verifier %q is shorter than RFC 7636's minimum length", verifier)
	}
	challenge := erunPKCEChallenge(verifier)
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if challenge != want {
		t.Fatalf("challenge = %q, want %q", challenge, want)
	}
}

func TestPKCEVerifierAndStateAreUnpredictable(t *testing.T) {
	v1, err := erunPKCEVerifier()
	if err != nil {
		t.Fatalf("erunPKCEVerifier: %v", err)
	}
	v2, err := erunPKCEVerifier()
	if err != nil {
		t.Fatalf("erunPKCEVerifier: %v", err)
	}
	if v1 == v2 {
		t.Fatal("two generated verifiers must not collide")
	}
	s1, err := erunPKCEState()
	if err != nil {
		t.Fatalf("erunPKCEState: %v", err)
	}
	s2, err := erunPKCEState()
	if err != nil {
		t.Fatalf("erunPKCEState: %v", err)
	}
	if s1 == s2 {
		t.Fatal("two generated states must not collide")
	}
}

func TestERunAuthorizationCodeURLIncludesPKCEParams(t *testing.T) {
	discovery := cloud.OIDCDiscovery{AuthorizationEndpoint: "https://auth.example.test/authorize"}
	authURL := erunAuthorizationCodeURL(discovery, "cli-1", "http://127.0.0.1:9999/callback", "state-1", "challenge-1", ErunOAuthScope)
	for _, want := range []string{
		"https://auth.example.test/authorize?",
		"client_id=cli-1",
		"code_challenge=challenge-1",
		"code_challenge_method=S256",
		"state=state-1",
		"response_type=code",
	} {
		if !strings.Contains(authURL, want) {
			t.Fatalf("authURL %q does not contain %q", authURL, want)
		}
	}
}
