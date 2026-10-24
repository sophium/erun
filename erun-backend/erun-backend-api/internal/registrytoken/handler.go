// Package registrytoken serves the hosted registry's v2 bearer token endpoint.
// The tenant comes only from the verified token's issuer, and the requested
// scope is clamped to that tenant's namespace before signing.
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

// TokenVerifier verifies the erun-api bearer token presented as the Basic password.
type TokenVerifier interface {
	VerifyBearerToken(ctx context.Context, token string) (security.Claims, error)
}

// TenantResolver maps verified claims to a tenant; it never creates one.
type TenantResolver interface {
	ResolveTenantByIssuer(ctx context.Context, claims security.Claims) (model.Tenant, error)
}

// Signer mints the short-lived registry access token.
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
	// AccessToken duplicates Token: clients vary in which field they read.
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

// registryTokenTTLSeconds is advisory response metadata; mcptoken.Signer owns the real exp.
const registryTokenTTLSeconds = 5 * 60

// authenticate never inspects the username: trusting it would let a client claim any tenant.
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

// resolveTenant uses only the token's issuer, never the client-supplied username or scope.
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
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}
