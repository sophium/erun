package backendapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

func TestAuthMiddlewareLogsAuditEventForAuthorizedRequest(t *testing.T) {
	var event AuditEvent
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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

// The MCP tool header is the only thing distinguishing this from a plain API call.
func TestAuthMiddlewareLogsMCPAuditEventForAnMCPToolCall(t *testing.T) {
	var event AuditEvent
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(ctx context.Context, token string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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

	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/019abc", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	req.Header.Set(platformclient.MCPToolAuditHeader, "review_show")
	rec := httptest.NewRecorder()
	withAPIPath("/v1/reviews/{review_id}", middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))).ServeHTTP(rec, req)

	if event.Type != model.AuditEventTypeMCP {
		t.Fatalf("type = %q, want %q", event.Type, model.AuditEventTypeMCP)
	}
	if event.MCPTool != "review_show" {
		t.Fatalf("mcpTool = %q, want %q", event.MCPTool, "review_show")
	}
	if event.TenantID != "019a7fa5-c2c0-7c55-bc70-714873a71f10" {
		t.Fatalf("unexpected tenant id: %q", event.TenantID)
	}
	if event.APIMethod != http.MethodGet || event.APIPath != "/v1/reviews/{review_id}" {
		t.Fatalf("unexpected method/path: %q %q", event.APIMethod, event.APIPath)
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
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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
		TenantResolver: TenantResolverFunc(func(ctx context.Context, claims Claims) (Tenant, error) {
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
