package eruncommon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OIDCDiscovery is the subset of an OIDC provider's
// /.well-known/openid-configuration document the device and PKCE flows need.
type OIDCDiscovery struct {
	Issuer                      string `json:"issuer"`
	AuthorizationEndpoint       string `json:"authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint,omitempty"`
}

// ERunDeviceAuthorization is a device authorization grant's initial response
// (RFC 8628 section 3.2): what the operator is told to visit, and what the
// poller then presents at the token endpoint.
type ERunDeviceAuthorization struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               time.Duration
	Interval                time.Duration
}

// ERunTokens is a successful OIDC token response.
type ERunTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
}

// erunOAuthScope requests both an id/access token and a refresh token, since a
// CLI/agent session needs to outlive one access token's short lifetime.
const erunOAuthScope = "openid offline_access"

// erunOrgClaimScope asks the shipped Zitadel IdP to include the org
// (resourceowner) claim erun's tenant resolution reads for a shared,
// org-scoped issuer. Requested by default on every erun login so a token
// actually carries the discriminator, rather than requiring an operator to
// know to pass --scope by hand. A dedicated/BYO issuer (the common case) has
// never heard of this scope; erunCloudProviderLogin retries once without it
// when the issuer refuses the request, so login still succeeds exactly as it
// did before this default was added.
const erunOrgClaimScope = "urn:zitadel:iam:user:resourceowner"

// erunLoginScope appends any operator-requested scopes to the baseline. A
// provider's reserved scopes are frequently absent from its discovery
// document's scopes_supported (Zitadel's urn:zitadel:* family is), so there is
// nothing to negotiate against — the caller has to be able to ask for them by
// name. Duplicates and blanks are dropped so the request stays well-formed.
func erunLoginScope(extra []string) string {
	scopes := strings.Fields(erunOAuthScope)
	seen := map[string]bool{}
	for _, s := range scopes {
		seen[s] = true
	}
	for _, candidate := range extra {
		for _, s := range strings.Fields(candidate) {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			scopes = append(scopes, s)
		}
	}
	return strings.Join(scopes, " ")
}

var (
	errERunAuthorizationPending = errors.New("authorization_pending")
	errERunSlowDown             = errors.New("slow_down")
	errERunExpiredToken         = errors.New("expired_token")
	errERunAccessDenied         = errors.New("access_denied")
	errERunInvalidScope         = errors.New("invalid_scope")
)

// isERunInvalidScopeError reports whether a login attempt failed because the
// issuer rejected a requested scope. The device/token-endpoint path surfaces
// this through oauthTokenError's errERunInvalidScope sentinel; the
// Authorization Code + PKCE path instead learns it from the redirect's own
// error query parameter (erunCallbackHandler), which is a plain wrapped
// string rather than that sentinel.
func isERunInvalidScopeError(err error) bool {
	return err != nil && (errors.Is(err, errERunInvalidScope) || strings.Contains(err.Error(), "invalid_scope"))
}

// defaultFetchPlatformInfo delegates to PlatformClient — the same
// unauthenticated GET /v1/platform call a caller makes once it has a
// PlatformClient of its own, so the two never drift.
func defaultFetchPlatformInfo(ctx Context, apiURL string) (PlatformInfo, error) {
	target := strings.TrimRight(strings.TrimSpace(apiURL), "/")
	ctx.Trace("GET " + target + "/v1/platform")
	if ctx.DryRun {
		return PlatformInfo{}, nil
	}
	info, err := NewPlatformClient(target, nil).Platform(context.Background())
	if err != nil {
		return PlatformInfo{}, fmt.Errorf("fetch erun platform info: %w", err)
	}
	return info, nil
}

func defaultFetchOIDCDiscovery(ctx Context, issuer string) (OIDCDiscovery, error) {
	target := strings.TrimRight(strings.TrimSpace(issuer), "/") + "/.well-known/openid-configuration"
	ctx.Trace("GET " + target)
	if ctx.DryRun {
		return OIDCDiscovery{Issuer: issuer}, nil
	}
	var discovery OIDCDiscovery
	if err := fetchJSON(target, &discovery); err != nil {
		return OIDCDiscovery{}, fmt.Errorf("fetch oidc discovery document: %w", err)
	}
	return discovery, nil
}

func defaultStartERunDeviceAuthorization(ctx Context, discovery OIDCDiscovery, clientID, scope string) (ERunDeviceAuthorization, error) {
	if strings.TrimSpace(discovery.DeviceAuthorizationEndpoint) == "" {
		return ERunDeviceAuthorization{}, fmt.Errorf("issuer %s does not advertise a device authorization endpoint", discovery.Issuer)
	}
	ctx.Trace("POST " + discovery.DeviceAuthorizationEndpoint)
	if ctx.DryRun {
		return ERunDeviceAuthorization{}, nil
	}
	var payload struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	form := url.Values{"client_id": {clientID}, "scope": {scope}}
	if err := postForm(discovery.DeviceAuthorizationEndpoint, form, &payload); err != nil {
		return ERunDeviceAuthorization{}, fmt.Errorf("start device authorization: %w", err)
	}
	if payload.DeviceCode == "" || payload.UserCode == "" {
		return ERunDeviceAuthorization{}, fmt.Errorf("device authorization response is missing device_code or user_code")
	}
	interval := payload.Interval
	if interval <= 0 {
		interval = 5
	}
	return ERunDeviceAuthorization{
		DeviceCode:              payload.DeviceCode,
		UserCode:                payload.UserCode,
		VerificationURI:         payload.VerificationURI,
		VerificationURIComplete: payload.VerificationURIComplete,
		ExpiresIn:               time.Duration(payload.ExpiresIn) * time.Second,
		Interval:                time.Duration(interval) * time.Second,
	}, nil
}

// pollERunDeviceToken polls the token endpoint until the device flow
// completes, honoring RFC 8628's authorization_pending/slow_down/expired_token
// responses. auth.Interval and auth.ExpiresIn are read directly (not
// re-defaulted here) so a caller — including a test — controls the pacing.
func pollERunDeviceToken(ctx Context, discovery OIDCDiscovery, clientID string, auth ERunDeviceAuthorization) (ERunTokens, error) {
	if ctx.DryRun {
		return ERunTokens{}, nil
	}
	interval := auth.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	expiresIn := auth.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 10 * time.Minute
	}
	deadline := time.Now().Add(expiresIn)
	for {
		if time.Now().After(deadline) {
			return ERunTokens{}, fmt.Errorf("device authorization expired before sign-in completed")
		}
		time.Sleep(interval)
		tokens, err := exchangeERunDeviceCode(discovery.TokenEndpoint, clientID, auth.DeviceCode)
		switch {
		case err == nil:
			return tokens, nil
		case errors.Is(err, errERunAuthorizationPending):
			continue
		case errors.Is(err, errERunSlowDown):
			interval += 5 * time.Second
			continue
		default:
			return ERunTokens{}, err
		}
	}
}

func exchangeERunDeviceCode(tokenEndpoint, clientID, deviceCode string) (ERunTokens, error) {
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
		"client_id":   {clientID},
	}
	return postFormForTokens(tokenEndpoint, form)
}

func defaultRefreshERunTokens(ctx Context, discovery OIDCDiscovery, clientID string, refreshToken string) (ERunTokens, error) {
	ctx.Trace("POST " + discovery.TokenEndpoint + " (refresh_token grant)")
	if ctx.DryRun {
		return ERunTokens{}, nil
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	tokens, err := postFormForTokens(discovery.TokenEndpoint, form)
	if err != nil {
		return ERunTokens{}, fmt.Errorf("refresh token: %w", err)
	}
	return tokens, nil
}

// runERunAuthorizationCodeLogin is the Authorization Code + PKCE fallback for
// an issuer that does not advertise a device authorization endpoint. It opens
// a loopback listener for the redirect (no browser is launched automatically;
// the URL is printed for the operator to open) and exchanges the returned
// code for tokens.
func runERunAuthorizationCodeLogin(ctx Context, discovery OIDCDiscovery, clientID, scope string) (ERunTokens, error) {
	if strings.TrimSpace(discovery.AuthorizationEndpoint) == "" {
		return ERunTokens{}, fmt.Errorf("issuer %s does not advertise an authorization endpoint", discovery.Issuer)
	}
	if ctx.DryRun {
		ctx.Trace("open loopback listener for oidc callback")
		return ERunTokens{}, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ERunTokens{}, fmt.Errorf("open loopback listener for oidc callback: %w", err)
	}
	defer func() { _ = listener.Close() }()
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return ERunTokens{}, fmt.Errorf("resolve loopback listener port")
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", tcpAddr.Port)

	verifier, err := erunPKCEVerifier()
	if err != nil {
		return ERunTokens{}, err
	}
	state, err := erunPKCEState()
	if err != nil {
		return ERunTokens{}, err
	}
	authURL := erunAuthorizationCodeURL(discovery, clientID, redirectURI, state, erunPKCEChallenge(verifier), scope)

	code, err := awaitERunOIDCCallback(ctx, listener, state, authURL)
	if err != nil {
		return ERunTokens{}, err
	}
	return exchangeERunAuthorizationCode(discovery.TokenEndpoint, clientID, redirectURI, verifier, code)
}

type erunCallbackResult struct {
	code string
	err  error
}

// awaitERunOIDCCallback does not close the server itself: closing it as soon
// as resultCh receives would race the handler's in-flight response write
// against the shutdown and could hand the browser an aborted connection.
// runERunAuthorizationCodeLogin's own listener.Close(), deferred until after
// the token exchange completes, is what stops it.
func awaitERunOIDCCallback(ctx Context, listener net.Listener, state string, authURL string) (string, error) {
	resultCh := make(chan erunCallbackResult, 1)
	server := &http.Server{Handler: erunCallbackHandler(state, resultCh)}
	go func() { _ = server.Serve(listener) }()

	ctx.Trace("open browser " + authURL)
	writeERunLoginPrompt(ctx, fmt.Sprintf("Open the following URL to sign in:\n\n  %s\n\n", authURL))

	select {
	case result := <-resultCh:
		if result.err != nil {
			return "", result.err
		}
		if result.code == "" {
			return "", fmt.Errorf("oidc callback did not include an authorization code")
		}
		return result.code, nil
	case <-time.After(5 * time.Minute):
		return "", fmt.Errorf("timed out waiting for the browser sign-in to complete")
	}
}

// erunCallbackHandler writes its response fully before signaling resultCh.
// The receiver tears the server down as soon as it reads from resultCh, so
// signaling first would race the in-flight response write against that
// shutdown and could hand the browser an aborted connection.
func erunCallbackHandler(state string, resultCh chan<- erunCallbackResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		switch {
		case query.Get("error") != "":
			_, _ = w.Write([]byte("Sign-in failed. You can close this window."))
			resultCh <- erunCallbackResult{err: fmt.Errorf("sign-in failed: %s", query.Get("error"))}
		case query.Get("state") != state:
			_, _ = w.Write([]byte("Sign-in failed. You can close this window."))
			resultCh <- erunCallbackResult{err: fmt.Errorf("oidc callback state mismatch")}
		default:
			_, _ = w.Write([]byte("Signed in. You can close this window."))
			resultCh <- erunCallbackResult{code: query.Get("code")}
		}
	}
}

func exchangeERunAuthorizationCode(tokenEndpoint, clientID, redirectURI, verifier, code string) (ERunTokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"code":          {code},
		"code_verifier": {verifier},
	}
	tokens, err := postFormForTokens(tokenEndpoint, form)
	if err != nil {
		return ERunTokens{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	return tokens, nil
}

func erunAuthorizationCodeURL(discovery OIDCDiscovery, clientID, redirectURI, state, codeChallenge, scope string) string {
	values := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {scope},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return discovery.AuthorizationEndpoint + "?" + values.Encode()
}

// erunPKCEVerifier generates an RFC 7636 code_verifier.
func erunPKCEVerifier() (string, error) {
	return erunRandomURLSafeString(32)
}

func erunPKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func erunPKCEState() (string, error) {
	return erunRandomURLSafeString(16)
}

func erunRandomURLSafeString(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

const erunHTTPTimeout = 15 * time.Second

func fetchJSON(target string, out any) error {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	return doERunRequest(req, out)
}

func postForm(target string, form url.Values, out any) error {
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return doERunRequest(req, out)
}

func doERunRequest(req *http.Request, out any) error {
	client := &http.Client{Timeout: erunHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return oauthTokenError(resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// postFormForTokens is the shared token-endpoint response shape for the
// device, authorization-code, and refresh-token grants.
func postFormForTokens(tokenEndpoint string, form url.Values) (ERunTokens, error) {
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := postForm(tokenEndpoint, form, &payload); err != nil {
		return ERunTokens{}, err
	}
	return ERunTokens{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		ExpiresIn:    time.Duration(payload.ExpiresIn) * time.Second,
	}, nil
}

// oauthTokenError maps a non-2xx token-endpoint response to one of the RFC
// 8628/6749 error sentinels when recognized, or a generic error carrying the
// server's own message otherwise.
func oauthTokenError(status int, body []byte) error {
	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &payload)
	switch payload.Error {
	case "authorization_pending":
		return errERunAuthorizationPending
	case "slow_down":
		return errERunSlowDown
	case "expired_token":
		return errERunExpiredToken
	case "access_denied":
		return errERunAccessDenied
	case "invalid_scope":
		return errERunInvalidScope
	}
	message := strings.TrimSpace(payload.ErrorDescription)
	if message == "" {
		message = strings.TrimSpace(payload.Error)
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	return fmt.Errorf("http %d: %s", status, message)
}
