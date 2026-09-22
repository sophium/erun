package eruncommon

import (
	"errors"
	"strings"
	"testing"
)

// job start's off-environment dispatch (exec_raw/exec_agent) gates on
// --exclusive the same way activity_lease_take does, so an edge that
// predates the feature produces the identical raw schema-rejection shape --
// see activity_lease_version_skew_test.go's rawExclusiveLeaseSchemaError for
// the field-observed wire text this is modeled on.
const rawExclusiveJobStartSchemaError = `MCP tools/call failed: invalid params: validating "arguments": validating root: unexpected additional properties ["exclusive"] (code -32602)`

func TestDescribeExclusiveJobStartVersionSkewNamesTheVersionMismatch(t *testing.T) {
	err := errors.New(rawExclusiveJobStartSchemaError)
	described := DescribeExclusiveJobStartVersionSkew("petios", "rihards-develop", "1.0.247", true, err)
	if described == nil {
		t.Fatal("described error is nil")
	}
	message := described.Error()
	for _, want := range []string{"petios/rihards-develop", "older than the one that added --exclusive", "job start", "erun >= 1.0.248", "reports 1.0.247"} {
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
	if got := DescribeExclusiveJobStartVersionSkew("petios", "rihards-develop", "1.0.247", false, notExclusive); got != notExclusive {
		t.Fatalf("a non-exclusive call must pass its error through unchanged, got %v", got)
	}

	unrelated := errors.New(`MCP tools/call failed: invalid params: validating "arguments": validating "name": value is required (code -32602)`)
	if got := DescribeExclusiveJobStartVersionSkew("petios", "rihards-develop", "1.0.247", true, unrelated); got != unrelated {
		t.Fatalf("a genuinely malformed call must pass its error through unchanged, got %v", got)
	}

	if got := DescribeExclusiveJobStartVersionSkew("petios", "rihards-develop", "1.0.247", true, nil); got != nil {
		t.Fatalf("a nil error must stay nil, got %v", got)
	}
}

// An unreadable environment version must degrade the sentence, never suppress
// the refusal: the caller still has to learn that no claim was taken.
func TestDescribeExclusiveJobStartVersionSkewWithoutAReportedVersion(t *testing.T) {
	err := errors.New(rawExclusiveJobStartSchemaError)
	described := DescribeExclusiveJobStartVersionSkew("petios", "rihards-develop", "", true, err)
	if described == nil {
		t.Fatal("an unreadable environment version must not suppress the refusal")
	}
	message := described.Error()
	for _, want := range []string{"erun >= 1.0.248", "reports no version", "older than the one that added --exclusive"} {
		if !strings.Contains(message, want) {
			t.Fatalf("described error %q does not mention %q", message, want)
		}
	}
	if !errors.Is(described, err) {
		t.Fatalf("described error does not wrap the original: %v", described)
	}
}

func TestIsExclusiveClaimVersionSkewMatchesOnlyTheSkewShape(t *testing.T) {
	if !IsExclusiveClaimVersionSkew(true, errors.New(rawExclusiveJobStartSchemaError)) {
		t.Fatal("the raw schema rejection of exclusive must be recognised as a version skew")
	}
	if IsExclusiveClaimVersionSkew(false, errors.New(rawExclusiveJobStartSchemaError)) {
		t.Fatal("a non-exclusive call must never be reported as a version skew")
	}
	if IsExclusiveClaimVersionSkew(true, nil) {
		t.Fatal("a nil error must never be reported as a version skew")
	}
	unrelated := errors.New(`MCP tools/call failed: invalid params: validating "arguments": validating "name": value is required (code -32602)`)
	if IsExclusiveClaimVersionSkew(true, unrelated) {
		t.Fatal("a genuinely malformed call must never be reported as a version skew")
	}
	missingOtherProperty := errors.New(`MCP tools/call failed: invalid params: validating "arguments": validating root: unexpected additional properties ["handoff"] (code -32602)`)
	if IsExclusiveClaimVersionSkew(true, missingOtherProperty) {
		t.Fatal("an unknown property that is not exclusive/orchestrator must not be reported as an exclusive version skew")
	}
}
