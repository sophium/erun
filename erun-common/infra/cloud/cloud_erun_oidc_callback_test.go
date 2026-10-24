package cloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

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
		tokens, err := runERunAuthorizationCodeLogin(core.Context{Stdout: stdout}, discovery, clientID, erunOAuthScope)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- tokens
	}()
	return resultCh, errCh
}

func TestRunERunAuthorizationCodeLoginRoundTrip(t *testing.T) {
	t.Setenv("ERUN_HOST_OS_OVERRIDE", "linux")
	t.Setenv("ERUN_XDG_OPEN_BIN", writeExecutableStub(t, 0))
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

func TestERunLoginBrowserCommandPerOS(t *testing.T) {
	const authURL = "https://auth.example.test/authorize?state=1"
	cases := []struct {
		hostOS     core.HostOS
		executable string
		args       []string
	}{
		{core.HostOSDarwin, "open", []string{authURL}},
		{core.HostOSLinux, "xdg-open", []string{authURL}},
		{core.HostOSWindows, "cmd", []string{"/c", "start", "", authURL}},
	}
	for _, c := range cases {
		executable, args, err := erunLoginBrowserCommand(c.hostOS, authURL)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.hostOS, err)
		}
		if executable != c.executable {
			t.Fatalf("%s: executable = %q, want %q", c.hostOS, executable, c.executable)
		}
		if !slices.Equal(args, c.args) {
			t.Fatalf("%s: args = %v, want %v", c.hostOS, args, c.args)
		}
	}
	if _, _, err := erunLoginBrowserCommand(core.HostOSUnknown, authURL); err == nil {
		t.Fatal("expected an unsupported host OS to error")
	}
}

// writeExecutableStub writes a script that exits immediately with exitCode,
// for pointing an ERUN_<NAME>_BIN override at a fake browser opener without a
// live desktop session.
func writeExecutableStub(t *testing.T, exitCode int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "browser-opener-stub")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("#!/bin/sh\nexit %d\n", exitCode)), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func TestOpenERunLoginBrowserSucceedsAndFailsNonFatally(t *testing.T) {
	t.Setenv("ERUN_HOST_OS_OVERRIDE", "linux")

	t.Setenv("ERUN_XDG_OPEN_BIN", writeExecutableStub(t, 0))
	if err := openERunLoginBrowser("https://auth.example.test/authorize"); err != nil {
		t.Fatalf("expected the stub launch to succeed, got: %v", err)
	}

	t.Setenv("ERUN_XDG_OPEN_BIN", filepath.Join(t.TempDir(), "does-not-exist"))
	if err := openERunLoginBrowser("https://auth.example.test/authorize"); err == nil {
		t.Fatal("expected a missing browser opener to fail")
	}
}

// TestAwaitERunOIDCCallbackReportsBrowserLaunchOutcome pins the trace/prompt
// contract the desktop and CLI both rely on: the operator is told what
// actually happened, and a launch failure never blocks the callback wait
// that follows.
func TestAwaitERunOIDCCallbackReportsBrowserLaunchOutcome(t *testing.T) {
	t.Setenv("ERUN_HOST_OS_OVERRIDE", "linux")
	tokenSrv := httptest.NewServer(authorizationCodeExchangeHandler(t, "auth-code-2", ERunTokens{AccessToken: "access-2", RefreshToken: "refresh-2", ExpiresIn: time.Hour}))
	defer tokenSrv.Close()
	discovery := OIDCDiscovery{AuthorizationEndpoint: "https://auth.example.test/authorize", TokenEndpoint: tokenSrv.URL}

	completeLoginAndSnapshot := func(t *testing.T) string {
		t.Helper()
		stdout := &syncBuffer{}
		resultCh, errCh := startERunAuthorizationCodeLogin(discovery, "cli-1", stdout)
		authURL := stdout.awaitURL(t, 2*time.Second)
		redirectURI, state := mustParseRedirectAndState(t, authURL)
		resp, err := http.Get(fmt.Sprintf("%s?code=auth-code-2&state=%s", redirectURI, state))
		if err != nil {
			t.Fatalf("simulate browser callback GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		select {
		case <-resultCh:
		case err := <-errCh:
			t.Fatalf("runERunAuthorizationCodeLogin: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for the login flow to complete after the callback")
		}
		return stdout.snapshot()
	}

	t.Run("browser opens", func(t *testing.T) {
		t.Setenv("ERUN_XDG_OPEN_BIN", writeExecutableStub(t, 0))
		prompt := completeLoginAndSnapshot(t)
		if !strings.Contains(prompt, "Opened your browser to sign in") {
			t.Fatalf("expected an opened-browser prompt, got:\n%s", prompt)
		}
		if strings.Contains(prompt, "Could not open a browser") {
			t.Fatalf("did not expect a fallback prompt, got:\n%s", prompt)
		}
	})

	t.Run("browser fails to open", func(t *testing.T) {
		t.Setenv("ERUN_XDG_OPEN_BIN", filepath.Join(t.TempDir(), "does-not-exist"))
		prompt := completeLoginAndSnapshot(t)
		if !strings.Contains(prompt, "Could not open a browser automatically") {
			t.Fatalf("expected a fallback prompt, got:\n%s", prompt)
		}
		if !strings.Contains(prompt, "https://auth.example.test/authorize") {
			t.Fatalf("expected the fallback prompt to still carry the sign-in url, got:\n%s", prompt)
		}
	})
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

func (b *syncBuffer) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
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

// TestAwaitERunOIDCCallbackTimesOutNamingTheListener locks the failure a
// too-short wait produces. The symptom shows up on the browser's side - the
// sign-in completes, the redirect hits a closed port, and it reads as a broken
// redirect URI - so the error has to name the listener and say to re-run,
// otherwise the operator debugs the wrong thing.
func TestAwaitERunOIDCCallbackTimesOutNamingTheListener(t *testing.T) {
	// This drives the real wait, which opens the operator's browser at the
	// authorize URL as its first act. Left unstubbed the suite launches the
	// machine's actual browser at a fixture hostname on every run -- an
	// outward side effect from a unit test, and on a laptop the operator sees a
	// stray tab resolving nowhere. The override is the seam the opener's own
	// doc names for exactly this, so the flow under test is unchanged and only
	// the launch is stubbed.
	t.Setenv("ERUN_HOST_OS_OVERRIDE", "linux")
	t.Setenv("ERUN_XDG_OPEN_BIN", writeExecutableStub(t, 0))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	code, err := awaitERunOIDCCallback(core.Context{}, listener, "state-1", "https://issuer.test/authorize", time.Millisecond)
	if err == nil {
		t.Fatalf("expected a timeout error, got code %q", code)
	}
	for _, want := range []string{"timed out after 1ms", "loopback listener", "Re-run the login"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// The default has to stay long enough for a person who is handed the URL and
// opens it later; five minutes expired routinely in exactly that case.
func TestERunLoginCallbackTimeoutLeavesRoomForAHumanSignIn(t *testing.T) {
	if erunLoginCallbackTimeout < 10*time.Minute {
		t.Fatalf("erunLoginCallbackTimeout = %s, want at least 10m for an asynchronous sign-in", erunLoginCallbackTimeout)
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
		if !errors.Is(got, want) {
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

// The login flow opens the authorize URL with the host's own opener, and a
// test that forgot to stub it launched the operator's real browser at a
// fixture hostname -- invisible in the Linux gate, a stray tab on a desktop.
// With no per-test stub, every platform's opener must resolve to the inert
// binary the package's TestMain installs.
func TestNoTestCanReachTheHostBrowserOpener(t *testing.T) {
	inert, err := exec.LookPath("true")
	if err != nil {
		t.Fatalf("look up inert binary: %v", err)
	}
	for _, hostOS := range []core.HostOS{core.HostOSDarwin, core.HostOSLinux, core.HostOSWindows} {
		executable, args, err := erunLoginBrowserCommand(hostOS, "https://auth.example.test/authorize")
		if err != nil {
			t.Fatalf("%s: %v", hostOS, err)
		}
		if got := core.Command(executable, args...).Path; got != inert {
			t.Fatalf("%s opener %q resolves to %q, want the inert %q", hostOS, executable, got, inert)
		}
	}
}
