package routes

import "net/http"

// PlatformInfo is this instance's own self-describing config: the values a
// client needs to authenticate against it and reach its console/API, without
// hardcoding any instance's name. Every field is optional; an absent value
// renders as an empty string.
type PlatformInfo struct {
	// Issuer is this platform's OIDC issuer URL (its hosted IdP).
	Issuer string `json:"issuer"`
	// APIURL is this instance's own API base URL.
	APIURL string `json:"apiUrl"`
	// ConsoleURL is this instance's hosted web console URL.
	ConsoleURL string `json:"consoleUrl"`
	// ConsoleClientID is the OIDC client id the hosted console authenticates
	// with (Authorization Code + PKCE).
	ConsoleClientID string `json:"consoleClientId"`
	// CLIClientID is the OIDC client id an erun CLI/agent authenticates with
	// (Device Authorization Grant, with an Authorization Code + PKCE fallback).
	CLIClientID string `json:"cliClientId"`
	// Brand is this instance's display name, if the operator set one.
	Brand string `json:"brand"`
}

// RegisterPlatformRoute registers GET /v1/platform directly on the mux,
// unauthenticated: a client needs this instance's own config before it has a
// token to authenticate with.
func RegisterPlatformRoute(mux *http.ServeMux, info PlatformInfo) {
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, info)
	})
}
