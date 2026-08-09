package erunmcp

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The per-env erun-mcp server is exposed publicly and its `raw` tool can
// kubectl-exec, so once a trust anchor is configured every request must be
// authenticated. Auth mirrors the erun api's issuer→tenant model, and the
// crypto verification lives in erun-common so the desktop signer and this
// verifier share one contract.
const (
	envMCPTrustedIssuers = "ERUN_MCP_TRUSTED_ISSUERS"
	envMCPTrustedIssuer  = "ERUN_MCP_TRUSTED_ISSUER"
	envMCPAudience       = "ERUN_MCP_AUDIENCE"
	envTenant            = "ERUN_TENANT"
)

type mcpAuthConfig struct {
	trustedIssuers map[string]string
	audience       string
	tenant         string
	oidc           *eruncommon.OIDCVerifier
}

// mcpAuthConfigFromEnv builds the auth config from the environment. An empty
// result leaves auth unconfigured — the legacy loopback-only behavior — so
// deployments that predate desktop-key injection keep working; a desktop
// deployment always sets a trust anchor, so its edge stays authenticated.
func mcpAuthConfigFromEnv() mcpAuthConfig {
	cfg := mcpAuthConfig{
		trustedIssuers: map[string]string{},
		audience:       strings.TrimSpace(os.Getenv(envMCPAudience)),
		tenant:         strings.TrimSpace(os.Getenv(envTenant)),
		oidc:           eruncommon.NewOIDCVerifier(),
	}
	if raw := strings.TrimSpace(os.Getenv(envMCPTrustedIssuers)); raw != "" {
		parsed := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			log.Printf("erun-mcp auth: ignoring malformed %s: %v", envMCPTrustedIssuers, err)
		} else {
			for issuer, tenant := range parsed {
				if issuer = strings.TrimSpace(issuer); issuer != "" {
					cfg.trustedIssuers[issuer] = strings.TrimSpace(tenant)
				}
			}
		}
	}
	if single := strings.TrimSpace(os.Getenv(envMCPTrustedIssuer)); single != "" {
		if _, ok := cfg.trustedIssuers[single]; !ok {
			cfg.trustedIssuers[single] = strings.TrimSpace(os.Getenv(envTenant))
		}
	}
	return cfg
}

func (c mcpAuthConfig) enabled() bool {
	return len(c.trustedIssuers) > 0
}

func authHTTPMiddleware(cfg mcpAuthConfig, next http.Handler) http.Handler {
	if !cfg.enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		token := bearerToken(req)
		if token == "" {
			writeUnauthorized(w, "a bearer token is required")
			return
		}
		issuer, err := eruncommon.UnverifiedMCPTokenIssuer(token)
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
		claims, err := eruncommon.VerifyMCPToken(req.Context(), cfg.oidc, token, issuer, cfg.audience, time.Now())
		if err != nil {
			writeUnauthorized(w, err.Error())
			return
		}
		// Authentication is done; what the caller may do travels with the request
		// from here, so the tool surface can be built for this caller rather than
		// filtered after the fact.
		identity := authIdentity{Tenant: tenant, User: claims.Subject, Capabilities: claims.Capabilities()}
		if identity.Capabilities.Empty() {
			writeForbidden(w, "token grants no erun capabilities")
			return
		}
		req = requestWithAuthTenant(req, tenant)
		next.ServeHTTP(w, req.WithContext(withAuthIdentity(req.Context(), identity)))
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

// A caller whose token is valid but grants nothing is authenticated and not
// authorized, which is 403 rather than 401: retrying with the same credentials
// is pointless, and saying so is the difference between "log in again" and "ask
// for a role".
func writeForbidden(w http.ResponseWriter, reason string) {
	http.Error(w, "forbidden: "+reason, http.StatusForbidden)
}

func writeUnauthorized(w http.ResponseWriter, reason string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized: "+reason, http.StatusUnauthorized)
}
