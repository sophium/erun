package mcpauth

import (
	"runtime"
	"strings"
	"testing"
)

func bytesAllocatedBy(parse func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	parse()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A bearer header is attacker-controlled up to net/http's 1 MiB header limit, and these
// run before anything is verified: splitting on every "." would allocate a slice entry
// per separator.
func TestTokenParsingDoesNotScaleWithSeparators(t *testing.T) {
	token := strings.Repeat(".", 1<<20)
	parsers := map[string]func(){
		"IssuerFromUnverifiedJWT":  func() { _, _ = IssuerFromUnverifiedJWT(token) },
		"SubjectFromUnverifiedJWT": func() { _, _ = SubjectFromUnverifiedJWT(token) },
		"parseMCPToken":            func() { _, _, _, _ = parseMCPToken(token) },
	}
	for name, parse := range parsers {
		if allocated := bytesAllocatedBy(parse); allocated > 1<<18 {
			t.Errorf("%s allocated %d bytes for a token of %d separators; its work must not grow with them", name, allocated, len(token))
		}
	}
}
