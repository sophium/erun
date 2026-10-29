package mcptoken

import (
	"runtime"
	"strings"
	"testing"
)

// The DNS-01 token arrives in an unauthenticated header; splitting it on every "."
// would allocate a slice entry per separator before the signature is checked.
func TestParseEdDSATokenDoesNotScaleWithSeparators(t *testing.T) {
	token := strings.Repeat(".", 1<<20)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, _, _, _ = parseEdDSAToken(token)
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<18 {
		t.Errorf("parseEdDSAToken allocated %d bytes for a token of %d separators; its work must not grow with them", allocated, len(token))
	}
}
