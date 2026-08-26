package eruncommon

import (
	"errors"
	"strings"
	"testing"
)

// An exclusive lease take against an environment whose MCP edge predates the
// exclusive-claim mechanism gets the go-sdk's own raw JSON-schema rejection
// of "exclusive"/"orchestrator" as unknown arguments -- indistinguishable
// from a caller typo. This is the exact wire text reported against erun
// 1.0.201.
const rawExclusiveLeaseSchemaError = `MCP tools/call failed: invalid params: validating "arguments": validating root: unexpected additional properties ["orchestrator" "exclusive"] (code -32602)`

func TestDescribeExclusiveActivityLeaseVersionSkewNamesTheVersionMismatch(t *testing.T) {
	err := errors.New(rawExclusiveLeaseSchemaError)
	described := DescribeExclusiveActivityLeaseVersionSkew("petios", "rihards-develop", true, err)
	if described == nil {
		t.Fatal("described error is nil")
	}
	message := described.Error()
	for _, want := range []string{"petios/rihards-develop", "older than the one that added --exclusive", "activity_lease_take"} {
		if !strings.Contains(message, want) {
			t.Fatalf("described error %q does not mention %q", message, want)
		}
	}
	if !errors.Is(described, err) {
		t.Fatalf("described error does not wrap the original: %v", described)
	}
}

func TestDescribeExclusiveActivityLeaseVersionSkewLeavesOtherFailuresAlone(t *testing.T) {
	notExclusive := errors.New(rawExclusiveLeaseSchemaError)
	if got := DescribeExclusiveActivityLeaseVersionSkew("petios", "rihards-develop", false, notExclusive); got != notExclusive {
		t.Fatalf("a non-exclusive call must pass its error through unchanged, got %v", got)
	}

	unrelated := errors.New(`MCP tools/call failed: invalid params: validating "arguments": validating "name": value is required (code -32602)`)
	if got := DescribeExclusiveActivityLeaseVersionSkew("petios", "rihards-develop", true, unrelated); got != unrelated {
		t.Fatalf("a genuinely malformed call must pass its error through unchanged, got %v", got)
	}

	if got := DescribeExclusiveActivityLeaseVersionSkew("petios", "rihards-develop", true, nil); got != nil {
		t.Fatalf("a nil error must stay nil, got %v", got)
	}
}
