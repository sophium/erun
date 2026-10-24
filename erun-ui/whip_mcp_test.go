package main

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
)

func TestDecodeWhipMCPResultNilStructuredContentIsAnError(t *testing.T) {
	result, err := decodeWhipMCPResult(&mcp.CallToolResult{StructuredContent: nil})
	if err == nil {
		t.Fatalf("expected a nil StructuredContent to be an error, got result %+v", result)
	}
	if result.Candidate.ID != "" || result.Decision != model.WhipDecisionNone {
		t.Fatalf("expected a zero result alongside the error, got %+v", result)
	}
}

func TestDecodeWhipMCPResultEmptyStructuredContentIsAnError(t *testing.T) {
	_, err := decodeWhipMCPResult(&mcp.CallToolResult{StructuredContent: map[string]any{}})
	if err == nil {
		t.Fatal("expected an empty StructuredContent object to be an error")
	}
}

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

func TestDecodeWhipMCPResultRejectsAnEmptyCandidateID(t *testing.T) {
	_, err := decodeWhipMCPResult(&mcp.CallToolResult{
		StructuredContent: map[string]any{"decision": 1, "pushed": true},
	})
	if err == nil {
		t.Fatal("expected a decoded result with an empty Candidate.ID to be an error")
	}
}

func TestDecodeWhipMCPResultPassesThroughARealResult(t *testing.T) {
	result, err := decodeWhipMCPResult(&mcp.CallToolResult{
		StructuredContent: map[string]any{
			"candidate": map[string]any{"kind": "environment", "id": "erun/ux", "name": "erun/ux"},
			"decision":  model.WhipDecisionNudge,
			"pushed":    true,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Candidate.ID != "erun/ux" || result.Decision != model.WhipDecisionNudge || !result.Pushed {
		t.Fatalf("got %+v, want a passthrough of the decoded result", result)
	}
}
