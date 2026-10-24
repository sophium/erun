package shared

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
)

// The per-env erun-mcp server is exposed publicly and its `raw` tool can
// kubectl-exec, so once a trust anchor is configured every request must be
// authenticated. Auth mirrors the erun api's issuer→tenant model, and the
// crypto verification lives in erun-common so the desktop signer and this
// verifier share one contract.
const (
	EnvMCPTrustedIssuers = "ERUN_MCP_TRUSTED_ISSUERS"
	EnvMCPTrustedIssuer  = "ERUN_MCP_TRUSTED_ISSUER"
	EnvMCPAudience       = "ERUN_MCP_AUDIENCE"
	EnvTenant            = "ERUN_TENANT"
)

type McpAuthConfig struct {
	trustedIssuers map[string]string
	audience       string
	tenant         string
	oidc           *mcpauth.OIDCVerifier
}

// McpAuthConfigFromEnv builds the auth config from the environment. An empty
// result leaves auth unconfigured — the legacy loopback-only behavior — so
// deployments that predate desktop-key injection keep working; a desktop
// deployment always sets a trust anchor, so its edge stays authenticated.
func McpAuthConfigFromEnv() McpAuthConfig {
	cfg := McpAuthConfig{
		trustedIssuers: map[string]string{},
		audience:       strings.TrimSpace(os.Getenv(EnvMCPAudience)),
		tenant:         strings.TrimSpace(os.Getenv(EnvTenant)),
		oidc:           mcpauth.NewOIDCVerifier(),
	}
	if raw := strings.TrimSpace(os.Getenv(EnvMCPTrustedIssuers)); raw != "" {
		parsed := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			log.Printf("erun-mcp auth: ignoring malformed %s: %v", EnvMCPTrustedIssuers, err)
		} else {
			for issuer, tenant := range parsed {
				if issuer = strings.TrimSpace(issuer); issuer != "" {
					cfg.trustedIssuers[issuer] = strings.TrimSpace(tenant)
				}
			}
		}
	}
	if single := strings.TrimSpace(os.Getenv(EnvMCPTrustedIssuer)); single != "" {
		if _, ok := cfg.trustedIssuers[single]; !ok {
			cfg.trustedIssuers[single] = strings.TrimSpace(os.Getenv(EnvTenant))
		}
	}
	return cfg
}

func (c McpAuthConfig) enabled() bool {
	return len(c.trustedIssuers) > 0
}

func AuthHTTPMiddleware(cfg McpAuthConfig, next http.Handler) http.Handler {
	return authHTTPMiddlewareWithTokenResolver(cfg, bearerToken, next)
}

// WsAttachAuthHTTPMiddleware is the attach route's own auth wrapper: it
// resolves the bearer token the same way authHTTPMiddleware does, but falls
// back to a WebSocket-subprotocol-carried token (attachSubprotocolBearerToken)
// when no Authorization header is present. A browser's WebSocket constructor
// cannot set an Authorization header on the handshake at all -- the
// subprotocol list is the only header-bearing surface it exposes -- so
// without this fallback a browser client could never authenticate to this
// route, regardless of capability. Every other route keeps the header-only
// resolver; this wider acceptance is scoped to the one route that needs it.
func WsAttachAuthHTTPMiddleware(cfg McpAuthConfig, next http.Handler) http.Handler {
	return authHTTPMiddlewareWithTokenResolver(cfg, resolveAttachBearerToken, next)
}

func resolveAttachBearerToken(req *http.Request) string {
	if token := bearerToken(req); token != "" {
		return token
	}
	return attachSubprotocolBearerToken(req)
}

func authHTTPMiddlewareWithTokenResolver(cfg McpAuthConfig, resolveToken func(*http.Request) string, next http.Handler) http.Handler {
	if !cfg.enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		token := resolveToken(req)
		if token == "" {
			writeUnauthorized(w, "a bearer token is required")
			return
		}
		issuer, err := mcpauth.UnverifiedMCPTokenIssuer(token)
		if err != nil {
			writeUnauthorized(w, err.Error())
			return
		}
		tenant, trusted := cfg.trustedIssuers[issuer]
		if !trusted {
			writeUnauthorized(w, "token issuer is not a trusted MCP issuer")
			return
		}
		// A per-env edge serves exactly one tenant; a token resolving to another
		// tenant means the trusted-issuer map is misconfigured for this env.
		if cfg.tenant != "" && tenant != cfg.tenant {
			writeUnauthorized(w, "token tenant does not match this environment")
			return
		}
		claims, err := mcpauth.VerifyMCPToken(req.Context(), cfg.oidc, token, issuer, cfg.audience, time.Now())
		if err != nil {
			writeUnauthorized(w, err.Error())
			return
		}
		// Authentication is done; what the caller may do travels with the request
		// from here, so the tool surface can be built for this caller rather than
		// filtered after the fact.
		identity := AuthIdentity{Tenant: tenant, User: claims.Subject, Capabilities: claims.Capabilities()}
		if identity.Capabilities.Empty() {
			WriteForbidden(w, "token grants no erun capabilities")
			return
		}
		req = requestWithAuthTenant(req, tenant)
		next.ServeHTTP(w, req.WithContext(WithAuthIdentity(req.Context(), identity)))
	})
}

const authTenantHeader = "X-Erun-Auth-Tenant"

func requestWithAuthTenant(req *http.Request, tenant string) *http.Request {
	if tenant == "" {
		return req
	}
	req.Header.Set(authTenantHeader, tenant)
	return req
}

// bearerToken matches the auth scheme case-insensitively per RFC 7235.
func bearerToken(req *http.Request) string {
	header := strings.TrimSpace(req.Header.Get("Authorization"))
	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// AttachAuthSubprotocol is the Sec-WebSocket-Protocol scheme name a browser
// attach client offers alongside its bearer token, e.g.
// new WebSocket(url, [attachAuthSubprotocol, token]) -- the RFC 6455 handshake
// joins that array into one comma-separated header, which is the only place a
// browser's WebSocket constructor lets a caller put a credential; it exposes
// no way to set an Authorization header. The scheme name is versioned so a
// future change to how the token is framed can't be silently misparsed by an
// old client.
const AttachAuthSubprotocol = "erun.bearer.v1"

// attachSubprotocolBearerToken extracts a bearer token from a two-entry
// Sec-WebSocket-Protocol offer whose first entry is attachAuthSubprotocol.
// Anything else -- no header, wrong scheme, wrong arity -- resolves to "",
// which resolveAttachBearerToken then reports as the same "a bearer token is
// required" 401 a missing Authorization header produces: a malformed offer is
// refused the ordinary way, not with an error that leaks which channel was tried.
func attachSubprotocolBearerToken(req *http.Request) string {
	protocols := websocket.Subprotocols(req)
	if len(protocols) != 2 || protocols[0] != AttachAuthSubprotocol {
		return ""
	}
	return strings.TrimSpace(protocols[1])
}

// A caller whose token is valid but grants nothing is authenticated and not
// authorized, which is 403 rather than 401: retrying with the same credentials
// is pointless, and saying so is the difference between "log in again" and "ask
// for a role".
func WriteForbidden(w http.ResponseWriter, reason string) {
	http.Error(w, "forbidden: "+reason, http.StatusForbidden)
}

func writeUnauthorized(w http.ResponseWriter, reason string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized: "+reason, http.StatusUnauthorized)
}
