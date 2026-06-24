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

// MCP auth edge (issue #655). The per-env erun-mcp server is exposed publicly
// (Traefik routes it at mcp.<tenant>-<env>.services.<base-domain>) and its `raw`
// tool can kubectl-exec, so it must always be authenticated once a trust anchor
// is configured.
//
// Auth mirrors the erun api's model (erun-backend-api): a key/value map of
// trusted issuer → tenant. A bearer token is accepted only when its verified
// `iss` is a configured trusted issuer; the mapped value is the tenant the
// token authenticates, so the tenant is identified per URL exactly as the api
// resolves it from the issuer. An issuer is either a `file://<path>` desktop
// public key (verified by erun-common's Ed25519 verifier) — the desktop case —
// or, later, an OIDC issuer URL. The crypto verification lives in erun-common
// so the desktop signer and this verifier share one contract.
const (
	// envMCPTrustedIssuers is a JSON object mapping each trusted issuer to the
	// tenant it authenticates: {"file:///etc/erun/mcp-auth/desktopid.pub":"acme"}.
	envMCPTrustedIssuers = "ERUN_MCP_TRUSTED_ISSUERS"
	// envMCPTrustedIssuer is single-issuer sugar: the one trusted issuer, mapped
	// to the env's own tenant (ERUN_TENANT). Convenient for the common
	// one-tenant-per-server case; ERUN_MCP_TRUSTED_ISSUERS wins when both are set.
	envMCPTrustedIssuer = "ERUN_MCP_TRUSTED_ISSUER"
	// envMCPAudience optionally pins the expected token audience.
	envMCPAudience = "ERUN_MCP_AUDIENCE"
	// envTenant is the env's own tenant, used as the value for the single-issuer
	// sugar form.
	envTenant = "ERUN_TENANT"
)

type mcpAuthConfig struct {
	// trustedIssuers maps each trusted token issuer (the key — a file:// public
	// key path or an OIDC issuer URL) to the tenant it authenticates (the value),
	// mirroring the erun api's issuer→tenant model. A token is accepted only when
	// its verified iss is a key here; the value is the resolved tenant.
	trustedIssuers map[string]string
	audience       string
}

// mcpAuthConfigFromEnv reads the auth configuration the chart wires onto the
// erun-mcp container. An empty map means no auth is configured — the legacy
// loopback-only behavior — so existing deployments that have not yet injected a
// desktop key keep working; a desktop deployment always sets it, so its MCP
// edge is always authenticated.
func mcpAuthConfigFromEnv() mcpAuthConfig {
	cfg := mcpAuthConfig{
		trustedIssuers: map[string]string{},
		audience:       strings.TrimSpace(os.Getenv(envMCPAudience)),
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
	// Single-issuer sugar: maps the one trusted issuer to the env's own tenant.
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

// authHTTPMiddleware rejects unauthenticated requests with 401 when auth is
// configured. It reads the (unverified) issuer to select the trusted entry,
// verifies the bearer JWT against that issuer's key, and resolves the tenant
// from the map. When auth is not configured it passes through unchanged.
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
		if _, err := eruncommon.VerifyMCPToken(token, issuer, cfg.audience, time.Now()); err != nil {
			writeUnauthorized(w, err.Error())
			return
		}
		// Tenant identified per URL from the issuer→tenant map (mirrors the erun
		// api). Carry it on the request so tools/audit can read the authenticated
		// tenant.
		next.ServeHTTP(w, requestWithAuthTenant(req, tenant))
	})
}

// authTenantHeader carries the tenant the auth edge resolved for the request.
const authTenantHeader = "X-Erun-Auth-Tenant"

func requestWithAuthTenant(req *http.Request, tenant string) *http.Request {
	if tenant == "" {
		return req
	}
	req.Header.Set(authTenantHeader, tenant)
	return req
}

// bearerToken extracts the token from an `Authorization: Bearer <token>` header
// (the scheme match is case-insensitive per RFC 7235).
func bearerToken(req *http.Request) string {
	header := strings.TrimSpace(req.Header.Get("Authorization"))
	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func writeUnauthorized(w http.ResponseWriter, reason string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized: "+reason, http.StatusUnauthorized)
}
