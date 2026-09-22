package erunmcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

const reviewCommentTestAlias = "erun-test"

// stubReviewCommentStore satisfies runtimeStore's read surface with one erun
// platform alias. Embedding the interface leaves the rest unimplemented on
// purpose: resolving the alias reads the config and nothing else, and the
// preview path under test returns before any of the other methods, the
// platform client, or a bearer token is reached. A call that does reach one
// panics loudly rather than silently passing.
type stubReviewCommentStore struct {
	runtimeStore
	config eruncommon.ERunConfig
}

func (s stubReviewCommentStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	return s.config, "/tmp/.erun/config.yaml", nil
}

func reviewCommentTestRuntime() RuntimeConfig {
	return RuntimeConfig{
		Context: RuntimeContext{Tenant: "acme", Environment: "dev"},
		Store: stubReviewCommentStore{config: eruncommon.ERunConfig{
			CloudProviders: []eruncommon.CloudProviderConfig{{
				Alias:    reviewCommentTestAlias,
				Provider: eruncommon.CloudProviderERun,
				ERun:     &eruncommon.ERunProviderConfig{APIURL: "https://api.test"},
			}},
		}},
	}
}

// callReviewCommentTool drives the registered tool over a real MCP session,
// the same schema-validating path a client uses, so a declared-required field
// the caller omitted is caught here rather than only in the handler.
func callReviewCommentTool(t *testing.T, arguments map[string]any) ReviewCommentResult {
	t.Helper()
	session := connectTestMCPSession(t, eruncommon.BuildInfo{Version: "1.2.3"}, reviewCommentTestRuntime())
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "review_comment", Arguments: arguments})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("review_comment was refused: %s", resultText(result))
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out ReviewCommentResult
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decode structured content %s: %v", encoded, err)
	}
	return out
}

// traceLine returns the single planned platform call, the MCP analogue of the
// CLI's --dry-run trace, or "" when the result carries a different number of
// trace lines.
func traceLine(result ReviewCommentResult) string {
	if len(result.Trace) != 1 {
		return ""
	}
	return result.Trace[0]
}

// TestReviewCommentToolAcceptsAnUnanchoredComment is the parity the CLI has
// and MCP did not: both transports must plan the same call for a comment
// carrying no commit/file/line anchor, byte for byte, since both drive the
// same RunReviewComment. What the platform then does with an anchor-less
// comment is deliberately not asserted here -- this test pins the client
// contract, and the two transports disagreeing about it was the defect.
func TestReviewCommentToolAcceptsAnUnanchoredComment(t *testing.T) {
	result := callReviewCommentTool(t, map[string]any{
		"reviewId": "018f0000-0000-7000-8000-000000000000",
		"body":     "overall this looks good",
		"preview":  true,
	})

	if !result.Preview {
		t.Errorf("expected a preview result, got %+v", result)
	}
	want := "platform: POST https://api.test/v1/reviews/018f0000-0000-7000-8000-000000000000/comments (commitId=, filePath=, line=0)"
	if got := traceLine(result); got != want {
		t.Errorf("unanchored comment planned call:\n got %q\nwant %q", got, want)
	}
}

// TestReviewCommentToolKeepsALineAnchoredComment guards the direction that
// already worked: an anchor must still reach the server intact.
func TestReviewCommentToolKeepsALineAnchoredComment(t *testing.T) {
	result := callReviewCommentTool(t, map[string]any{
		"reviewId": "018f0000-0000-7000-8000-000000000000",
		"commitId": "abc123",
		"filePath": "main.go",
		"line":     42,
		"body":     "nit: rename this",
		"preview":  true,
	})

	want := "platform: POST https://api.test/v1/reviews/018f0000-0000-7000-8000-000000000000/comments (commitId=abc123, filePath=main.go, line=42)"
	if got := traceLine(result); got != want {
		t.Errorf("anchored comment planned call:\n got %q\nwant %q", got, want)
	}
}

// TestReviewCommentToolKeepsAReplyInItsThread guards the third shape:
// replying to an existing thread, which carries an anchor and a parent.
func TestReviewCommentToolKeepsAReplyInItsThread(t *testing.T) {
	result := callReviewCommentTool(t, map[string]any{
		"reviewId":        "018f0000-0000-7000-8000-000000000000",
		"commitId":        "abc123",
		"filePath":        "main.go",
		"line":            42,
		"body":            "good catch, fixed",
		"parentCommentId": "018g0000-0000-7000-8000-000000000000",
		"preview":         true,
	})

	want := "platform: POST https://api.test/v1/reviews/018f0000-0000-7000-8000-000000000000/comments (commitId=abc123, filePath=main.go, line=42, replyTo=018g0000-0000-7000-8000-000000000000)"
	if got := traceLine(result); got != want {
		t.Errorf("reply planned call:\n got %q\nwant %q", got, want)
	}
}

// TestReviewCommentToolRefusesACallMissingReviewIDOrBody keeps the remaining
// required inputs required, and keeps the refusal naming which one is
// missing rather than listing every field the tool accepts.
func TestReviewCommentToolRefusesACallMissingReviewIDOrBody(t *testing.T) {
	handler := reviewCommentTool(RuntimeConfig{})
	cases := []struct {
		name  string
		input ReviewCommentInput
		want  string
	}{
		{
			name:  "no reviewId",
			input: ReviewCommentInput{Body: "overall this looks good"},
			want:  "reviewId is required",
		},
		{
			name:  "no body",
			input: ReviewCommentInput{ReviewID: "018f0000-0000-7000-8000-000000000000"},
			want:  "body is required",
		},
		{
			name:  "whitespace-only body",
			input: ReviewCommentInput{ReviewID: "018f0000-0000-7000-8000-000000000000", Body: "  \n"},
			want:  "body is required",
		},
		{
			name:  "neither",
			input: ReviewCommentInput{},
			want:  "reviewId and body are required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No store is configured: the refusal must precede any platform
			// resolution, so a missing input never depends on config state.
			_, _, err := handler(context.Background(), nil, tc.input)
			if err == nil {
				t.Fatal("expected a refusal, got none")
			}
			if err.Error() != tc.want {
				t.Errorf("refusal message:\n got %q\nwant %q", err.Error(), tc.want)
			}
		})
	}
}

// TestReviewCommentInputSchemaRequiresOnlyReviewIDAndBody is the declared half
// of the same contract. A caller reading the schema, or a client that
// validates against it, must see the anchor as optional -- declaring it
// required was enough on its own to make the call unreachable.
func TestReviewCommentInputSchemaRequiresOnlyReviewIDAndBody(t *testing.T) {
	session := connectTestMCPSession(t, eruncommon.BuildInfo{Version: "1.2.3"}, reviewCommentTestRuntime())
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	var schema map[string]any
	for _, tool := range tools.Tools {
		if tool.Name == "review_comment" {
			asMap, ok := tool.InputSchema.(map[string]any)
			if !ok {
				t.Fatalf("review_comment InputSchema is %T, not map[string]any", tool.InputSchema)
			}
			schema = asMap
		}
	}
	if schema == nil {
		t.Fatal("review_comment is not registered")
	}
	_, required := schemaPropertiesAndRequired(t, &mcp.Tool{Name: "review_comment", InputSchema: schema})
	got := strings.Join(required, ",")
	if got != "reviewId,body" {
		t.Errorf("review_comment required properties = %q, want %q", got, "reviewId,body")
	}
}
