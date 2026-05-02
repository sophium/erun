package backendapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

var (
	ErrMissingBearerToken = errors.New("missing bearer token")
	ErrInvalidBearerToken = errors.New("invalid bearer token")
	ErrTenantNotResolved  = errors.New("tenant not resolved")
	ErrUserNotResolved    = errors.New("user not resolved")
)

type Claims = security.Claims
type Tenant = model.Tenant
type User = model.User

type Identity struct {
	Tenant Tenant
	User   User
}

type IdentityResolver interface {
	ResolveIdentity(ctx context.Context, claims Claims) (Tenant, User, error)
}

type IdentityResolverFunc func(ctx context.Context, claims Claims) (Tenant, User, error)

func (f IdentityResolverFunc) ResolveIdentity(ctx context.Context, claims Claims) (Tenant, User, error) {
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
	ResolveTenantByIssuer(ctx context.Context, issuer string) (Tenant, error)
}

type TenantResolverFunc func(ctx context.Context, issuer string) (Tenant, error)

func (f TenantResolverFunc) ResolveTenantByIssuer(ctx context.Context, issuer string) (Tenant, error) {
	return f(ctx, issuer)
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
}

type authContextKey struct{}

func AuthFromContext(ctx context.Context) (AuthContext, bool) {
	auth, ok := ctx.Value(authContextKey{}).(AuthContext)
	return auth, ok
}

type AuthMiddleware struct {
	verifier   TokenVerifier
	identities IdentityResolver
	tenants    TenantResolver
	users      UserResolver
	cache      *IdentityResolutionCache
	audit      AuditLogger
	authz      Authorizer
}

type AuthMiddlewareOptions struct {
	TokenVerifier    TokenVerifier
	IdentityResolver IdentityResolver
	TenantResolver   TenantResolver
	UserResolver     UserResolver
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
		verifier:   options.TokenVerifier,
		identities: options.IdentityResolver,
		tenants:    options.TenantResolver,
		users:      options.UserResolver,
		cache:      options.IdentityCache,
		audit:      options.AuditLogger,
		authz:      options.Authorizer,
	}, nil
}

func (m *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := bearerToken(r.Header.Get("Authorization"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		claims, err := m.verifier.VerifyBearerToken(r.Context(), token)
		if err != nil || strings.TrimSpace(claims.Issuer) == "" || strings.TrimSpace(claims.Subject) == "" {
			http.Error(w, ErrInvalidBearerToken.Error(), http.StatusUnauthorized)
			return
		}

		identity, err := m.resolveIdentity(r.Context(), claims)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), authContextKey{}, AuthContext{
			Claims: claims,
			Tenant: identity.Tenant,
			User:   identity.User,
		})
		ctx = security.WithContext(ctx, security.Context{
			TenantID:       identity.Tenant.TenantID,
			TenantType:     string(identity.Tenant.Type),
			ErunUserID:     identity.User.UserID,
			ExternalIssuer: claims.Issuer,
			ExternalUserID: claims.Subject,
		})
		req := r.WithContext(ctx)
		if m.authz != nil {
			apiPath, ok := APIPathFromContext(req.Context())
			if !ok {
				http.Error(w, "api path not resolved", http.StatusInternalServerError)
				return
			}
			if err := m.authz.Authorize(req.Context(), req.Method, apiPath); err != nil {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
		}
		if m.audit != nil {
			if _, ok := APIPathFromContext(req.Context()); !ok {
				http.Error(w, "api path not resolved", http.StatusInternalServerError)
				return
			}
		}
		_ = m.logAuditEvent(req)
		next.ServeHTTP(w, req)
	})
}

func (m *AuthMiddleware) resolveIdentity(ctx context.Context, claims Claims) (Identity, error) {
	if m.cache != nil {
		if identity, err, ok := m.cache.Get(claims.Issuer, claims.Subject); ok {
			return identity, err
		}
	}

	if m.identities != nil {
		tenant, user, err := m.identities.ResolveIdentity(ctx, claims)
		if err != nil || strings.TrimSpace(tenant.TenantID) == "" || strings.TrimSpace(user.UserID) == "" {
			if err == nil {
				err = ErrUserNotResolved
			}
			if m.cache != nil {
				m.cache.SetFailure(claims.Issuer, claims.Subject, err)
			}
			return Identity{}, err
		}
		identity := Identity{Tenant: tenant, User: user}
		if m.cache != nil {
			m.cache.SetSuccess(claims.Issuer, claims.Subject, identity)
		}
		return identity, nil
	}

	tenant, err := m.tenants.ResolveTenantByIssuer(ctx, claims.Issuer)
	if err != nil || strings.TrimSpace(tenant.TenantID) == "" {
		err = ErrTenantNotResolved
		if m.cache != nil {
			m.cache.SetFailure(claims.Issuer, claims.Subject, err)
		}
		return Identity{}, err
	}
	user, err := m.users.ResolveUserByExternalID(ctx, tenant.TenantID, claims.Issuer, claims.Subject)
	if err != nil || strings.TrimSpace(user.UserID) == "" {
		err = ErrUserNotResolved
		if m.cache != nil {
			m.cache.SetFailure(claims.Issuer, claims.Subject, err)
		}
		return Identity{}, err
	}

	identity := Identity{Tenant: tenant, User: user}
	if m.cache != nil {
		m.cache.SetSuccess(claims.Issuer, claims.Subject, identity)
	}
	return identity, nil
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
	return m.audit.LogAuditEvent(r.Context(), AuditEvent{
		TenantID:         auth.Tenant.TenantID,
		ErunUserID:       auth.User.UserID,
		ExternalUserID:   auth.Claims.Subject,
		ExternalIssuerID: auth.Claims.Issuer,
		Type:             model.AuditEventTypeAPI,
		APIMethod:        r.Method,
		APIPath:          apiPath,
		CreatedAt:        time.Now().UTC(),
	})
}

func bearerToken(header string) (string, error) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return "", ErrMissingBearerToken
	}
	return fields[1], nil
}
