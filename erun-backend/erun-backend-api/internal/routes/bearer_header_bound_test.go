package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

func bytesAllocatedBy(run func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

type countingInviteRequestTokenVerifier struct {
	calls int
}

func (v *countingInviteRequestTokenVerifier) VerifyBearerToken(context.Context, string) (security.Claims, error) {
	v.calls++
	return security.Claims{}, context.Canceled
}

// GET /v1/invite-requests/mine is unauthenticated and unthrottled, and it reads the
// Authorization header before verifying anything: collecting each of its fields
// would allocate per field before the two-field check rejects it. The multi-byte
// rune sends a full split down its Unicode path, several times the ASCII cost.
func TestInviteRequestBearerParsingDoesNotScaleWithHeaderFields(t *testing.T) {
	ascii := strings.Repeat("a ", 1<<19)
	headers := map[string]string{
		"ASCII fields": ascii,
		"ASCII fields ending in a multi-byte rune": ascii[:len(ascii)-len("é")] + "é",
	}
	verifier := &countingInviteRequestTokenVerifier{}
	mux := http.NewServeMux()
	RegisterInviteRequestPublicRoutes(mux, verifier, &stubInviteRequestSubmitter{}, NewInviteRequestRateLimiter(stubInviteRequestWindowReader{windowSeconds: 60}))
	for name, header := range headers {
		req := httptest.NewRequest(http.MethodGet, "/v1/invite-requests/mine", nil)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		allocated := bytesAllocatedBy(func() { mux.ServeHTTP(rec, req) })
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
		if allocated > 1<<18 {
			t.Errorf("%s: allocated %d bytes for a %d-byte header; the work must not grow with its fields", name, allocated, len(header))
		}
	}
	if verifier.calls != 0 {
		t.Errorf("the verifier was asked %d times about a header that is not a bearer pair", verifier.calls)
	}
}
