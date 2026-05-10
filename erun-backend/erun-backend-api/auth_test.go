package backendapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthMiddlewareResolvesTenantFromIssuer(t *testing.T) {
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			if token != "valid-token" {
				t.Fatalf("unexpected token: %q", token)
			}
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			if issuer != "https://issuer.example" {
				t.Fatalf("unexpected issuer: %q", issuer)
			}
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			if tenantID != "019a7fa5-c2c0-7c55-bc70-714873a71f10" {
				t.Fatalf("unexpected tenant id: %q", tenantID)
			}
			if issuer != "https://issuer.example" {
				t.Fatalf("unexpected issuer: %q", issuer)
			}
			if externalID != "user-1" {
				t.Fatalf("unexpected external id: %q", externalID)
			}
			return User{UserID: "019a7fa5-c2c0-7c55-bc70-714873a71f11"}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, ok := AuthFromContext(r.Context())
		if !ok {
			t.Fatal("expected auth context")
		}
		if auth.Tenant.TenantID != "019a7fa5-c2c0-7c55-bc70-714873a71f10" {
			t.Fatalf("unexpected tenant id: %q", auth.Tenant.TenantID)
		}
		if auth.User.UserID != "019a7fa5-c2c0-7c55-bc70-714873a71f11" {
			t.Fatalf("unexpected user id: %q", auth.User.UserID)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestAuthMiddlewareResolvesIdentityWithSingleResolver(t *testing.T) {
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		IdentityResolver: IdentityResolverFunc(func(ctx context.Context, claims Claims) (Tenant, User, error) {
			if claims.Issuer != "https://issuer.example" {
				t.Fatalf("unexpected issuer: %q", claims.Issuer)
			}
			if claims.Subject != "user-1" {
				t.Fatalf("unexpected subject: %q", claims.Subject)
			}
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, User{UserID: "019a7fa5-c2c0-7c55-bc70-714873a71f11"}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestAuthMiddlewareAppliesUsernameHint(t *testing.T) {
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		IdentityResolver: IdentityResolverFunc(func(ctx context.Context, claims Claims) (Tenant, User, error) {
			if claims.Username != "Rihards.Freimanis" {
				t.Fatalf("unexpected username hint: %q", claims.Username)
			}
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, User{UserID: "019a7fa5-c2c0-7c55-bc70-714873a71f11", Username: claims.Username}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	req.Header.Set(usernameHintHeader, " Rihards.Freimanis ")
	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, ok := AuthFromContext(r.Context())
		if !ok {
			t.Fatal("expected auth context")
		}
		if auth.User.Username != "Rihards.Freimanis" {
			t.Fatalf("unexpected auth username: %q", auth.User.Username)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestAuthMiddlewareRefreshesCachedIdentityForUsernameHint(t *testing.T) {
	cache := NewIdentityResolutionCache(IdentityCacheOptions{PositiveTTL: time.Minute})
	resolverCalls := 0
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		IdentityResolver: IdentityResolverFunc(func(ctx context.Context, claims Claims) (Tenant, User, error) {
			resolverCalls++
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, User{UserID: "019a7fa5-c2c0-7c55-bc70-714873a71f11", Username: claims.Username}, nil
		}),
		IdentityCache: cache,
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	for _, username := range []string{"", "Rihards.Freimanis"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		if username != "" {
			req.Header.Set(usernameHintHeader, username)
		}
		rec := httptest.NewRecorder()
		middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("unexpected status: %d", rec.Code)
		}
	}

	if resolverCalls != 2 {
		t.Fatalf("expected username hint to refresh cached identity, got %d resolver calls", resolverCalls)
	}
}

func TestAuthMiddlewareRejectsMissingBearerToken(t *testing.T) {
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			t.Fatal("verifier should not be called")
			return Claims{}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			t.Fatal("tenant resolver should not be called")
			return Tenant{}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			t.Fatal("user resolver should not be called")
			return User{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/whoami", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestAuthMiddlewareRejectsMalformedBearerToken(t *testing.T) {
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			t.Fatal("verifier should not be called")
			return Claims{}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			t.Fatal("tenant resolver should not be called")
			return Tenant{}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			t.Fatal("user resolver should not be called")
			return User{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer token extra")
	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestAuthMiddlewareRejectsUnknownTenantIssuer(t *testing.T) {
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://unknown.example", Subject: "user-1"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			return Tenant{}, errors.New("not found")
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			t.Fatal("user resolver should not be called")
			return User{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestAuthMiddlewareRejectsUnknownExternalUser(t *testing.T) {
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "unknown-user"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			return User{}, errors.New("not found")
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestAuthMiddlewareCachesFailedExternalUserResolution(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cache := NewIdentityResolutionCache(IdentityCacheOptions{
		PositiveTTL: time.Minute,
		NegativeTTL: time.Minute,
		Now: func() time.Time {
			return now
		},
	})
	userResolverCalls := 0
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "unknown-user"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			userResolverCalls++
			return User{}, errors.New("not found")
		}),
		IdentityCache: cache,
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		rec := httptest.NewRecorder()
		middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("next handler should not be called")
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected status: %d", rec.Code)
		}
	}

	if userResolverCalls != 1 {
		t.Fatalf("expected one user resolver call, got %d", userResolverCalls)
	}
}

func TestAuthMiddlewareExpiresFailedExternalUserResolutionCache(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cache := NewIdentityResolutionCache(IdentityCacheOptions{
		PositiveTTL: time.Minute,
		NegativeTTL: time.Second,
		Now: func() time.Time {
			return now
		},
	})
	userResolverCalls := 0
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "unknown-user"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			userResolverCalls++
			return User{}, errors.New("not found")
		}),
		IdentityCache: cache,
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(httptest.NewRecorder(), req)

	now = now.Add(2 * time.Second)
	req = httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(httptest.NewRecorder(), req)

	if userResolverCalls != 2 {
		t.Fatalf("expected two user resolver calls, got %d", userResolverCalls)
	}
}

func TestAuthMiddlewareLogsAuditEventForAuthorizedRequest(t *testing.T) {
	var event AuditEvent
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			return User{UserID: "019a7fa5-c2c0-7c55-bc70-714873a71f11"}, nil
		}),
		AuditLogger: AuditLoggerFunc(func(ctx context.Context, auditEvent AuditEvent) error {
			event = auditEvent
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami?verbose=true", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	withAPIPath("/v1/whoami", middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))).ServeHTTP(rec, req)

	if event.TenantID != "019a7fa5-c2c0-7c55-bc70-714873a71f10" {
		t.Fatalf("unexpected tenant id: %q", event.TenantID)
	}
	if event.ErunUserID != "019a7fa5-c2c0-7c55-bc70-714873a71f11" {
		t.Fatalf("unexpected user id: %q", event.ErunUserID)
	}
	if event.ExternalIssuerID != "https://issuer.example" {
		t.Fatalf("unexpected issuer: %q", event.ExternalIssuerID)
	}
	if event.ExternalUserID != "user-1" {
		t.Fatalf("unexpected external user: %q", event.ExternalUserID)
	}
	if event.Type != "API" {
		t.Fatalf("unexpected audit event type: %q", event.Type)
	}
	if event.APIMethod != http.MethodGet {
		t.Fatalf("unexpected method: %q", event.APIMethod)
	}
	if event.APIPath != "/v1/whoami" {
		t.Fatalf("unexpected api path: %q", event.APIPath)
	}
	if event.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be populated")
	}
}

func TestAuthMiddlewareAuthorizesBeforeAuditAndHandler(t *testing.T) {
	var authorizedMethod string
	var authorizedPath string
	auditCalls := 0
	handlerCalls := 0
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			return User{UserID: "019a7fa5-c2c0-7c55-bc70-714873a71f11"}, nil
		}),
		Authorizer: AuthorizerFunc(func(ctx context.Context, method string, apiPath string) error {
			authorizedMethod = method
			authorizedPath = apiPath
			return nil
		}),
		AuditLogger: AuditLoggerFunc(func(ctx context.Context, auditEvent AuditEvent) error {
			auditCalls++
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/019abc", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	withAPIPath("/v1/reviews/{review_id}", middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.WriteHeader(http.StatusNoContent)
	}))).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if authorizedMethod != http.MethodGet || authorizedPath != "/v1/reviews/{review_id}" {
		t.Fatalf("unexpected authorization input: %s %s", authorizedMethod, authorizedPath)
	}
	if auditCalls != 1 {
		t.Fatalf("expected one audit call, got %d", auditCalls)
	}
	if handlerCalls != 1 {
		t.Fatalf("expected one handler call, got %d", handlerCalls)
	}
}

func TestAuthMiddlewareRejectsDeniedPermissionBeforeAuditAndHandler(t *testing.T) {
	auditCalls := 0
	handlerCalls := 0
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			return User{UserID: "019a7fa5-c2c0-7c55-bc70-714873a71f11"}, nil
		}),
		Authorizer: AuthorizerFunc(func(ctx context.Context, method string, apiPath string) error {
			return errors.New("denied")
		}),
		AuditLogger: AuditLoggerFunc(func(ctx context.Context, auditEvent AuditEvent) error {
			auditCalls++
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/019abc", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	withAPIPath("/v1/reviews/{review_id}", middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
	}))).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if auditCalls != 0 {
		t.Fatalf("unexpected audit calls: %d", auditCalls)
	}
	if handlerCalls != 0 {
		t.Fatalf("unexpected handler calls: %d", handlerCalls)
	}
}

func TestAuthMiddlewareDoesNotLogAuditEventForRejectedRequest(t *testing.T) {
	auditCalls := 0
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{}, errors.New("invalid")
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			t.Fatal("tenant resolver should not be called")
			return Tenant{}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			t.Fatal("user resolver should not be called")
			return User{}, nil
		}),
		AuditLogger: AuditLoggerFunc(func(ctx context.Context, auditEvent AuditEvent) error {
			auditCalls++
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rec := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})).ServeHTTP(rec, req)

	if auditCalls != 0 {
		t.Fatalf("unexpected audit calls: %d", auditCalls)
	}
}
