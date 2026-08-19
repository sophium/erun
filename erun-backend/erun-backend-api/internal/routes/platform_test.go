package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlatformRouteServesConfiguredValues(t *testing.T) {
	mux := http.NewServeMux()
	RegisterPlatformRoute(mux, PlatformInfo{
		Issuer:          "https://auth.example.test",
		APIURL:          "https://api.example.test",
		ConsoleURL:      "https://console.example.test",
		ConsoleClientID: "console-client",
		CLIClientID:     "cli-client",
		Brand:           "Example",
	})

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
	want := PlatformInfo{
		Issuer:          "https://auth.example.test",
		APIURL:          "https://api.example.test",
		ConsoleURL:      "https://console.example.test",
		ConsoleClientID: "console-client",
		CLIClientID:     "cli-client",
		Brand:           "Example",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
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
