package jobs

import (
	"errors"
	"strings"
	"testing"
)

// Same raw schema-rejection shape as activity_lease_take on an old edge.
const rawExclusiveJobStartSchemaError = `MCP tools/call failed: invalid params: validating "arguments": validating root: unexpected additional properties ["exclusive"] (code -32602)`

func TestDescribeExclusiveJobStartVersionSkewNamesTheVersionMismatch(t *testing.T) {
	err := errors.New(rawExclusiveJobStartSchemaError)
	described := DescribeExclusiveJobStartVersionSkew("petios", "rihards-develop", true, err)
	if described == nil {
		t.Fatal("described error is nil")
	}
	message := described.Error()
	for _, want := range []string{"petios/rihards-develop", "older than the one that added --exclusive", "job start"} {
		if !strings.Contains(message, want) {
			t.Fatalf("described error %q does not mention %q", message, want)
		}
	}
	if !errors.Is(described, err) {
		t.Fatalf("described error does not wrap the original: %v", described)
	}
}

func TestDescribeExclusiveJobStartVersionSkewLeavesOtherFailuresAlone(t *testing.T) {
	notExclusive := errors.New(rawExclusiveJobStartSchemaError)
	if got := DescribeExclusiveJobStartVersionSkew("petios", "rihards-develop", false, notExclusive); !errors.Is(got, notExclusive) || got.Error() != notExclusive.Error() {
		t.Fatalf("a non-exclusive call must pass its error through unchanged, got %v", got)
	}

	unrelated := errors.New(`MCP tools/call failed: invalid params: validating "arguments": validating "name": value is required (code -32602)`)
	if got := DescribeExclusiveJobStartVersionSkew("petios", "rihards-develop", true, unrelated); !errors.Is(got, unrelated) || got.Error() != unrelated.Error() {
		t.Fatalf("a genuinely malformed call must pass its error through unchanged, got %v", got)
	}

	if got := DescribeExclusiveJobStartVersionSkew("petios", "rihards-develop", true, nil); got != nil {
		t.Fatalf("a nil error must stay nil, got %v", got)
	}
}
