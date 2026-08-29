package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// review.go exposes `erun review`'s operations as MCP tools over the same
// shared eruncommon.RunReview* functions the CLI drives
// (erun-cli/cmd/review.go), mirroring platform.go's pattern so preview
// (this module's non-interactive analogue of --dry-run) reuses the exact
// same trace rather than a separately hand-maintained plan.

type ReviewListInput struct {
	platformAliasInput
	TargetBranch   string `json:"targetBranch,omitempty" jsonschema:"filter by target branch"`
	SourceBranch   string `json:"sourceBranch,omitempty" jsonschema:"filter by source branch"`
	Status         string `json:"status,omitempty" jsonschema:"filter by status: OPEN, CLOSED, FAILED, READY, MERGE, or MERGED"`
	AuthorUserID   string `json:"authorUserId,omitempty" jsonschema:"filter by author user id"`
	ReviewerUserID string `json:"reviewerUserId,omitempty" jsonschema:"filter by reviewer user id"`
	Mine           bool   `json:"mine,omitempty" jsonschema:"show only reviews the caller authored; resolves the caller's user id via whoami and cannot be combined with authorUserId"`
	WaitingOnMe    bool   `json:"waitingOnMe,omitempty" jsonschema:"show only reviews the caller is a reviewer on; resolves the caller's user id via whoami and cannot be combined with reviewerUserId"`
}

type ReviewListResult struct {
	Preview bool                        `json:"preview"`
	Reviews []eruncommon.PlatformReview `json:"reviews,omitempty"`
	Trace   []string                    `json:"trace,omitempty"`
}

func reviewListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewListInput) (*mcp.CallToolResult, ReviewListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewListInput) (*mcp.CallToolResult, ReviewListResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		reviews, err := eruncommon.RunReviewList(ctx, runtime.Store, input.Alias, eruncommon.ReviewListParams{
			TargetBranch: input.TargetBranch, SourceBranch: input.SourceBranch, Status: input.Status,
			AuthorUserID: input.AuthorUserID, ReviewerUserID: input.ReviewerUserID,
			Mine: input.Mine, WaitingOnMe: input.WaitingOnMe,
		}, cloudDependencies())
		if err != nil {
			return nil, ReviewListResult{}, err
		}
		return nil, ReviewListResult{Preview: input.Preview, Reviews: reviews, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewShowInput struct {
	platformAliasInput
	ReviewID string `json:"reviewId" jsonschema:"review id to fetch"`
}

type ReviewShowResult struct {
	Preview bool                    `json:"preview"`
	Detail  eruncommon.ReviewDetail `json:"detail,omitempty"`
	Trace   []string                `json:"trace,omitempty"`
}

func reviewShowTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewShowInput) (*mcp.CallToolResult, ReviewShowResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewShowInput) (*mcp.CallToolResult, ReviewShowResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" {
			return nil, ReviewShowResult{}, fmt.Errorf("reviewId is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		detail, err := eruncommon.RunReviewShow(ctx, runtime.Store, input.Alias, input.ReviewID, cloudDependencies())
		if err != nil {
			return nil, ReviewShowResult{}, err
		}
		return nil, ReviewShowResult{Preview: input.Preview, Detail: detail, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewCreateInput struct {
	platformAliasInput
	Name         string `json:"name" jsonschema:"review name (unique per tenant; the eventual squash-merge message)"`
	TargetBranch string `json:"targetBranch" jsonschema:"branch this review proposes merging into"`
	SourceBranch string `json:"sourceBranch" jsonschema:"branch this review proposes merging; must already be pushed to the remote (use exec_push)"`
}

type ReviewResult struct {
	Preview bool                      `json:"preview"`
	Review  eruncommon.PlatformReview `json:"review,omitempty"`
	Trace   []string                  `json:"trace,omitempty"`
}

func reviewCreateTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewCreateInput) (*mcp.CallToolResult, ReviewResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewCreateInput) (*mcp.CallToolResult, ReviewResult, error) {
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.TargetBranch) == "" || strings.TrimSpace(input.SourceBranch) == "" {
			return nil, ReviewResult{}, fmt.Errorf("name, targetBranch, and sourceBranch are required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		review, err := eruncommon.RunReviewCreate(ctx, runtime.Store, input.Alias, eruncommon.PlatformCreateReviewParams{
			Name: input.Name, TargetBranch: input.TargetBranch, SourceBranch: input.SourceBranch,
		}, cloudDependencies())
		if err != nil {
			return nil, ReviewResult{}, err
		}
		return nil, ReviewResult{Preview: input.Preview, Review: review, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewCommentInput struct {
	platformAliasInput
	ReviewID        string `json:"reviewId" jsonschema:"review id to comment on"`
	CommitID        string `json:"commitId" jsonschema:"commit hash the comment is anchored to"`
	FilePath        string `json:"filePath" jsonschema:"file path the comment is anchored to"`
	Line            int    `json:"line" jsonschema:"line number the comment is anchored to"`
	Body            string `json:"body" jsonschema:"comment text"`
	ParentCommentID string `json:"parentCommentId,omitempty" jsonschema:"comment id to reply to, making this a reply in that thread"`
}

type ReviewCommentResult struct {
	Preview bool                       `json:"preview"`
	Comment eruncommon.PlatformComment `json:"comment,omitempty"`
	Trace   []string                   `json:"trace,omitempty"`
}

func reviewCommentTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewCommentInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewCommentInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" || strings.TrimSpace(input.CommitID) == "" || strings.TrimSpace(input.FilePath) == "" || strings.TrimSpace(input.Body) == "" {
			return nil, ReviewCommentResult{}, fmt.Errorf("reviewId, commitId, filePath, and body are required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		comment, err := eruncommon.RunReviewComment(ctx, runtime.Store, input.Alias, eruncommon.ReviewCommentParams{
			ReviewID: input.ReviewID, CommitID: input.CommitID, FilePath: input.FilePath,
			Line: input.Line, Body: input.Body, ParentCommentID: input.ParentCommentID,
		}, cloudDependencies())
		if err != nil {
			return nil, ReviewCommentResult{}, err
		}
		return nil, ReviewCommentResult{Preview: input.Preview, Comment: comment, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewCloseInput struct {
	platformAliasInput
	ReviewID string `json:"reviewId" jsonschema:"review id to close"`
}

func reviewCloseTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewCloseInput) (*mcp.CallToolResult, ReviewResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewCloseInput) (*mcp.CallToolResult, ReviewResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" {
			return nil, ReviewResult{}, fmt.Errorf("reviewId is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		review, err := eruncommon.RunReviewClose(ctx, runtime.Store, input.Alias, input.ReviewID, cloudDependencies())
		if err != nil {
			return nil, ReviewResult{}, err
		}
		return nil, ReviewResult{Preview: input.Preview, Review: review, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewRecordBuildInput struct {
	platformAliasInput
	ReviewID      string `json:"reviewId" jsonschema:"review id to record the build against"`
	CommitID      string `json:"commitId" jsonschema:"full 40-character commit hash the build ran against"`
	Version       string `json:"version" jsonschema:"version the build minted (from the build tool's result), required even for a failed build since release resolves the version before the build step runs"`
	Successful    bool   `json:"successful" jsonschema:"whether the build succeeded; false records a failed build"`
	FailureDetail string `json:"failureDetail,omitempty" jsonschema:"why the build failed; only meaningful when successful is false"`
}

type ReviewRecordBuildResult struct {
	Preview bool                     `json:"preview"`
	Build   eruncommon.PlatformBuild `json:"build,omitempty"`
	Trace   []string                 `json:"trace,omitempty"`
}

// reviewRecordBuildTool is the only way an erun client transitions a review
// off OPEN: recording a build moves it to READY (successful) or FAILED (not),
// and on to MERGE if it was already the merge queue's head. There is no
// separate "set review status" tool for this — a READY with no build is a
// different thing entirely (the missed-merge-window requeue).
func reviewRecordBuildTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewRecordBuildInput) (*mcp.CallToolResult, ReviewRecordBuildResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewRecordBuildInput) (*mcp.CallToolResult, ReviewRecordBuildResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" || strings.TrimSpace(input.CommitID) == "" || strings.TrimSpace(input.Version) == "" {
			return nil, ReviewRecordBuildResult{}, fmt.Errorf("reviewId, commitId, and version are required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		build, err := eruncommon.RunReviewRecordBuild(ctx, runtime.Store, input.Alias, eruncommon.ReviewRecordBuildParams{
			ReviewID: input.ReviewID, CommitID: input.CommitID, Version: input.Version,
			Successful: input.Successful, FailureDetail: input.FailureDetail,
		}, cloudDependencies())
		if err != nil {
			return nil, ReviewRecordBuildResult{}, err
		}
		return nil, ReviewRecordBuildResult{Preview: input.Preview, Build: build, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewCommentStatusInput struct {
	platformAliasInput
	ReviewID  string `json:"reviewId" jsonschema:"review id the comment belongs to"`
	CommentID string `json:"commentId" jsonschema:"comment id to change status on; must be a thread's root comment, not a reply"`
}

func reviewResolveTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewCommentStatusInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewCommentStatusInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" || strings.TrimSpace(input.CommentID) == "" {
			return nil, ReviewCommentResult{}, fmt.Errorf("reviewId and commentId are required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		comment, err := eruncommon.RunReviewResolve(ctx, runtime.Store, input.Alias, input.ReviewID, input.CommentID, cloudDependencies())
		if err != nil {
			return nil, ReviewCommentResult{}, err
		}
		return nil, ReviewCommentResult{Preview: input.Preview, Comment: comment, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

func reviewUnresolveTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewCommentStatusInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewCommentStatusInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" || strings.TrimSpace(input.CommentID) == "" {
			return nil, ReviewCommentResult{}, fmt.Errorf("reviewId and commentId are required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		comment, err := eruncommon.RunReviewUnresolve(ctx, runtime.Store, input.Alias, input.ReviewID, input.CommentID, cloudDependencies())
		if err != nil {
			return nil, ReviewCommentResult{}, err
		}
		return nil, ReviewCommentResult{Preview: input.Preview, Comment: comment, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewMergeQueueListInput struct {
	platformAliasInput
	TargetBranch string `json:"targetBranch" jsonschema:"target branch to list the merge queue for"`
}

func reviewMergeQueueListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewMergeQueueListInput) (*mcp.CallToolResult, ReviewListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewMergeQueueListInput) (*mcp.CallToolResult, ReviewListResult, error) {
		if strings.TrimSpace(input.TargetBranch) == "" {
			return nil, ReviewListResult{}, fmt.Errorf("targetBranch is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		reviews, err := eruncommon.RunReviewMergeQueueList(ctx, runtime.Store, input.Alias, input.TargetBranch, cloudDependencies())
		if err != nil {
			return nil, ReviewListResult{}, err
		}
		return nil, ReviewListResult{Preview: input.Preview, Reviews: reviews, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewMergeQueueAdvanceInput struct {
	platformAliasInput
	TargetBranch string `json:"targetBranch" jsonschema:"target branch whose merge queue to advance"`
}

func reviewMergeQueueAdvanceTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewMergeQueueAdvanceInput) (*mcp.CallToolResult, ReviewResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewMergeQueueAdvanceInput) (*mcp.CallToolResult, ReviewResult, error) {
		if strings.TrimSpace(input.TargetBranch) == "" {
			return nil, ReviewResult{}, fmt.Errorf("targetBranch is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		review, err := eruncommon.RunReviewMergeQueueAdvance(ctx, runtime.Store, input.Alias, input.TargetBranch, cloudDependencies())
		if err != nil {
			return nil, ReviewResult{}, err
		}
		return nil, ReviewResult{Preview: input.Preview, Review: review, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewMergeQueueOverrideAdvanceInput struct {
	platformAliasInput
	TargetBranch string `json:"targetBranch" jsonschema:"target branch whose merge queue to advance"`
	Reason       string `json:"reason" jsonschema:"why the unresolved-thread gate is being bypassed; required, and recorded in the platform's audit trail"`
}

func reviewMergeQueueOverrideAdvanceTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewMergeQueueOverrideAdvanceInput) (*mcp.CallToolResult, ReviewResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewMergeQueueOverrideAdvanceInput) (*mcp.CallToolResult, ReviewResult, error) {
		if strings.TrimSpace(input.TargetBranch) == "" || strings.TrimSpace(input.Reason) == "" {
			return nil, ReviewResult{}, fmt.Errorf("targetBranch and reason are required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		review, err := eruncommon.RunReviewMergeQueueOverrideAdvance(ctx, runtime.Store, input.Alias, input.TargetBranch, input.Reason, cloudDependencies())
		if err != nil {
			return nil, ReviewResult{}, err
		}
		return nil, ReviewResult{Preview: input.Preview, Review: review, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}
