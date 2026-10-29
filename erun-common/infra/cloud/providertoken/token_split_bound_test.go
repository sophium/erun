package providertoken

import (
	"runtime"
	"strings"
	"testing"
)

// The bearer token is read before it is trusted; splitting it on every "." would
// allocate a slice entry per separator.
func TestIssuerFromJWTDoesNotScaleWithSeparators(t *testing.T) {
	token := strings.Repeat(".", 1<<20)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, _ = issuerFromJWT(token)
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<18 {
		t.Errorf("issuerFromJWT allocated %d bytes for a token of %d separators; its work must not grow with them", allocated, len(token))
	}
}
