package backendapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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

// A shared issuer maps (issuer, subject) to a tenant per org, so the cache must key on org.
func TestAuthMiddlewareKeepsOrgScopedTenantsDistinctInCache(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cache := NewIdentityResolutionCache(IdentityCacheOptions{
		PositiveTTL: time.Minute,
		Now:         func() time.Time { return now },
	})
	tenantForOrg := map[string]string{
		"org-a": "019a7fa5-c2c0-7c55-bc70-714873a71f1a",
		"org-b": "019a7fa5-c2c0-7c55-bc70-714873a71f1b",
	}
	var event AuditEvent
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://shared.example", Subject: "user-1", Raw: map[string]any{"org": token}}, nil
		}),
		OrgResolver: OrgResolverFunc(func(ctx context.Context, claims Claims) (string, error) {
			org, _ := claims.Raw["org"].(string)
			return org, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
			org, _ := claims.Raw["org"].(string)
			tenantID, ok := tenantForOrg[org]
			if !ok {
				return Tenant{}, errors.New("unknown org")
			}
			return Tenant{TenantID: tenantID}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			return User{UserID: "019a7fa5-c2c0-7c55-bc70-714873a71f11"}, nil
		}),
		AuditLogger: AuditLoggerFunc(func(ctx context.Context, auditEvent AuditEvent) error {
			event = auditEvent
			return nil
		}),
		IdentityCache: cache,
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	resolve := func(org string) (string, AuditEvent) {
		event = AuditEvent{}
		var resolvedTenant string
		req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
		req.Header.Set("Authorization", "Bearer "+org)
		rec := httptest.NewRecorder()
		withAPIPath("/v1/whoami", middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth, _ := AuthFromContext(r.Context())
			resolvedTenant = auth.Tenant.TenantID
			w.WriteHeader(http.StatusNoContent)
		}))).ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("org %q: unexpected status: %d", org, rec.Code)
		}
		return resolvedTenant, event
	}

	if got, ev := resolve("org-a"); got != tenantForOrg["org-a"] || ev.ExternalOrgID != "org-a" {
		t.Fatalf("org-a: tenant=%q externalOrgID=%q", got, ev.ExternalOrgID)
	}
	if got, ev := resolve("org-b"); got != tenantForOrg["org-b"] || ev.ExternalOrgID != "org-b" {
		t.Fatalf("org-b leaked cached tenant: tenant=%q externalOrgID=%q", got, ev.ExternalOrgID)
	}
	if got, _ := resolve("org-a"); got != tenantForOrg["org-a"] {
		t.Fatalf("org-a re-resolve: tenant=%q", got)
	}
}

// A failed org derivation must bypass the cache rather than cache under org="".
func TestAuthMiddlewareBypassesIdentityCacheWhenOrgDerivationFails(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cache := NewIdentityResolutionCache(IdentityCacheOptions{
		PositiveTTL: time.Minute,
		Now:         func() time.Time { return now },
	})
	tenantForOrg := map[string]string{
		"org-a": "019a7fa5-c2c0-7c55-bc70-714873a71f1a",
		"org-b": "019a7fa5-c2c0-7c55-bc70-714873a71f1b",
	}
	tenantResolverCalls := 0
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://shared.example", Subject: "user-1", Raw: map[string]any{"org": token}}, nil
		}),
		OrgResolver: OrgResolverFunc(func(ctx context.Context, claims Claims) (string, error) {
			return "", errors.New("transient org lookup failure")
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
			tenantResolverCalls++
			org, _ := claims.Raw["org"].(string)
			tenantID, ok := tenantForOrg[org]
			if !ok {
				return Tenant{}, errors.New("unknown org")
			}
			return Tenant{TenantID: tenantID}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			return User{UserID: "019a7fa5-c2c0-7c55-bc70-714873a71f11"}, nil
		}),
		IdentityCache: cache,
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}

	resolve := func(org string) string {
		var resolvedTenant string
		req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
		req.Header.Set("Authorization", "Bearer "+org)
		rec := httptest.NewRecorder()
		middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth, _ := AuthFromContext(r.Context())
			resolvedTenant = auth.Tenant.TenantID
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("org %q: unexpected status: %d", org, rec.Code)
		}
		return resolvedTenant
	}

	if got := resolve("org-a"); got != tenantForOrg["org-a"] {
		t.Fatalf("org-a: tenant=%q", got)
	}
	if got := resolve("org-b"); got != tenantForOrg["org-b"] {
		t.Fatalf("org-b leaked cached tenant: tenant=%q", got)
	}
	if got := resolve("org-a"); got != tenantForOrg["org-a"] {
		t.Fatalf("org-a re-resolve: tenant=%q", got)
	}
	if tenantResolverCalls != 3 {
		t.Fatalf("expected cache bypass to re-resolve every request, got %d tenant resolver calls", tenantResolverCalls)
	}
}
