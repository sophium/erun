package backendapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerRequiresBearerTokenForAPIEndpoint(t *testing.T) {
	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, nil
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			return User{UserID: "019a7fa5-c2c0-7c55-bc70-714873a71f11"}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewHandler failed: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/whoami", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestHandlerHealthzDoesNotRequireBearerToken(t *testing.T) {
	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{}, errors.New("verifier should not be called")
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, issuer string) (Tenant, error) {
			return Tenant{}, errors.New("tenant resolver should not be called")
		}),
		UserResolver: UserResolverFunc(func(ctx context.Context, tenantID string, issuer string, externalID string) (User, error) {
			return User{}, errors.New("user resolver should not be called")
		}),
	})
	if err != nil {
		t.Fatalf("NewHandler failed: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestHandlerWhoamiReturnsResolvedTenant(t *testing.T) {
	var authorizedMethod string
	var authorizedPath string
	var auditEvent AuditEvent
	handler, err := NewHandler(HandlerOptions{
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
		AuditLogger: AuditLoggerFunc(func(ctx context.Context, event AuditEvent) error {
			auditEvent = event
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("NewHandler failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"tenantId":"019a7fa5-c2c0-7c55-bc70-714873a71f10"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"userId":"019a7fa5-c2c0-7c55-bc70-714873a71f11"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if authorizedMethod != http.MethodGet || authorizedPath != "/v1/whoami" {
		t.Fatalf("unexpected authorization input: %s %s", authorizedMethod, authorizedPath)
	}
	if auditEvent.APIPath != "/v1/whoami" {
		t.Fatalf("unexpected audit api path: %q", auditEvent.APIPath)
	}
	if auditEvent.Type != "API" {
		t.Fatalf("unexpected audit event type: %q", auditEvent.Type)
	}
}
