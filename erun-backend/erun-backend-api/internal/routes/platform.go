package routes

import "net/http"

// PlatformInfo is this instance's self-describing config. Absent fields render as "".
type PlatformInfo struct {
	Issuer     string `json:"issuer"`
	APIURL     string `json:"apiUrl"`
	ConsoleURL string `json:"consoleUrl"`
	// ConsoleClientID uses Authorization Code + PKCE.
	ConsoleClientID string `json:"consoleClientId"`
	// CLIClientID uses Device Authorization Grant with an Authorization Code + PKCE fallback.
	CLIClientID string `json:"cliClientId"`
	// MobileClientID is empty until an operator configures a mobile redirect URI.
	MobileClientID string `json:"mobileClientId"`
	Brand          string `json:"brand"`
	DocsURL        string `json:"docsUrl"`
	// Tagline unset keeps the client's bundled default.
	Tagline string `json:"tagline"`
	// LogoURL is absolute because one console image serves every instance.
	LogoURL string `json:"logoUrl"`
	// Version is the build serving this request, stamped via -ldflags. Unstamped
	// builds report "dev" rather than "" so the state is not mistaken for missing.
	Version string `json:"version"`
}

// RegisterPlatformRoute is unauthenticated: clients need this config before a token.
func RegisterPlatformRoute(mux *http.ServeMux, info PlatformInfo) {
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, info)
	})
}
