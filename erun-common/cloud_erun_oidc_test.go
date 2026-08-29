package eruncommon

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDefaultFetchPlatformInfoParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/platform" {
			t.Fatalf("path = %q, want /v1/platform", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(PlatformInfo{Issuer: "https://auth.example.test", CLIClientID: "cli-1"})
	}))
	defer srv.Close()

	info, err := defaultFetchPlatformInfo(Context{}, srv.URL)
	if err != nil {
		t.Fatalf("defaultFetchPlatformInfo: %v", err)
	}
	if info.Issuer != "https://auth.example.test" || info.CLIClientID != "cli-1" {
		t.Fatalf("info = %+v", info)
	}
}

func TestDefaultFetchOIDCDiscoveryParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(OIDCDiscovery{
			Issuer:                      "http://placeholder.invalid",
			TokenEndpoint:               "/token",
			DeviceAuthorizationEndpoint: "/device",
		})
	}))
	defer srv.Close()

	discovery, err := defaultFetchOIDCDiscovery(Context{}, srv.URL)
	if err != nil {
		t.Fatalf("defaultFetchOIDCDiscovery: %v", err)
	}
	if discovery.TokenEndpoint != "/token" || discovery.DeviceAuthorizationEndpoint != "/device" {
		t.Fatalf("discovery = %+v", discovery)
	}
}

func TestFetchHelpersReturnErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := defaultFetchPlatformInfo(Context{}, srv.URL); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if _, err := defaultFetchOIDCDiscovery(Context{}, srv.URL); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestDryRunFetchersPerformNoRequest(t *testing.T) {
	// A dry run must never depend on the platform being reachable, so both
	// fetchers must short-circuit before making any HTTP call.
	if _, err := defaultFetchPlatformInfo(Context{DryRun: true}, "http://127.0.0.1:1"); err != nil {
		t.Fatalf("dry run fetch platform info: %v", err)
	}
	if _, err := defaultFetchOIDCDiscovery(Context{DryRun: true}, "http://127.0.0.1:1"); err != nil {
		t.Fatalf("dry run fetch discovery: %v", err)
	}
}

func TestDefaultStartERunDeviceAuthorization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.FormValue("client_id") != "cli-1" {
			t.Fatalf("client_id = %q", r.FormValue("client_id"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "dev-code",
			"user_code":                 "USER-CODE",
			"verification_uri":          "https://auth.example.test/device",
			"verification_uri_complete": "https://auth.example.test/device?code=USER-CODE",
			"expires_in":                600,
			"interval":                  5,
		})
	}))
	defer srv.Close()

	auth, err := defaultStartERunDeviceAuthorization(Context{}, OIDCDiscovery{DeviceAuthorizationEndpoint: srv.URL}, "cli-1", erunOAuthScope)
	if err != nil {
		t.Fatalf("defaultStartERunDeviceAuthorization: %v", err)
	}
	if auth.DeviceCode != "dev-code" || auth.UserCode != "USER-CODE" {
		t.Fatalf("auth = %+v", auth)
	}
	if auth.Interval != 5*time.Second || auth.ExpiresIn != 600*time.Second {
		t.Fatalf("auth timing = %+v", auth)
	}
}

func TestDefaultStartERunDeviceAuthorizationRequiresEndpoint(t *testing.T) {
	if _, err := defaultStartERunDeviceAuthorization(Context{}, OIDCDiscovery{Issuer: "https://auth.example.test"}, "cli-1", erunOAuthScope); err == nil {
		t.Fatal("expected an error when no device authorization endpoint is advertised")
	}
}

// deviceTokenServer simulates a token endpoint that answers
// authorization_pending N times, then optionally slow_down once, then success.
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

	auth := ERunDeviceAuthorization{
		DeviceCode: "dev-code",
		Interval:   time.Millisecond,
		ExpiresIn:  10 * time.Second,
	}
	tokens, err := pollERunDeviceToken(Context{}, OIDCDiscovery{TokenEndpoint: srv.URL}, "cli-1", auth)
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

	auth := ERunDeviceAuthorization{DeviceCode: "dev-code", Interval: time.Millisecond, ExpiresIn: 10 * time.Second}
	if _, err := pollERunDeviceToken(Context{}, OIDCDiscovery{TokenEndpoint: srv.URL}, "cli-1", auth); err == nil {
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

	auth := ERunDeviceAuthorization{DeviceCode: "dev-code", Interval: time.Millisecond, ExpiresIn: 5 * time.Millisecond}
	if _, err := pollERunDeviceToken(Context{}, OIDCDiscovery{TokenEndpoint: srv.URL}, "cli-1", auth); err == nil {
		t.Fatal("expected the poll to time out")
	} else if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("error = %v, want to mention expiry", err)
	}
}

func TestPollERunDeviceTokenDryRunPerformsNoRequest(t *testing.T) {
	auth := ERunDeviceAuthorization{DeviceCode: "dev-code", Interval: time.Millisecond, ExpiresIn: time.Second}
	if _, err := pollERunDeviceToken(Context{DryRun: true}, OIDCDiscovery{TokenEndpoint: "http://127.0.0.1:1"}, "cli-1", auth); err != nil {
		t.Fatalf("dry run poll: %v", err)
	}
}

func TestDefaultRefreshERunTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != "refresh-1" {
			t.Fatalf("form = %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh-access", "expires_in": 3600})
	}))
	defer srv.Close()

	tokens, err := defaultRefreshERunTokens(Context{}, OIDCDiscovery{TokenEndpoint: srv.URL}, "cli-1", "refresh-1")
	if err != nil {
		t.Fatalf("defaultRefreshERunTokens: %v", err)
	}
	if tokens.AccessToken != "fresh-access" || tokens.ExpiresIn != time.Hour {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestDefaultRefreshERunTokensPropagatesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "refresh token revoked"})
	}))
	defer srv.Close()

	_, err := defaultRefreshERunTokens(Context{}, OIDCDiscovery{TokenEndpoint: srv.URL}, "cli-1", "refresh-1")
	if err == nil || !strings.Contains(err.Error(), "refresh token revoked") {
		t.Fatalf("err = %v, want to mention the server's error description", err)
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
	discovery := OIDCDiscovery{AuthorizationEndpoint: "https://auth.example.test/authorize"}
	authURL := erunAuthorizationCodeURL(discovery, "cli-1", "http://127.0.0.1:9999/callback", "state-1", "challenge-1", erunOAuthScope)
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

// TestRunERunAuthorizationCodeLoginRoundTrip drives the whole PKCE fallback
// end to end: it lets the function open its own loopback listener, recovers
// the redirect_uri/state it generated from the printed sign-in URL (the only
// channel a black-box caller has), simulates the browser's GET to that
// callback, and confirms the resulting token exchange reaches the real code
// verifier.
// authorizationCodeExchangeHandler simulates the token endpoint's
// authorization_code grant, asserting the exchange carries the given code and
// a non-empty PKCE code_verifier.
func authorizationCodeExchangeHandler(t *testing.T, wantCode string, tokens ERunTokens) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.FormValue("grant_type") != "authorization_code" || r.FormValue("code") != wantCode || r.FormValue("code_verifier") == "" {
			t.Fatalf("form = %v, want grant_type=authorization_code code=%s and a non-empty code_verifier", r.Form, wantCode)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": tokens.AccessToken, "refresh_token": tokens.RefreshToken,
			"expires_in": int(tokens.ExpiresIn.Seconds()),
		})
	}
}

// startERunAuthorizationCodeLogin runs the login in the background and
// returns channels for its result, mirroring how a real caller would await it
// while a browser callback happens concurrently.
func startERunAuthorizationCodeLogin(discovery OIDCDiscovery, clientID string, stdout io.Writer) (<-chan ERunTokens, <-chan error) {
	resultCh := make(chan ERunTokens, 1)
	errCh := make(chan error, 1)
	go func() {
		tokens, err := runERunAuthorizationCodeLogin(Context{Stdout: stdout}, discovery, clientID, erunOAuthScope)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- tokens
	}()
	return resultCh, errCh
}

func TestRunERunAuthorizationCodeLoginRoundTrip(t *testing.T) {
	tokenSrv := httptest.NewServer(authorizationCodeExchangeHandler(t, "auth-code-1", ERunTokens{AccessToken: "access-1", RefreshToken: "refresh-1", ExpiresIn: time.Hour}))
	defer tokenSrv.Close()

	discovery := OIDCDiscovery{AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: tokenSrv.URL}
	stdout := &syncBuffer{}
	resultCh, errCh := startERunAuthorizationCodeLogin(discovery, "cli-1", stdout)

	authURL := stdout.awaitURL(t, 2*time.Second)
	redirectURI, state := mustParseRedirectAndState(t, authURL)

	callbackResp, err := http.Get(fmt.Sprintf("%s?code=auth-code-1&state=%s", redirectURI, state))
	if err != nil {
		t.Fatalf("simulate browser callback GET: %v", err)
	}
	defer func() { _ = callbackResp.Body.Close() }()

	select {
	case tokens := <-resultCh:
		if tokens.AccessToken != "access-1" || tokens.RefreshToken != "refresh-1" {
			t.Fatalf("tokens = %+v", tokens)
		}
	case err := <-errCh:
		t.Fatalf("runERunAuthorizationCodeLogin: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the login flow to complete after the callback")
	}
}

func mustParseRedirectAndState(t *testing.T, authURL string) (string, string) {
	t.Helper()
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse printed auth url: %v", err)
	}
	redirectURI := parsed.Query().Get("redirect_uri")
	state := parsed.Query().Get("state")
	if redirectURI == "" || state == "" {
		t.Fatalf("auth url %q is missing redirect_uri or state", authURL)
	}
	return redirectURI, state
}

// syncBuffer lets the test goroutine poll for the sign-in URL the login
// printed while the login's own goroutine concurrently writes it.
type syncBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *syncBuffer) awaitURL(t *testing.T, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		snapshot := string(b.data)
		b.mu.Unlock()
		// The authorization endpoint's own scheme, not "http://127.0.0.1" —
		// the redirect_uri (which does start with that) is percent-encoded
		// inside the printed URL's query string, so it never appears literally.
		if idx := strings.Index(snapshot, "https://auth.example.test/authorize"); idx >= 0 {
			rest := snapshot[idx:]
			if end := strings.IndexAny(rest, "\n \t"); end >= 0 {
				return rest[:end]
			}
			return strings.TrimSpace(rest)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for the sign-in url to be printed")
	return ""
}

func TestExchangeERunAuthorizationCodeAndCallbackHandler(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.FormValue("code_verifier") != "verifier-1" {
			t.Fatalf("code_verifier = %q", r.FormValue("code_verifier"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-1", "refresh_token": "refresh-1", "expires_in": 3600})
	}))
	defer tokenSrv.Close()

	tokens, err := exchangeERunAuthorizationCode(tokenSrv.URL, "cli-1", "http://127.0.0.1:1/callback", "verifier-1", "code-1")
	if err != nil {
		t.Fatalf("exchangeERunAuthorizationCode: %v", err)
	}
	if tokens.AccessToken != "access-1" {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestERunCallbackHandlerAcceptsMatchingStateAndCode(t *testing.T) {
	resultCh := make(chan erunCallbackResult, 1)
	handler := erunCallbackHandler("state-1", resultCh)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(fmt.Sprintf("%s/callback?code=auth-code-1&state=state-1", srv.URL))
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	result := <-resultCh
	if result.err != nil || result.code != "auth-code-1" {
		t.Fatalf("result = %+v", result)
	}
}

func TestERunCallbackHandlerRejectsStateMismatch(t *testing.T) {
	resultCh := make(chan erunCallbackResult, 1)
	handler := erunCallbackHandler("state-1", resultCh)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(fmt.Sprintf("%s/callback?code=auth-code-1&state=wrong-state", srv.URL))
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	result := <-resultCh
	if result.err == nil {
		t.Fatal("expected a state-mismatch error")
	}
}

func TestERunCallbackHandlerSurfacesProviderError(t *testing.T) {
	resultCh := make(chan erunCallbackResult, 1)
	handler := erunCallbackHandler("state-1", resultCh)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(fmt.Sprintf("%s/callback?error=access_denied&state=state-1", srv.URL))
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	result := <-resultCh
	if result.err == nil || !strings.Contains(result.err.Error(), "access_denied") {
		t.Fatalf("result = %+v, want an access_denied error", result)
	}
}

func TestOAuthTokenErrorMapsKnownCodes(t *testing.T) {
	cases := map[string]error{
		`{"error":"authorization_pending"}`: errERunAuthorizationPending,
		`{"error":"slow_down"}`:             errERunSlowDown,
		`{"error":"expired_token"}`:         errERunExpiredToken,
		`{"error":"access_denied"}`:         errERunAccessDenied,
	}
	for body, want := range cases {
		got := oauthTokenError(http.StatusBadRequest, []byte(body))
		if got != want {
			t.Fatalf("oauthTokenError(%q) = %v, want %v", body, got, want)
		}
	}
}

func TestOAuthTokenErrorFallsBackToServerMessage(t *testing.T) {
	err := oauthTokenError(http.StatusInternalServerError, []byte(`{"error":"server_error","error_description":"something broke"}`))
	if !strings.Contains(err.Error(), "something broke") {
		t.Fatalf("err = %v, want to mention the server's description", err)
	}
}
