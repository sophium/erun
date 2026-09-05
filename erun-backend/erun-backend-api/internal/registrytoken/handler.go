// Package registrytoken serves the hosted container registry's v2 bearer
// token endpoint. A docker/OCI client pushing or pulling against the
// registry gets challenged with `WWW-Authenticate: Bearer realm=...,
// service=...,scope=...`; it then presents HTTP Basic credentials to realm —
// username is a fixed, documented sentinel, and the password is the tenant's
// own erun-api bearer token — and gets back a short-lived, scope-limited JWT
// it presents to the registry as a normal bearer token.
//
// This is the security boundary of the hosted-registry feature: the tenant is
// resolved only from the verified token's issuer, never from anything
// client-supplied (the username, or the requested scope's repository name),
// and the requested scope is clamped to that tenant's own namespace before
// anything is signed. See
// https://distribution.github.io/distribution/spec/auth/token/.
package registrytoken

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mcptoken"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// TokenVerifier verifies the tenant's own erun-api bearer token, presented as
// the HTTP Basic password. Satisfied by *backendapi.BearerTokenVerifier — the
// exact same verifier (and configured audience) the platform API itself
// authenticates with, so a registry-token request is held to the same bar as
// any other authenticated API call.
type TokenVerifier interface {
	VerifyBearerToken(ctx context.Context, token string) (security.Claims, error)
}

// TenantResolver maps verified claims to the tenant they authenticate as.
// Satisfied by *repository.IdentityRepository. It never creates a tenant or
// user as a side effect of minting a registry token.
type TenantResolver interface {
	ResolveTenantByIssuer(ctx context.Context, claims security.Claims) (model.Tenant, error)
}

// Signer mints the short-lived registry access token. Satisfied by
// *mcptoken.Signer.
type Signer interface {
	SignRegistry(subject, service string, access []mcptoken.RegistryAccessScope, now time.Time) (string, error)
}

// Handler serves GET /v2/token.
type Handler struct {
	verifier TokenVerifier
	tenants  TenantResolver
	signer   Signer
	now      func() time.Time
}

func NewHandler(verifier TokenVerifier, tenants TenantResolver, signer Signer) *Handler {
	return &Handler{verifier: verifier, tenants: tenants, signer: signer, now: time.Now}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /v2/token", http.HandlerFunc(h.serveToken))
}

type tokenResponse struct {
	Token string `json:"token"`
	// AccessToken duplicates Token: some clients read one field name, some the
	// other, per the registry token spec's own note that implementations vary.
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	IssuedAt    string `json:"issued_at"`
}

func (h *Handler) serveToken(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	tenant, ok := h.resolveTenant(w, r, claims)
	if !ok {
		return
	}

	service := strings.TrimSpace(r.URL.Query().Get("service"))
	if service == "" {
		http.Error(w, "missing service parameter", http.StatusBadRequest)
		return
	}

	requested := mcptoken.ParseRegistryTokenScopes(r.URL.Query()["scope"])
	granted := mcptoken.ClampRegistryScopesToTenant(tenant.Name, requested)

	now := h.now()
	token, err := h.signer.SignRegistry(claims.Subject, service, granted, now)
	if err != nil {
		log.Printf("erun registry token mint failed tenant=%q reason=%q", tenant.Name, err.Error())
		http.Error(w, "token mint failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, tokenResponse{
		Token:       token,
		AccessToken: token,
		ExpiresIn:   int(registryTokenTTLSeconds),
		IssuedAt:    now.UTC().Format(time.RFC3339),
	})
}

// registryTokenTTLSeconds mirrors mcptoken's registryTokenTTL; it is
// duplicated here (rather than imported) because it is response metadata for
// the client's own refresh timing, not something this handler enforces —
// mcptoken.Signer alone owns the token's real `exp`.
const registryTokenTTLSeconds = 5 * 60

// authenticate verifies the HTTP Basic password as the tenant's own erun-api
// bearer token. The username is a documented sentinel and is never inspected:
// trusting it would let a client claim any tenant merely by naming it.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (security.Claims, bool) {
	_, password, ok := r.BasicAuth()
	password = strings.TrimSpace(password)
	if !ok || password == "" {
		writeUnauthorized(w)
		return security.Claims{}, false
	}
	claims, err := h.verifier.VerifyBearerToken(r.Context(), password)
	if err != nil || strings.TrimSpace(claims.Issuer) == "" || strings.TrimSpace(claims.Subject) == "" {
		reason := "invalid bearer token"
		if err != nil {
			reason = err.Error()
		}
		log.Printf("erun registry token rejected reason=%q", reason)
		writeUnauthorized(w)
		return security.Claims{}, false
	}
	return claims, true
}

// resolveTenant maps the verified claims to a tenant, strictly from the
// token's own issuer — never from the request's username or scope, both of
// which are client-supplied and therefore untrusted for this decision.
func (h *Handler) resolveTenant(w http.ResponseWriter, r *http.Request, claims security.Claims) (model.Tenant, bool) {
	tenant, err := h.tenants.ResolveTenantByIssuer(r.Context(), claims)
	if err != nil || strings.TrimSpace(tenant.Name) == "" {
		reason := "tenant not resolved"
		if err != nil {
			reason = err.Error()
		}
		log.Printf("erun registry token rejected issuer=%q reason=%q", claims.Issuer, reason)
		writeUnauthorized(w)
		return model.Tenant{}, false
	}
	return tenant, true
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="erun registry token service"`)
	http.Error(w, "invalid bearer token", http.StatusUnauthorized)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
