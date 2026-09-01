package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// configuredPlatformInfo is every field an operator can set, so a field added
// to the struct without being carried through the handler shows up as a
// zero-valued round trip rather than passing unnoticed.
var configuredPlatformInfo = PlatformInfo{
	Issuer:          "https://auth.example.test",
	APIURL:          "https://api.example.test",
	ConsoleURL:      "https://console.example.test",
	ConsoleClientID: "console-client",
	CLIClientID:     "cli-client",
	MobileClientID:  "mobile-client",
	Brand:           "Example",
	DocsURL:         "https://docs.example.test",
	Tagline:         "Example's own one-line pitch.",
	LogoURL:         "https://cdn.example.test/logo.svg",
}

func TestPlatformRouteServesConfiguredValues(t *testing.T) {
	mux := http.NewServeMux()
	RegisterPlatformRoute(mux, configuredPlatformInfo)

	req := httptest.NewRequest(http.MethodGet, "/v1/platform", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got PlatformInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got != configuredPlatformInfo {
		t.Fatalf("got %+v, want %+v", got, configuredPlatformInfo)
	}
}

// The response's key set is the contract a client parses against, so it is
// asserted on the raw body rather than through PlatformInfo: a client field
// with no server counterpart silently resolves to a bundled default, which is
// exactly the failure this locks down.
func TestPlatformRouteBodyCarriesEveryDiscoveryKey(t *testing.T) {
	mux := http.NewServeMux()
	RegisterPlatformRoute(mux, configuredPlatformInfo)

	req := httptest.NewRequest(http.MethodGet, "/v1/platform", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	wantKeys := []string{
		"issuer", "apiUrl", "consoleUrl", "consoleClientId", "cliClientId", "mobileClientId",
		"brand", "docsUrl", "tagline", "logoUrl",
	}
	for _, key := range wantKeys {
		value, ok := body[key]
		if !ok {
			t.Fatalf("response is missing key %q; got %v", key, body)
		}
		if value == "" {
			t.Fatalf("key %q is empty although it was configured; got %v", key, body)
		}
	}
	if len(body) != len(wantKeys) {
		t.Fatalf("response has %d keys, want exactly %d (%v); got %v", len(body), len(wantKeys), wantKeys, body)
	}
}

// An unset field is an empty string in the body, never an omitted key: a
// client cannot distinguish "this platform sets no tagline" from "this
// platform is too old to have one" if the key disappears, and both must fall
// back to the bundled default rather than fail.
func TestPlatformRouteRendersUnsetFieldsAsEmptyStrings(t *testing.T) {
	mux := http.NewServeMux()
	RegisterPlatformRoute(mux, PlatformInfo{Issuer: "https://auth.example.test"})

	req := httptest.NewRequest(http.MethodGet, "/v1/platform", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, key := range []string{"docsUrl", "tagline", "logoUrl"} {
		value, ok := body[key]
		if !ok {
			t.Fatalf("unset key %q must still be present in the body; got %v", key, body)
		}
		if value != "" {
			t.Fatalf("unset key %q = %v, want an empty string", key, value)
		}
	}
}

func TestPlatformRouteServesEmptyValuesWithoutError(t *testing.T) {
	mux := http.NewServeMux()
	RegisterPlatformRoute(mux, PlatformInfo{})

	req := httptest.NewRequest(http.MethodGet, "/v1/platform", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got PlatformInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got != (PlatformInfo{}) {
		t.Fatalf("got %+v, want zero-valued PlatformInfo", got)
	}
}

func TestPlatformRouteIsUnauthenticated(t *testing.T) {
	// RegisterPlatformRoute is called with a plain *http.ServeMux, never a
	// ProtectedRouteRegistrar — this test exists to keep that contract from
	// silently regressing behind a future refactor.
	mux := http.NewServeMux()
	RegisterPlatformRoute(mux, PlatformInfo{Issuer: "https://auth.example.test"})

	req := httptest.NewRequest(http.MethodGet, "/v1/platform", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with no auth middleware involved", rec.Code)
	}
}
