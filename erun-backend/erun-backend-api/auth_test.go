package backendapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// decodedAuthError reads the {code, message} envelope every auth-layer error
// response carries, failing the test if the body isn't that shape.
func decodedAuthError(t *testing.T, rec *httptest.ResponseRecorder) authErrorEnvelope {
	t.Helper()
	var envelope authErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v (body: %s)", err, rec.Body.String())
	}
	return envelope
}

// mustEqual fails when a value the middleware passed through differs from the one
// it resolved from.
func mustEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("unexpected %s: %q", field, got)
	}
}

func TestAuthMiddlewareResolvesTenantFromIssuer(t *testing.T) {
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			mustEqual(t, "token", token, "valid-token")
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
			mustEqual(t, "issuer", claims.Issuer, "https://issuer.example")
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			mustEqual(t, "tenant id", tenantID, "019a7fa5-c2c0-7c55-bc70-714873a71f10")
			mustEqual(t, "issuer", issuer, "https://issuer.example")
			mustEqual(t, "external id", externalID, "user-1")
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
		mustEqual(t, "tenant id", auth.Tenant.TenantID, "019a7fa5-c2c0-7c55-bc70-714873a71f10")
		mustEqual(t, "user id", auth.User.UserID, "019a7fa5-c2c0-7c55-bc70-714873a71f11")
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
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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
	if code := decodedAuthError(t, rec).Code; code != "TENANT_UNRESOLVED" {
		t.Fatalf("code = %q, want TENANT_UNRESOLVED -- an unknown issuer means no tenant matched this token, not that the caller isn't enrolled", code)
	}
}

func TestAuthMiddlewareRejectsUnknownExternalUser(t *testing.T) {
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "unknown-user"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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
	if code := decodedAuthError(t, rec).Code; code != "NOT_ENROLLED" {
		t.Fatalf("code = %q, want NOT_ENROLLED -- the tenant resolved fine, this external id just isn't a member", code)
	}
}

// TestAuthMiddlewareReportsTenantUnresolvedForOrgClaimMissing checks at the
// middleware boundary that a combined IdentityResolver
// (the production wiring) reporting security.ErrTenantUnresolved -- an
// org-scoped issuer whose token carries no matching org claim -- must render
// as TENANT_UNRESOLVED, never the generic "not enrolled" message that sends
// an already-enrolled operator to ask for an enrolment that already exists.
func TestAuthMiddlewareReportsTenantUnresolvedForOrgClaimMissing(t *testing.T) {
	underlying := fmt.Errorf("%w: issuer %q is org-scoped but the token carries no matching claim", security.ErrTenantUnresolved, "https://auth.example/shared")
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://auth.example/shared", Subject: "rihards@frs.lv"}, nil
		}),
		IdentityResolver: IdentityResolverFunc(func(ctx context.Context, claims Claims) (Tenant, User, error) {
			return Tenant{}, User{}, underlying
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
	if code := decodedAuthError(t, rec).Code; code != "TENANT_UNRESOLVED" {
		t.Fatalf("code = %q, want TENANT_UNRESOLVED", code)
	}
}

// TestAuthMiddlewareReportsNotEnrolledForCombinedResolverNotFound mirrors the
// same distinction through the combined IdentityResolver wiring: a tenant
// that resolved fine but reports a plain repository.ErrNotFound (this
// external identity is not one of its users) must render as NOT_ENROLLED,
// not TENANT_UNRESOLVED.
func TestAuthMiddlewareReportsNotEnrolledForCombinedResolverNotFound(t *testing.T) {
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "a-stranger"}, nil
		}),
		IdentityResolver: IdentityResolverFunc(func(ctx context.Context, claims Claims) (Tenant, User, error) {
			return Tenant{}, User{}, repository.ErrNotFound
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
	if code := decodedAuthError(t, rec).Code; code != "NOT_ENROLLED" {
		t.Fatalf("code = %q, want NOT_ENROLLED", code)
	}
}

// TestAuthMiddlewareReportsResolutionFailedWithoutLeakingRawDatabaseError
// checks at the middleware boundary that a repository.ErrIdentityResolutionFailed
// renders as its own RESOLUTION_FAILED code, distinct from NOT_ENROLLED and
// TENANT_UNRESOLVED, and that the client-facing message and the auth-rejected
// log line carry only the sanitized sentinel's text, never a raw constraint
// name or SQLSTATE.
func TestAuthMiddlewareReportsResolutionFailedWithoutLeakingRawDatabaseError(t *testing.T) {
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://auth.erunpaas.com", Subject: "387534471668170904"}, nil
		}),
		IdentityResolver: IdentityResolverFunc(func(ctx context.Context, claims Claims) (Tenant, User, error) {
			return Tenant{}, User{}, repository.ErrIdentityResolutionFailed
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
	envelope := decodedAuthError(t, rec)
	if envelope.Code != "RESOLUTION_FAILED" {
		t.Fatalf("code = %q, want RESOLUTION_FAILED -- this is an internal error, not a real answer about enrolment or tenant resolution", envelope.Code)
	}
	for _, leak := range []string{"SQLSTATE", "constraint", "duplicate key"} {
		if strings.Contains(envelope.Message, leak) {
			t.Fatalf("response message leaked raw database detail (%q): %q", leak, envelope.Message)
		}
	}
	logged := logBuf.String()
	for _, leak := range []string{"SQLSTATE", "constraint", "duplicate key"} {
		if strings.Contains(logged, leak) {
			t.Fatalf("auth-rejected log leaked raw database detail (%q): %q", leak, logged)
		}
	}
}

func TestAuthMiddlewareReportsUnauthenticatedForMissingBearerToken(t *testing.T) {
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			t.Fatal("verifier should not be called")
			return Claims{}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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
	if code := decodedAuthError(t, rec).Code; code != "UNAUTHENTICATED" {
		t.Fatalf("code = %q, want UNAUTHENTICATED", code)
	}
}

func TestClassifyIdentityErrorMapsTenantUnresolved(t *testing.T) {
	err := classifyIdentityError(fmt.Errorf("%w: issuer unknown", security.ErrTenantUnresolved))
	if !errors.Is(err, ErrTenantNotResolved) {
		t.Fatalf("err = %v, want ErrTenantNotResolved", err)
	}
	if errors.Is(err, ErrUserNotResolved) {
		t.Fatalf("err = %v, must not also satisfy ErrUserNotResolved", err)
	}
}

func TestClassifyIdentityErrorMapsNotEnrolled(t *testing.T) {
	err := classifyIdentityError(repository.ErrNotFound)
	if !errors.Is(err, ErrUserNotResolved) {
		t.Fatalf("err = %v, want ErrUserNotResolved", err)
	}
	if errors.Is(err, ErrTenantNotResolved) {
		t.Fatalf("err = %v, must not also satisfy ErrTenantNotResolved", err)
	}
}

func TestClassifyIdentityErrorPassesThroughUnexpectedErrors(t *testing.T) {
	unexpected := errors.New("database connection reset")
	if got := classifyIdentityError(unexpected); !errors.Is(got, unexpected) {
		t.Fatalf("err = %v, want the unexpected error passed through unclassified", got)
	}
}

// TestAuthErrorCodeDistinguishesResolutionFailedFromNotEnrolledAndUnresolved
// locks in that all three 401-shaped outcomes get their own code: a real
// "you need enrolling" (NOT_ENROLLED), a real "your tenant could not be
// determined" (TENANT_UNRESOLVED), and a sanitized internal error
// (RESOLUTION_FAILED) that is neither.
func TestAuthErrorCodeDistinguishesResolutionFailedFromNotEnrolledAndUnresolved(t *testing.T) {
	if code := authErrorCode(repository.ErrIdentityResolutionFailed); code != "RESOLUTION_FAILED" {
		t.Fatalf("code = %q, want RESOLUTION_FAILED", code)
	}
	if code := authErrorCode(ErrUserNotResolved); code != "NOT_ENROLLED" {
		t.Fatalf("code = %q, want NOT_ENROLLED", code)
	}
	if code := authErrorCode(ErrTenantNotResolved); code != "TENANT_UNRESOLVED" {
		t.Fatalf("code = %q, want TENANT_UNRESOLVED", code)
	}
}
