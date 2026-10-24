package erunmcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-mcp/tools/review"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

const reviewCommentTestAlias = "erun-test"

// stubReviewCommentStore embeds the interface so any call beyond alias
// resolution panics instead of passing.
type stubReviewCommentStore struct {
	shared.RuntimeStore
	config configpkg.ERunConfig
}

func (s stubReviewCommentStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "/tmp/.erun/config.yaml", nil
}

func reviewCommentTestRuntime() shared.RuntimeConfig {
	return shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "acme", Environment: "dev"},
		Store: stubReviewCommentStore{config: configpkg.ERunConfig{
			CloudProviders: []model.CloudProviderConfig{{
				Alias:    reviewCommentTestAlias,
				Provider: configpkg.CloudProviderERun,
				ERun:     &model.ERunProviderConfig{APIURL: "https://api.test"},
			}},
		}},
	}
}

// callReviewCommentTool goes through a real MCP session so schema validation applies.
func callReviewCommentTool(t *testing.T, arguments map[string]any) review.ReviewCommentResult {
	t.Helper()
	session := connectTestMCPSession(t, model.BuildInfo{Version: "1.2.3"}, reviewCommentTestRuntime())
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
	var out review.ReviewCommentResult
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decode structured content %s: %v", encoded, err)
	}
	return out
}

func traceLine(result review.ReviewCommentResult) string {
	if len(result.Trace) != 1 {
		return ""
	}
	return result.Trace[0]
}

// Both transports drive RunReviewComment, so they must plan the same call byte for byte.
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

func TestReviewCommentInputSchemaRequiresOnlyReviewIDAndBody(t *testing.T) {
	session := connectTestMCPSession(t, model.BuildInfo{Version: "1.2.3"}, reviewCommentTestRuntime())
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
