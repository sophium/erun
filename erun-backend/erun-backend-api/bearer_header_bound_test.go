package backendapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func bytesAllocatedBy(run func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// manyFieldAuthorizationHeaders fill net/http's 1 MiB header limit with fields. The
// multi-byte rune matters: it sends a full split down its Unicode path, which
// allocates several times what the ASCII path does.
func manyFieldAuthorizationHeaders() map[string]string {
	ascii := strings.Repeat("a ", 1<<19)
	return map[string]string{
		"ASCII fields": ascii,
		"ASCII fields ending in a multi-byte rune": ascii[:len(ascii)-len("é")] + "é",
	}
}

// The Authorization header is read on every protected route before anything about
// it is trusted; collecting each of its fields would allocate per field before the
// two-field check rejects it.
func TestAuthMiddlewareBearerParsingDoesNotScaleWithHeaderFields(t *testing.T) {
	verified := false
	middleware, err := NewAuthMiddleware(AuthMiddlewareOptions{
		TokenVerifier: TokenVerifierFunc(func(context.Context, string) (Claims, error) {
			verified = true
			return Claims{}, ErrInvalidBearerToken
		}),
		IdentityResolver: IdentityResolverFunc(func(context.Context, Claims) (Tenant, User, error) {
			return Tenant{}, User{}, ErrTenantNotResolved
		}),
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware: %v", err)
	}
	handler := middleware.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a header that is not a bearer pair reached the protected handler")
	}))
	for name, header := range manyFieldAuthorizationHeaders() {
		req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		allocated := bytesAllocatedBy(func() { handler.ServeHTTP(rec, req) })
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
		if allocated > 1<<18 {
			t.Errorf("%s: allocated %d bytes for a %d-byte header; the work must not grow with its fields", name, allocated, len(header))
		}
	}
	if verified {
		t.Error("the verifier was asked about a header that is not a bearer pair")
	}
}
