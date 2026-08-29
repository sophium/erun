package main

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// TestDecodeWhipMCPResultNilStructuredContentIsAnError is the red-then-green
// contract for the bug this file exists to close: json.Unmarshal("null", &v)
// is a documented no-op, so a nil StructuredContent must never be allowed to
// decode straight into an indistinguishable-from-real zero WhipResult.
func TestDecodeWhipMCPResultNilStructuredContentIsAnError(t *testing.T) {
	result, err := decodeWhipMCPResult(&mcp.CallToolResult{StructuredContent: nil})
	if err == nil {
		t.Fatalf("expected a nil StructuredContent to be an error, got result %+v", result)
	}
	if result.Candidate.ID != "" || result.Decision != eruncommon.WhipDecisionNone {
		t.Fatalf("expected a zero result alongside the error, got %+v", result)
	}
}

// TestDecodeWhipMCPResultEmptyStructuredContentIsAnError covers the sibling
// case the issue calls out explicitly: {} decodes to a zero struct with no
// unmarshal error just as readily as null does.
func TestDecodeWhipMCPResultEmptyStructuredContentIsAnError(t *testing.T) {
	_, err := decodeWhipMCPResult(&mcp.CallToolResult{StructuredContent: map[string]any{}})
	if err == nil {
		t.Fatal("expected an empty StructuredContent object to be an error")
	}
}

// TestDecodeWhipMCPResultIsErrorSurfacesToolMessage covers the sibling this
// decode path was missing entirely: a tool-reported failure comes back as a
// populated CallToolResult with IsError set and no StructuredContent, not as
// a JSON-RPC error, so session.CallTool's own err is nil. Without an explicit
// IsError check this fell into the same nil-StructuredContent trap and
// silently reported success.
func TestDecodeWhipMCPResultIsErrorSurfacesToolMessage(t *testing.T) {
	_, err := decodeWhipMCPResult(&mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: "environment mismatch"}},
	})
	if err == nil {
		t.Fatal("expected IsError to be surfaced as an error")
	}
	if !strings.Contains(err.Error(), "environment mismatch") {
		t.Fatalf("expected the tool's own message to be carried, got %q", err.Error())
	}
}

// TestDecodeWhipMCPResultRejectsAnEmptyCandidateID covers a decoded result
// that unmarshalled cleanly but still names no target -- the identity a
// report row is judged by must never depend on what the pod chose to echo
// back.
func TestDecodeWhipMCPResultRejectsAnEmptyCandidateID(t *testing.T) {
	_, err := decodeWhipMCPResult(&mcp.CallToolResult{
		StructuredContent: map[string]any{"decision": 1, "pushed": true},
	})
	if err == nil {
		t.Fatal("expected a decoded result with an empty Candidate.ID to be an error")
	}
}

// TestDecodeWhipMCPResultPassesThroughARealResult is the green counterpart:
// a genuine decision with a stamped identity decodes and returns untouched.
func TestDecodeWhipMCPResultPassesThroughARealResult(t *testing.T) {
	result, err := decodeWhipMCPResult(&mcp.CallToolResult{
		StructuredContent: map[string]any{
			"candidate": map[string]any{"kind": "environment", "id": "erun/ux", "name": "erun/ux"},
			"decision":  eruncommon.WhipDecisionNudge,
			"pushed":    true,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Candidate.ID != "erun/ux" || result.Decision != eruncommon.WhipDecisionNudge || !result.Pushed {
		t.Fatalf("got %+v, want a passthrough of the decoded result", result)
	}
}
