package backendapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

var (
	ErrMissingBearerToken = errors.New("missing bearer token")
	ErrInvalidBearerToken = errors.New("invalid bearer token")
	ErrTenantNotResolved  = errors.New("tenant not resolved")
	ErrUserNotResolved    = errors.New("user not resolved")
)

const usernameHintHeader = "X-ERun-Username"

type (
	Claims = security.Claims
	Tenant = model.Tenant
	User   = model.User
)

type Identity struct {
	Tenant Tenant
	User   User
	// Org is the resolved org claim value for an org-scoped issuer; empty for
	// single-tenant issuers.
	Org string
}

type IdentityResolver interface {
	ResolveIdentity(ctx context.Context, claims Claims) (Tenant, User, error)
}

type IdentityResolverFunc func(ctx context.Context, claims Claims) (Tenant, User, error)

func (f IdentityResolverFunc) ResolveIdentity(ctx context.Context, claims Claims) (Tenant, User, error) {
	return f(ctx, claims)
}

// OrgResolver derives the org claim value that, with the issuer, resolves the
// tenant for a shared (org-scoped) issuer; it returns "" for single-tenant or
// unregistered issuers. The org belongs in the identity cache key so a shared
// issuer cannot collide different orgs onto one cached tenant.
type OrgResolver interface {
	ResolveOrg(ctx context.Context, claims Claims) (string, error)
}

type OrgResolverFunc func(ctx context.Context, claims Claims) (string, error)

func (f OrgResolverFunc) ResolveOrg(ctx context.Context, claims Claims) (string, error) {
	return f(ctx, claims)
}

type TokenVerifier interface {
	VerifyBearerToken(ctx context.Context, token string) (Claims, error)
}

type TokenVerifierFunc func(ctx context.Context, token string) (Claims, error)

func (f TokenVerifierFunc) VerifyBearerToken(ctx context.Context, token string) (Claims, error) {
	return f(ctx, token)
}

type TenantResolver interface {
	// ResolveTenantByIssuer maps a verified token to its tenant by (issuer, org);
	// single-tenant issuers ignore the org.
	ResolveTenantByIssuer(ctx context.Context, claims Claims) (Tenant, error)
}

type TenantResolverFunc func(ctx context.Context, claims Claims) (Tenant, error)

func (f TenantResolverFunc) ResolveTenantByIssuer(ctx context.Context, claims Claims) (Tenant, error) {
	return f(ctx, claims)
}

type UserResolver interface {
	ResolveUserByExternalID(ctx context.Context, tenantID string, issuer string, externalID string) (User, error)
}

type UserResolverFunc func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error)

func (f UserResolverFunc) ResolveUserByExternalID(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
	return f(ctx, tenantID, issuer, externalID)
}

type AuditEvent = model.AuditEvent

type AuditLogger interface {
	LogAuditEvent(ctx context.Context, event AuditEvent) error
}

type AuditLoggerFunc func(ctx context.Context, event AuditEvent) error

func (f AuditLoggerFunc) LogAuditEvent(ctx context.Context, event AuditEvent) error {
	return f(ctx, event)
}

type Authorizer interface {
	Authorize(ctx context.Context, method string, apiPath string) error
}

type AuthorizerFunc func(ctx context.Context, method string, apiPath string) error

func (f AuthorizerFunc) Authorize(ctx context.Context, method string, apiPath string) error {
	return f(ctx, method, apiPath)
}

type AuthContext struct {
	Claims Claims
	Tenant Tenant
	User   User
	// Org is the resolved org claim value for an org-scoped issuer; empty for
	// single-tenant issuers.
	Org string
}

type authContextKey struct{}

func AuthFromContext(ctx context.Context) (AuthContext, bool) {
	auth, ok := ctx.Value(authContextKey{}).(AuthContext)
	return auth, ok
}

type AuthMiddleware struct {
	verifier    TokenVerifier
	identities  IdentityResolver
	tenants     TenantResolver
	users       UserResolver
	orgResolver OrgResolver
	cache       *IdentityResolutionCache
	audit       AuditLogger
	authz       Authorizer
}

type AuthMiddlewareOptions struct {
	TokenVerifier    TokenVerifier
	IdentityResolver IdentityResolver
	TenantResolver   TenantResolver
	UserResolver     UserResolver
	OrgResolver      OrgResolver
	IdentityCache    *IdentityResolutionCache
	AuditLogger      AuditLogger
	Authorizer       Authorizer
}

func NewAuthMiddleware(options AuthMiddlewareOptions) (*AuthMiddleware, error) {
	if options.TokenVerifier == nil {
		return nil, errors.New("token verifier is required")
	}
	if options.IdentityResolver == nil && options.TenantResolver == nil {
		return nil, errors.New("tenant resolver is required")
	}
	if options.IdentityResolver == nil && options.UserResolver == nil {
		return nil, errors.New("user resolver is required")
	}
	return &AuthMiddleware{
		verifier:    options.TokenVerifier,
		identities:  options.IdentityResolver,
		tenants:     options.TenantResolver,
		users:       options.UserResolver,
		orgResolver: options.OrgResolver,
		cache:       options.IdentityCache,
		audit:       options.AuditLogger,
		authz:       options.Authorizer,
	}, nil
}

func (m *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := m.authenticate(r)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", err.Error())
			return
		}

		identity, err := m.resolveIdentity(r.Context(), claims)
		if err != nil {
			log.Printf("erun api auth rejected method=%s path=%s issuer=%q subject=%q reason=%q", r.Method, r.URL.Path, claims.Issuer, claims.Subject, err.Error())
			writeAuthError(w, http.StatusUnauthorized, authErrorCode(err), err.Error())
			return
		}

		req := r.WithContext(authenticatedContext(r.Context(), claims, identity))
		if !m.authorize(w, req, identity) {
			return
		}
		_ = m.logAuditEvent(req)
		next.ServeHTTP(w, req)
	})
}

// authenticate verifies the request's bearer token and returns its claims. It
// logs the concrete rejection reason and returns the client-facing error, which
// deliberately hides verifier detail behind ErrInvalidBearerToken.
func (m *AuthMiddleware) authenticate(r *http.Request) (Claims, error) {
	token, err := bearerToken(r.Header.Get("Authorization"))
	if err != nil {
		logAuthRejected(r, err.Error())
		return Claims{}, err
	}
	claims, err := m.verifier.VerifyBearerToken(r.Context(), token)
	if err != nil || strings.TrimSpace(claims.Issuer) == "" || strings.TrimSpace(claims.Subject) == "" {
		reason := ErrInvalidBearerToken.Error()
		if err != nil {
			reason = err.Error()
		}
		logAuthRejected(r, reason)
		return Claims{}, ErrInvalidBearerToken
	}
	return claimsWithUsernameHint(claims, r.Header.Get(usernameHintHeader)), nil
}

func logAuthRejected(r *http.Request, reason string) {
	log.Printf("erun api auth rejected method=%s path=%s reason=%q", r.Method, r.URL.Path, reason)
}

// authenticatedContext carries the resolved identity to route code both as the
// auth context and as the security context repository transactions bind RLS from.
func authenticatedContext(ctx context.Context, claims Claims, identity Identity) context.Context {
	ctx = context.WithValue(ctx, authContextKey{}, AuthContext{
		Claims: claims,
		Tenant: identity.Tenant,
		User:   identity.User,
		Org:    identity.Org,
	})
	return security.WithContext(ctx, security.Context{
		TenantID:       identity.Tenant.TenantID,
		TenantType:     string(identity.Tenant.Type),
		ErunUserID:     identity.User.UserID,
		ExternalIssuer: claims.Issuer,
		ExternalUserID: claims.Subject,
		ExternalOrgID:  identity.Org,
	})
}

// authorize enforces endpoint authorization and reports whether the request may
// continue, writing the rejection itself. Both authorization and auditing key off
// the canonical route path, so a request that reached here without one is a
// route-registration fault rather than a caller error.
func (m *AuthMiddleware) authorize(w http.ResponseWriter, req *http.Request, identity Identity) bool {
	if m.authz == nil && m.audit == nil {
		return true
	}
	apiPath, ok := APIPathFromContext(req.Context())
	if !ok {
		log.Printf("erun api request rejected method=%s path=%s reason=%q", req.Method, req.URL.Path, "api path not resolved")
		http.Error(w, "api path not resolved", http.StatusInternalServerError)
		return false
	}
	if m.authz == nil {
		return true
	}
	if err := m.authz.Authorize(req.Context(), req.Method, apiPath); err != nil {
		log.Printf("erun api authorization rejected method=%s path=%s tenant=%q user=%q reason=%q", req.Method, apiPath, identity.Tenant.TenantID, identity.User.UserID, err.Error())
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return false
	}
	return true
}

func (m *AuthMiddleware) resolveIdentity(ctx context.Context, claims Claims) (Identity, error) {
	// Derive the org first so it is part of the cache key: a shared org-scoped
	// issuer maps the same (issuer, subject) to different tenants per org, so
	// (issuer, subject) alone would leak one tenant's RLS context to another.
	//
	// A failed derivation leaves the org unknown; caching under org="" would
	// conflate it with a single-tenant success or a different org and reintroduce
	// that cross-tenant collision, so it bypasses the cache and falls through to
	// full resolution. org="" is reserved strictly for issuers positively known
	// to be single-tenant or unregistered.
	org, orgErr := m.resolveOrg(ctx, claims)
	cache := m.cache
	if orgErr != nil {
		cache = nil
	}

	// A cached success whose username the token now contradicts counts as a miss,
	// so the refreshed username reaches the database.
	if cache != nil {
		identity, err, ok := cache.Get(claims.Issuer, claims.Subject, org)
		if ok && !cachedIdentityNeedsUsernameRefresh(identity, err, claims) {
			return identity, err
		}
	}

	tenant, user, err := m.tenantAndUser(ctx, claims)
	if err != nil {
		if cache != nil {
			cache.SetFailure(claims.Issuer, claims.Subject, org, err)
		}
		return Identity{}, err
	}
	identity := Identity{Tenant: tenant, User: user, Org: m.resolvedOrg(ctx, claims, org, orgErr)}
	if cache != nil {
		cache.SetSuccess(claims.Issuer, claims.Subject, org, identity)
	}
	return identity, nil
}

// tenantAndUser resolves the tenant and ERun user for verified claims. The
// combined identity resolver takes precedence when wired, because it owns
// bootstrap and must create tenant, issuer, and user atomically.
func (m *AuthMiddleware) tenantAndUser(ctx context.Context, claims Claims) (Tenant, User, error) {
	if m.identities != nil {
		tenant, user, err := m.identities.ResolveIdentity(ctx, claims)
		return resolvedIdentity(tenant, user, classifyIdentityError(err))
	}
	tenant, err := m.tenants.ResolveTenantByIssuer(ctx, claims)
	if err != nil || strings.TrimSpace(tenant.TenantID) == "" {
		return Tenant{}, User{}, ErrTenantNotResolved
	}
	user, err := m.users.ResolveUserByExternalID(ctx, tenant.TenantID, claims.Issuer, claims.Subject)
	if err != nil || strings.TrimSpace(user.UserID) == "" {
		return Tenant{}, User{}, ErrUserNotResolved
	}
	return tenant, user, nil
}

// classifyIdentityError normalizes the combined IdentityResolver's error onto
// the same ErrTenantNotResolved/ErrUserNotResolved split the separate
// tenant/user resolver wiring above already produces, so Wrap reports which
// half failed the same way regardless of which wiring is in play.
// security.ErrTenantUnresolved means the tenant itself could not be
// determined from the token (an org-scoped issuer whose token carries no
// matching org claim, or an issuer with no tenant mapping at all) — distinct
// from a repository.ErrNotFound reached after a tenant already resolved,
// which means this external identity simply is not enrolled in it. Any other
// error passes through unclassified rather than being guessed at;
// repository.ErrIdentityResolutionFailed is one such error, and authErrorCode
// still recognizes it directly as RESOLUTION_FAILED, distinct from both.
func classifyIdentityError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, security.ErrTenantUnresolved) {
		return fmt.Errorf("%w: %s", ErrTenantNotResolved, err.Error())
	}
	if errors.Is(err, repository.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrUserNotResolved, err.Error())
	}
	return err
}

// resolvedIdentity treats a blank tenant or user as unresolved, so a resolver
// that reports no error but no identity cannot authenticate a request.
func resolvedIdentity(tenant Tenant, user User, err error) (Tenant, User, error) {
	if err == nil && strings.TrimSpace(tenant.TenantID) == "" {
		err = ErrTenantNotResolved
	}
	if err == nil && strings.TrimSpace(user.UserID) == "" {
		err = ErrUserNotResolved
	}
	if err != nil {
		return Tenant{}, User{}, err
	}
	return tenant, user, nil
}

// authErrorCode reports the machine-readable code for a resolution failure so
// a client can render "no tenant matched this token" apart from "you are not
// enrolled" instead of collapsing both into one generic message — the
// distinction the not-enrolled UI otherwise cannot make (erun#1721).
// RESOLUTION_FAILED is a third, distinct outcome: an internal error (a
// database failure IdentityRepository already sanitized into
// repository.ErrIdentityResolutionFailed) rather than a real answer about
// enrolment, so a client must not tell the caller they need enrolling, or
// that their tenant could not be determined — neither claim is true here
// (erun#1752).
func authErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrTenantNotResolved):
		return "TENANT_UNRESOLVED"
	case errors.Is(err, ErrUserNotResolved):
		return "NOT_ENROLLED"
	case errors.Is(err, repository.ErrIdentityResolutionFailed):
		return "RESOLUTION_FAILED"
	default:
		return "UNAUTHORIZED"
	}
}

// authErrorEnvelope mirrors internal/routes' {code, message} error envelope
// (see internal/routes/errors.go) so every API error response — including
// this pre-route auth layer, which cannot import that internal package's
// unexported writer — carries the same machine-readable shape.
type authErrorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(authErrorEnvelope{Code: code, Message: message})
}

func (m *AuthMiddleware) resolveOrg(ctx context.Context, claims Claims) (string, error) {
	if m.orgResolver == nil {
		return "", nil
	}
	return m.orgResolver.ResolveOrg(ctx, claims)
}

// resolvedOrg returns the org to record on a successfully resolved identity. On
// the normal path the org derived for the cache key stands. When the initial
// derivation failed, full resolution has since warmed the issuer's org-scoping
// mode, so re-derive now for an accurate audit record; a still-failing
// derivation leaves it empty rather than failing an already-resolved request.
func (m *AuthMiddleware) resolvedOrg(ctx context.Context, claims Claims, org string, orgErr error) string {
	if orgErr == nil {
		return org
	}
	if reOrg, reErr := m.resolveOrg(ctx, claims); reErr == nil {
		return reOrg
	}
	return ""
}

func claimsWithUsernameHint(claims Claims, hint string) Claims {
	hint = strings.TrimSpace(hint)
	if hint == "" || strings.ContainsAny(hint, "\r\n") || len(hint) > 256 {
		return claims
	}
	claims.Username = hint
	return claims
}

func cachedIdentityNeedsUsernameRefresh(identity Identity, err error, claims Claims) bool {
	if err != nil {
		return false
	}
	username := strings.TrimSpace(claims.Username)
	if username == "" {
		return false
	}
	return username != strings.TrimSpace(identity.User.Username)
}

func (m *AuthMiddleware) logAuditEvent(r *http.Request) error {
	if m.audit == nil {
		return nil
	}
	auth, ok := AuthFromContext(r.Context())
	if !ok {
		return errors.New("missing auth context")
	}
	apiPath, ok := APIPathFromContext(r.Context())
	if !ok {
		return errors.New("api path not resolved")
	}
	event := AuditEvent{
		TenantID:         auth.Tenant.TenantID,
		ErunUserID:       auth.User.UserID,
		ExternalUserID:   auth.Claims.Subject,
		ExternalIssuerID: auth.Claims.Issuer,
		ExternalOrgID:    auth.Org,
		Type:             model.AuditEventTypeAPI,
		APIMethod:        r.Method,
		APIPath:          apiPath,
		CreatedAt:        time.Now().UTC(),
	}
	// A caller authenticates with the same bearer token whether it arrived via
	// the CLI, the console, or an MCP tool call, so nothing about the request
	// otherwise distinguishes them; PlatformClient.WithMCPTool sets this header
	// only when erun-mcp made the call, naming which tool did.
	if tool := strings.TrimSpace(r.Header.Get(eruncommon.MCPToolAuditHeader)); tool != "" {
		event.Type = model.AuditEventTypeMCP
		event.MCPTool = tool
	}
	return m.audit.LogAuditEvent(r.Context(), event)
}

func bearerToken(header string) (string, error) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return "", ErrMissingBearerToken
	}
	return fields[1], nil
}
