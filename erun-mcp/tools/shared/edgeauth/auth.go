package edgeauth

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/infra/mcpauth"
	"github.com/sophium/erun/erun-mcp/tools/shared/callerid"
)

// The edge is public and `raw` can kubectl-exec, so once a trust anchor is
// configured every request must authenticate.
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

// McpAuthConfigFromEnv builds the auth config; an empty result leaves auth off (loopback-only).
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

// WsAttachAuthHTTPMiddleware also accepts a token in the WebSocket subprotocol,
// since browsers cannot set Authorization on the handshake. Only attach uses it.
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
		// A per-env edge serves one tenant; another tenant means a misconfigured issuer map.
		if cfg.tenant != "" && tenant != cfg.tenant {
			writeUnauthorized(w, "token tenant does not match this environment")
			return
		}
		claims, err := mcpauth.VerifyMCPToken(req.Context(), cfg.oidc, token, issuer, cfg.audience, time.Now())
		if err != nil {
			writeUnauthorized(w, err.Error())
			return
		}
		// Capabilities travel with the request so the tool surface is built per caller.
		identity := callerid.AuthIdentity{Tenant: tenant, User: claims.Subject, Capabilities: claims.Capabilities()}
		if identity.Capabilities.Empty() {
			WriteForbidden(w, "token grants no erun capabilities")
			return
		}
		req = requestWithAuthTenant(req, tenant)
		next.ServeHTTP(w, req.WithContext(callerid.WithAuthIdentity(req.Context(), identity)))
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

// AttachAuthSubprotocol is the scheme a browser offers with its token:
// new WebSocket(url, [AttachAuthSubprotocol, token]).
const AttachAuthSubprotocol = "erun.bearer.v1"

// attachSubprotocolBearerToken returns the token from a [scheme, token] offer, or ""
// so a malformed offer gets the ordinary missing-token 401. It reads the offers as
// the upgrader does, but the header is unauthenticated and can be as large as the
// server admits, so it stops splitting after the two entries it reads.
func attachSubprotocolBearerToken(req *http.Request) string {
	protocols := strings.SplitN(strings.TrimSpace(req.Header.Get("Sec-Websocket-Protocol")), ",", 3)
	if len(protocols) != 2 || strings.TrimSpace(protocols[0]) != AttachAuthSubprotocol {
		return ""
	}
	return strings.TrimSpace(protocols[1])
}

// WriteForbidden answers 403: the token is valid but grants nothing, so retrying won't help.
func WriteForbidden(w http.ResponseWriter, reason string) {
	http.Error(w, "forbidden: "+reason, http.StatusForbidden)
}

func writeUnauthorized(w http.ResponseWriter, reason string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized: "+reason, http.StatusUnauthorized)
}
