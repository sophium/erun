package cloud

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

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// OIDCDiscovery is the subset of the OIDC discovery document the device and PKCE flows need.
type OIDCDiscovery struct {
	Issuer                      string `json:"issuer"`
	AuthorizationEndpoint       string `json:"authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint,omitempty"`
}

// ERunDeviceAuthorization is a device authorization grant's initial response (RFC 8628 3.2).
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

// erunOAuthScope includes offline_access so a session outlives one access token.
const erunOAuthScope = "openid offline_access"

// erunOrgClaimScope asks Zitadel for the resourceowner claim tenant resolution reads;
// issuers that reject it are retried without it.
const erunOrgClaimScope = "urn:zitadel:iam:user:resourceowner"

// erunLoginScope appends operator scopes, deduplicated; reserved scopes are often
// missing from scopes_supported, so they must be requestable by name.
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

// isERunInvalidScopeError covers both the token-endpoint sentinel and the PKCE
// redirect's error parameter, which arrives as a plain string.
func isERunInvalidScopeError(err error) bool {
	return err != nil && (errors.Is(err, errERunInvalidScope) || strings.Contains(err.Error(), "invalid_scope"))
}

func defaultFetchPlatformInfo(ctx core.Context, apiURL string) (platformclient.PlatformInfo, error) {
	target := strings.TrimRight(strings.TrimSpace(apiURL), "/")
	ctx.Trace("GET " + target + "/v1/platform")
	if ctx.DryRun {
		return platformclient.PlatformInfo{}, nil
	}
	info, err := platformclient.NewPlatformClient(target, nil).Platform(context.Background())
	if err != nil {
		return platformclient.PlatformInfo{}, fmt.Errorf("fetch erun platform info: %w", err)
	}
	return info, nil
}

func defaultFetchOIDCDiscovery(ctx core.Context, issuer string) (OIDCDiscovery, error) {
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

func defaultStartERunDeviceAuthorization(ctx core.Context, discovery OIDCDiscovery, clientID, scope string) (ERunDeviceAuthorization, error) {
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

// pollERunDeviceToken uses auth.Interval and auth.ExpiresIn as given so callers control pacing.
func pollERunDeviceToken(ctx core.Context, discovery OIDCDiscovery, clientID string, auth ERunDeviceAuthorization) (ERunTokens, error) {
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

// defaultClientCredentialsERunTokens runs the client_credentials grant for a machine identity.
// Credentials go in the Authorization header to match the BASIC auth method erun provisions.
// The org-claim scope is retried without it for issuers that reject it.
func defaultClientCredentialsERunTokens(ctx core.Context, discovery OIDCDiscovery, clientID string, clientSecret string) (ERunTokens, error) {
	if strings.TrimSpace(discovery.TokenEndpoint) == "" {
		return ERunTokens{}, fmt.Errorf("issuer %s does not advertise a token endpoint", discovery.Issuer)
	}
	ctx.Trace("POST " + discovery.TokenEndpoint + " (client_credentials grant)")
	if ctx.DryRun {
		return ERunTokens{}, nil
	}
	tokens, err := postClientCredentialsForm(discovery.TokenEndpoint, clientID, clientSecret, erunOrgClaimScope)
	if err == nil || !isERunInvalidScopeError(err) {
		return tokens, err
	}
	ctx.Trace("cloud machine identity: issuer rejected scope " + erunOrgClaimScope + "; retrying without it")
	return postClientCredentialsForm(discovery.TokenEndpoint, clientID, clientSecret, "")
}

func postClientCredentialsForm(tokenEndpoint, clientID, clientSecret, scope string) (ERunTokens, error) {
	form := url.Values{"grant_type": {"client_credentials"}}
	if scope != "" {
		form.Set("scope", scope)
	}
	req, err := http.NewRequest(http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return ERunTokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(clientID, clientSecret)
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := doERunRequest(req, &payload); err != nil {
		return ERunTokens{}, fmt.Errorf("client credentials: %w", err)
	}
	return ERunTokens{
		AccessToken: payload.AccessToken,
		ExpiresIn:   time.Duration(payload.ExpiresIn) * time.Second,
	}, nil
}

func defaultRefreshERunTokens(ctx core.Context, discovery OIDCDiscovery, clientID string, refreshToken string) (ERunTokens, error) {
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

// runERunAuthorizationCodeLogin is the Authorization Code + PKCE flow on a loopback listener.
func runERunAuthorizationCodeLogin(ctx core.Context, discovery OIDCDiscovery, clientID, scope string) (ERunTokens, error) {
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

	code, err := awaitERunOIDCCallback(ctx, listener, state, authURL, erunLoginCallbackTimeout)
	if err != nil {
		return ERunTokens{}, err
	}
	return exchangeERunAuthorizationCode(discovery.TokenEndpoint, clientID, redirectURI, verifier, code)
}

// erunLoginCallbackTimeout is generous because the URL may be handed to a person
// who opens it minutes later.
const erunLoginCallbackTimeout = 15 * time.Minute

type erunCallbackResult struct {
	code string
	err  error
}

// awaitERunOIDCCallback does not close the server: closing on receipt would race the
// handler's response write. timeout is a parameter so tests can reach the expiry branch.
func awaitERunOIDCCallback(ctx core.Context, listener net.Listener, state string, authURL string, timeout time.Duration) (string, error) {
	resultCh := make(chan erunCallbackResult, 1)
	server := &http.Server{Handler: erunCallbackHandler(state, resultCh)}
	go func() { _ = server.Serve(listener) }()

	// A launch failure is non-fatal; the printed URL still works.
	if err := openERunLoginBrowser(authURL); err != nil {
		ctx.Trace("could not open a browser: " + err.Error())
		writeERunLoginPrompt(ctx, fmt.Sprintf("Could not open a browser automatically (%s). Open the following URL to sign in:\n\n  %s\n\n", err.Error(), authURL))
	} else {
		ctx.Trace("opened browser " + authURL)
		writeERunLoginPrompt(ctx, fmt.Sprintf("Opened your browser to sign in. If it did not open, visit:\n\n  %s\n\n", authURL))
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			return "", result.err
		}
		if result.code == "" {
			return "", fmt.Errorf("oidc callback did not include an authorization code")
		}
		return result.code, nil
	case <-time.After(timeout):
		// The URL is not repeated: once the listener is gone it cannot complete.
		return "", fmt.Errorf("timed out after %s waiting for the browser sign-in to complete; the loopback listener that receives the redirect only lives for the duration of this command, so a sign-in finished after that lands on a closed port (the browser shows a connection refused). Re-run the login and complete the sign-in while it waits", timeout)
	}
}

// erunLoginBrowserCommand returns the URL opener per OS; on windows the empty title
// argument keeps start from treating the URL as the window title.
func erunLoginBrowserCommand(hostOS core.HostOS, url string) (string, []string, error) {
	switch hostOS {
	case core.HostOSDarwin:
		return "open", []string{url}, nil
	case core.HostOSLinux:
		return "xdg-open", []string{url}, nil
	case core.HostOSWindows:
		return "cmd", []string{"/c", "start", "", url}, nil
	case core.HostOSUnknown:
	}
	return "", nil, fmt.Errorf("opening a browser is unsupported on %s", hostOS)
}

// openERunLoginBrowser detaches immediately so a hung browser cannot block the callback wait.
func openERunLoginBrowser(url string) error {
	executable, args, err := erunLoginBrowserCommand(core.DetectHost().OS, url)
	if err != nil {
		return err
	}
	cmd := core.Command(executable, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch %s: %w", executable, err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}

// erunCallbackHandler writes its response before signaling resultCh, because the
// receiver shuts the server down on receipt.
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

// postFormForTokens is shared by the device, authorization-code, and refresh-token grants.
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

// oauthTokenError maps a token-endpoint error to an RFC 8628/6749 sentinel when recognized.
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
