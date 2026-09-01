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
	// MobileClientID is the OIDC client id a mobile companion client
	// authenticates with (Authorization Code + PKCE, no device code — a
	// mobile client always has a system browser to redirect through). Empty
	// until an operator configures a mobile client's redirect URI
	// (zitadel.oidc.mobileRedirectUris): no erun-mobile app is minted until
	// then, since its custom URL scheme belongs to whatever client ships.
	MobileClientID string `json:"mobileClientId"`
	// Brand is this instance's display name, if the operator set one.
	Brand string `json:"brand"`
	// DocsURL is the documentation site this instance's own surfaces link to,
	// so a client's front door points at the operator's docs instead of the
	// vendor's.
	DocsURL string `json:"docsUrl"`
	// Tagline is the one-line pitch a client's landing page leads with; unset
	// leaves the client's bundled product default in place.
	Tagline string `json:"tagline"`
	// LogoURL is an absolute URL to this instance's logo. Absolute rather than
	// a path this API serves, because a brand asset does not live in the
	// console image — one built image serves every instance.
	LogoURL string `json:"logoUrl"`
	// Version is the build actually serving this request, not the release a
	// tag names or a client's own compiled-in version — a tag can exist
	// before any deployment serves it, and a field a build predates is
	// silently ignored rather than rejected, so this is the only
	// non-destructive way to answer "is fix X actually deployed here". Stamped
	// in via -ldflags at image build time, the same mechanism `erun version`
	// uses. Unlike the other fields above, an unresolved value renders as
	// "dev" rather than an empty string: an empty version reads as "no build
	// happened", where the true state is "this binary was never stamped with
	// one" (a plain `go build` outside the release pipeline) -- a distinction
	// this field must preserve rather than collapse into a blank that looks
	// like a missing feature.
	Version string `json:"version"`
}

// RegisterPlatformRoute registers GET /v1/platform directly on the mux,
// unauthenticated: a client needs this instance's own config before it has a
// token to authenticate with.
func RegisterPlatformRoute(mux *http.ServeMux, info PlatformInfo) {
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, info)
	})
}
