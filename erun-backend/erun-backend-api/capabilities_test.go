package backendapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-common/base/model"
)

type stubCapabilityResolver struct {
	candidates []model.PlatformCapability
	permit     func(model.PlatformCapability) bool
	err        error
}

func (r *stubCapabilityResolver) PermittedRoutes(_ context.Context, candidates []model.PlatformCapability) ([]model.PlatformCapability, error) {
	r.candidates = candidates
	if r.err != nil {
		return nil, r.err
	}
	var permitted []model.PlatformCapability
	for _, candidate := range candidates {
		if r.permit == nil || r.permit(candidate) {
			permitted = append(permitted, candidate)
		}
	}
	return permitted, nil
}

func capabilityHandlerOptions(resolver *stubCapabilityResolver) HandlerOptions {
	return HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(context.Context, string) (Claims, error) {
			return Claims{Issuer: "https://issuer.example", Subject: "user-1"}, nil
		}),
		TenantResolver: TenantResolverFunc(func(context.Context, Claims) (Tenant, error) {
			return Tenant{TenantID: "019a7fa5-c2c0-7c55-bc70-714873a71f10"}, nil
		}),
		UserResolver: UserResolverFunc(func(context.Context, string, string, string) (User, error) {
			return User{UserID: "019a7fa5-c2c0-7c55-bc70-714873a71f11"}, nil
		}),
		Authorizer:   AuthorizerFunc(func(context.Context, string, string) error { return nil }),
		Capabilities: resolver,
	}
}

// undialedDatabase is never connected; it lets the full route surface register without PostgreSQL.
func undialedDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", "postgres://erun@127.0.0.1:1/erun?sslmode=disable")
	if err != nil {
		t.Fatalf("open placeholder database handle: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func whoamiCapabilities(t *testing.T, handler http.Handler) model.PlatformCapabilities {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Capabilities model.PlatformCapabilities `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode whoami: %v", err)
	}
	return response.Capabilities
}

func TestWhoamiReportsTheCallersCapabilities(t *testing.T) {
	resolver := &stubCapabilityResolver{
		permit: func(candidate model.PlatformCapability) bool { return candidate.Method == http.MethodGet },
	}
	handler, err := NewHandler(capabilityHandlerOptions(resolver))
	if err != nil {
		t.Fatalf("NewHandler failed: %v", err)
	}

	capabilities := whoamiCapabilities(t, handler)
	if !capabilities.Allows(http.MethodGet, "/v1/whoami") {
		t.Fatalf("expected the caller's own whoami read to be reported, got %+v", capabilities)
	}
}

func TestWhoamiReportsAnEmptyCapabilitySetForAPermissionlessCaller(t *testing.T) {
	resolver := &stubCapabilityResolver{
		permit: func(model.PlatformCapability) bool { return false },
	}
	handler, err := NewHandler(capabilityHandlerOptions(resolver))
	if err != nil {
		t.Fatalf("NewHandler failed: %v", err)
	}

	if capabilities := whoamiCapabilities(t, handler); len(capabilities) != 0 {
		t.Fatalf("expected no capabilities, got %+v", capabilities)
	}
}

func TestCapabilityCandidatesAreTheHandlersOwnRoutes(t *testing.T) {
	resolver := &stubCapabilityResolver{}
	options := capabilityHandlerOptions(resolver)
	options.DB = undialedDatabase(t)
	auth, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier:  options.TokenVerifier,
		TenantResolver: options.TenantResolver,
		UserResolver:   options.UserResolver,
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware failed: %v", err)
	}
	mux := http.NewServeMux()
	catalog := registerProtectedRoutes(mux, auth, options, repository.NewTxManager(options.DB, options.DBDialect), options.Authorizer)

	candidates := catalog.sorted()
	if len(candidates) < 2 {
		t.Fatalf("expected the handler's protected routes in the catalog, got %+v", candidates)
	}
	for _, candidate := range candidates {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(candidate.Method, concreteRequestPath(candidate.Path), nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("capability candidate %s %s is not a route this handler serves", candidate.Method, candidate.Path)
		}
	}
	if !model.PlatformCapabilities(candidates).Allows(http.MethodGet, "/v1/whoami") {
		t.Errorf("expected the whoami route itself among the candidates, got %+v", candidates)
	}
}

var routeWildcardPattern = regexp.MustCompile(`\{[^}]*\}`)

func concreteRequestPath(apiPath string) string {
	return routeWildcardPattern.ReplaceAllString(apiPath, "placeholder")
}

// An empty set means "you may do nothing", so a resolver failure must not report one.
func TestWhoamiFailsRatherThanClaimingNoCapabilities(t *testing.T) {
	resolver := &stubCapabilityResolver{err: errors.New("capability lookup failed")}
	handler, err := NewHandler(capabilityHandlerOptions(resolver))
	if err != nil {
		t.Fatalf("NewHandler failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected the whoami read to fail, got status %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCapabilitiesDefaultToTheAuthorizerThatEnforcesThem(t *testing.T) {
	options := HandlerOptions{DB: undialedDatabase(t)}
	authorizer := resolveAuthorizer(options)
	if resolveCapabilities(options, authorizer) == nil {
		t.Fatal("expected the database-backed authorizer to answer the capability set")
	}
	injected := HandlerOptions{Authorizer: AuthorizerFunc(func(context.Context, string, string) error { return nil })}
	if resolveCapabilities(injected, resolveAuthorizer(injected)) != nil {
		t.Fatal("expected no capability resolver for an authorizer that cannot resolve one")
	}
}
