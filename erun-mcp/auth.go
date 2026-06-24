package erunmcp

import (
	"net/http"
	"os"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// MCP auth edge (issue #655). The per-env erun-mcp server is exposed publicly
// (Traefik routes it at mcp.<tenant>-<env>.services.<base-domain>) and its `raw`
// tool can kubectl-exec, so it must always be authenticated once a trust anchor
// is configured. authHTTPMiddleware verifies a bearer JWT against a trusted
// file:// issuer (the desktop's injected public key); the verification itself
// lives in erun-common so the desktop signer and this verifier share one
// contract.
const (
	// envMCPTrustedIssuer names the file://<path> issuer whose public key the
	// server loads to verify bearer tokens. When set, auth is REQUIRED.
	envMCPTrustedIssuer = "ERUN_MCP_TRUSTED_ISSUER"
	// envMCPAudience optionally pins the expected token audience.
	envMCPAudience = "ERUN_MCP_AUDIENCE"
)

type mcpAuthConfig struct {
	trustedIssuer string
	audience      string
}

// mcpAuthConfigFromEnv reads the auth configuration the chart wires onto the
// erun-mcp container. An empty trusted issuer means no auth is configured —
// the legacy loopback-only behavior — so existing deployments that have not yet
// injected a desktop key keep working; a desktop deployment always sets it, so
// its MCP edge is always authenticated.
func mcpAuthConfigFromEnv() mcpAuthConfig {
	return mcpAuthConfig{
		trustedIssuer: strings.TrimSpace(os.Getenv(envMCPTrustedIssuer)),
		audience:      strings.TrimSpace(os.Getenv(envMCPAudience)),
	}
}

func (c mcpAuthConfig) enabled() bool {
	return c.trustedIssuer != ""
}

// authHTTPMiddleware rejects unauthenticated requests with 401 when auth is
// configured, verifying the bearer JWT against the trusted file:// issuer's
// public key. When auth is not configured it passes through unchanged.
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
		if _, err := eruncommon.VerifyMCPToken(token, cfg.trustedIssuer, cfg.audience, time.Now()); err != nil {
			writeUnauthorized(w, err.Error())
			return
		}
		next.ServeHTTP(w, req)
	})
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
