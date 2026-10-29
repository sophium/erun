package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// configuredPlatformInfo sets every field so a dropped field shows up as zero.
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
	Version:         "1.0.221",
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

// Asserted on the raw body: a missing key silently resolves to a client default.
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
		"brand", "docsUrl", "tagline", "logoUrl", "version",
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
	mux := http.NewServeMux()
	RegisterPlatformRoute(mux, PlatformInfo{Issuer: "https://auth.example.test"})

	req := httptest.NewRequest(http.MethodGet, "/v1/platform", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with no auth middleware involved", rec.Code)
	}
}
