package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	reviewpkg "github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-mcp/tools/shared/clouddeps"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

type ReviewListInput struct {
	toolio.PlatformAliasInput
	TargetBranch   string `json:"targetBranch,omitempty" jsonschema:"filter by target branch"`
	Repository     string `json:"repository,omitempty" jsonschema:"filter by repository, in any form git accepts; not defaulted from the checkout, since a listing is how you find work across every repository"`
	SourceBranch   string `json:"sourceBranch,omitempty" jsonschema:"filter by source branch"`
	Status         string `json:"status,omitempty" jsonschema:"filter by status: OPEN, CLOSED, FAILED, READY, MERGE, or MERGED"`
	AuthorUserID   string `json:"authorUserId,omitempty" jsonschema:"filter by author user id"`
	ReviewerUserID string `json:"reviewerUserId,omitempty" jsonschema:"filter by reviewer user id"`
	Mine           bool   `json:"mine,omitempty" jsonschema:"show only reviews the caller authored; resolves the caller's user id via whoami and cannot be combined with authorUserId"`
	WaitingOnMe    bool   `json:"waitingOnMe,omitempty" jsonschema:"show only reviews the caller is a reviewer on; resolves the caller's user id via whoami and cannot be combined with reviewerUserId"`
}

type ReviewListResult struct {
	Preview bool                   `json:"preview"`
	Reviews []model.PlatformReview `json:"reviews,omitempty"`
	Trace   []string               `json:"trace,omitempty"`
}

func ReviewListTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewListInput) (*mcp.CallToolResult, ReviewListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewListInput) (*mcp.CallToolResult, ReviewListResult, error) {
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_list"
		reviews, err := reviewpkg.RunReviewList(ctx, runtime.Store, input.Alias, model.ReviewListParams{
			Repository:   input.Repository,
			TargetBranch: input.TargetBranch, SourceBranch: input.SourceBranch, Status: input.Status,
			AuthorUserID: input.AuthorUserID, ReviewerUserID: input.ReviewerUserID,
			Mine: input.Mine, WaitingOnMe: input.WaitingOnMe,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewListResult{}, err
		}
		return nil, ReviewListResult{Preview: input.Preview, Reviews: reviews, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewShowInput struct {
	toolio.PlatformAliasInput
	ReviewID string `json:"reviewId" jsonschema:"review id to fetch"`
}

type ReviewShowResult struct {
	Preview bool               `json:"preview"`
	Detail  model.ReviewDetail `json:"detail,omitempty"`
	Trace   []string           `json:"trace,omitempty"`
}

func ReviewShowTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewShowInput) (*mcp.CallToolResult, ReviewShowResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewShowInput) (*mcp.CallToolResult, ReviewShowResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" {
			return nil, ReviewShowResult{}, fmt.Errorf("reviewId is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_show"
		detail, err := reviewpkg.RunReviewShow(ctx, runtime.Store, input.Alias, input.ReviewID, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewShowResult{}, err
		}
		return nil, ReviewShowResult{Preview: input.Preview, Detail: detail, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewCreateInput struct {
	toolio.PlatformAliasInput
	Name         string `json:"name" jsonschema:"review name (unique per tenant and repository among reviews that can still land; the eventual squash-merge message)"`
	Repository   string `json:"repository" jsonschema:"repository the branches belong to, in any form git accepts; defaults to this agent's own checkout origin"`
	TargetBranch string `json:"targetBranch" jsonschema:"branch this review proposes merging into"`
	SourceBranch string `json:"sourceBranch" jsonschema:"branch this review proposes merging; must already be pushed to the remote (use exec_push)"`
	IssueRef     string `json:"issueRef" jsonschema:"issue this work belongs to, as owner/repo#number or as a bare number the platform joins to the recorded repository; recorded as a declared link, which outranks the number the source branch names. Omit to leave the branch as the whole link"`
}

type ReviewResult struct {
	Preview bool                 `json:"preview"`
	Review  model.PlatformReview `json:"review,omitempty"`
	Trace   []string             `json:"trace,omitempty"`
}

func ReviewCreateTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewCreateInput) (*mcp.CallToolResult, ReviewResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewCreateInput) (*mcp.CallToolResult, ReviewResult, error) {
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.TargetBranch) == "" || strings.TrimSpace(input.SourceBranch) == "" {
			return nil, ReviewResult{}, fmt.Errorf("name, targetBranch, and sourceBranch are required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_create"
		review, err := reviewpkg.RunReviewCreate(ctx, runtime.Store, input.Alias, model.PlatformCreateReviewParams{
			Repository: input.Repository, Name: input.Name, TargetBranch: input.TargetBranch, SourceBranch: input.SourceBranch,
			IssueRef: input.IssueRef,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewResult{}, err
		}
		return nil, ReviewResult{Preview: input.Preview, Review: review, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

// ReviewCommentInput's anchor is optional, matching the CLI; the platform
// decides whether an unanchored comment is valid.
type ReviewCommentInput struct {
	toolio.PlatformAliasInput
	ReviewID        string `json:"reviewId" jsonschema:"review id to comment on"`
	CommitID        string `json:"commitId,omitempty" jsonschema:"commit hash the comment is anchored to"`
	FilePath        string `json:"filePath,omitempty" jsonschema:"file path the comment is anchored to"`
	Line            int    `json:"line,omitempty" jsonschema:"line number the comment is anchored to"`
	Body            string `json:"body" jsonschema:"comment text"`
	ParentCommentID string `json:"parentCommentId,omitempty" jsonschema:"comment id to reply to, making this a reply in that thread"`
}

type ReviewCommentResult struct {
	Preview bool                  `json:"preview"`
	Comment model.PlatformComment `json:"comment,omitempty"`
	Trace   []string              `json:"trace,omitempty"`
}

func ReviewCommentTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewCommentInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewCommentInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
		var missing []string
		if strings.TrimSpace(input.ReviewID) == "" {
			missing = append(missing, "reviewId")
		}
		if strings.TrimSpace(input.Body) == "" {
			missing = append(missing, "body")
		}
		if len(missing) > 0 {
			verb := " is"
			if len(missing) > 1 {
				verb = " are"
			}
			return nil, ReviewCommentResult{}, fmt.Errorf("%s%s required", strings.Join(missing, " and "), verb)
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_comment"
		comment, err := reviewpkg.RunReviewComment(ctx, runtime.Store, input.Alias, model.ReviewCommentParams{
			ReviewID: input.ReviewID, CommitID: input.CommitID, FilePath: input.FilePath,
			Line: input.Line, Body: input.Body, ParentCommentID: input.ParentCommentID,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewCommentResult{}, err
		}
		return nil, ReviewCommentResult{Preview: input.Preview, Comment: comment, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewCloseInput struct {
	toolio.PlatformAliasInput
	ReviewID string `json:"reviewId" jsonschema:"review id to close"`
}

func ReviewCloseTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewCloseInput) (*mcp.CallToolResult, ReviewResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewCloseInput) (*mcp.CallToolResult, ReviewResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" {
			return nil, ReviewResult{}, fmt.Errorf("reviewId is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_close"
		review, err := reviewpkg.RunReviewClose(ctx, runtime.Store, input.Alias, input.ReviewID, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewResult{}, err
		}
		return nil, ReviewResult{Preview: input.Preview, Review: review, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewRecordBuildInput struct {
	toolio.PlatformAliasInput
	ReviewID      string `json:"reviewId" jsonschema:"review id to record the build against"`
	CommitID      string `json:"commitId" jsonschema:"full 40-character commit hash the build ran against"`
	Version       string `json:"version" jsonschema:"version the build minted, from the run's own build result (a plain erun build --output json, or --dry-run --output json when it failed before printing one), required even for a failed build; a recorded build publishes nothing, so this is metadata no platform path resolves and a --release produced version is accepted but not required"`
	Successful    bool   `json:"successful" jsonschema:"whether the build succeeded; false records a failed build"`
	FailureDetail string `json:"failureDetail,omitempty" jsonschema:"why the build failed; only meaningful when successful is false"`
}

type ReviewRecordBuildResult struct {
	Preview bool                `json:"preview"`
	Build   model.PlatformBuild `json:"build,omitempty"`
	Trace   []string            `json:"trace,omitempty"`
}

// ReviewRecordBuildTool moves a review off OPEN to READY or FAILED, and to
// MERGE if it heads the queue.
func ReviewRecordBuildTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewRecordBuildInput) (*mcp.CallToolResult, ReviewRecordBuildResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewRecordBuildInput) (*mcp.CallToolResult, ReviewRecordBuildResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" || strings.TrimSpace(input.CommitID) == "" {
			return nil, ReviewRecordBuildResult{}, fmt.Errorf("reviewId and commitId are required")
		}
		if strings.TrimSpace(input.Version) == "" {
			return nil, ReviewRecordBuildResult{}, fmt.Errorf("version is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_record-build"
		build, err := reviewpkg.RunReviewRecordBuild(ctx, runtime.Store, input.Alias, model.ReviewRecordBuildParams{
			ReviewID: input.ReviewID, CommitID: input.CommitID, Version: input.Version,
			Successful: input.Successful, FailureDetail: input.FailureDetail,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewRecordBuildResult{}, err
		}
		return nil, ReviewRecordBuildResult{Preview: input.Preview, Build: build, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewReportMergedInput struct {
	toolio.PlatformAliasInput
	ReviewID  string `json:"reviewId" jsonschema:"review id to report merged"`
	BuildID   string `json:"buildId,omitempty" jsonschema:"the successful recorded build of the merge commit that landed; omit for work that landed without the queue"`
	RemoteURL string `json:"remoteUrl" jsonschema:"the git remote the platform fetches to verify the merge, in any form git accepts; an SSH remote is read over its host's HTTPS without credentials"`
}

type ReviewReportMergedResult struct {
	Preview bool                 `json:"preview"`
	Review  model.PlatformReview `json:"review,omitempty"`
	Trace   []string             `json:"trace,omitempty"`
}

// ReviewReportMergedTool reports a landed merge; the platform verifies it, and
// MERGE_NOT_VERIFIED leaves the review at MERGE.
func ReviewReportMergedTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewReportMergedInput) (*mcp.CallToolResult, ReviewReportMergedResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewReportMergedInput) (*mcp.CallToolResult, ReviewReportMergedResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" || strings.TrimSpace(input.RemoteURL) == "" {
			return nil, ReviewReportMergedResult{}, fmt.Errorf("reviewId and remoteUrl are required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_report-merged"
		review, err := reviewpkg.RunReviewReportMerged(ctx, runtime.Store, input.Alias, input.ReviewID, input.BuildID, input.RemoteURL, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewReportMergedResult{}, err
		}
		return nil, ReviewReportMergedResult{Preview: input.Preview, Review: review, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewRequeueInput struct {
	toolio.PlatformAliasInput
	ReviewID string `json:"reviewId" jsonschema:"review id to requeue; must currently be at MERGE"`
}

// ReviewRequeueTool moves a review from MERGE back to READY, freeing the
// target branch's single MERGE slot.
func ReviewRequeueTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewRequeueInput) (*mcp.CallToolResult, ReviewResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewRequeueInput) (*mcp.CallToolResult, ReviewResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" {
			return nil, ReviewResult{}, fmt.Errorf("reviewId is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_requeue"
		review, err := reviewpkg.RunReviewRequeue(ctx, runtime.Store, input.Alias, input.ReviewID, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewResult{}, err
		}
		return nil, ReviewResult{Preview: input.Preview, Review: review, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewCommentStatusInput struct {
	toolio.PlatformAliasInput
	ReviewID  string `json:"reviewId" jsonschema:"review id the comment belongs to"`
	CommentID string `json:"commentId" jsonschema:"comment id to change status on; must be a thread's root comment, not a reply"`
}

func ReviewResolveTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewCommentStatusInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewCommentStatusInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" || strings.TrimSpace(input.CommentID) == "" {
			return nil, ReviewCommentResult{}, fmt.Errorf("reviewId and commentId are required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_resolve"
		comment, err := reviewpkg.RunReviewResolve(ctx, runtime.Store, input.Alias, input.ReviewID, input.CommentID, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewCommentResult{}, err
		}
		return nil, ReviewCommentResult{Preview: input.Preview, Comment: comment, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

func ReviewUnresolveTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewCommentStatusInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewCommentStatusInput) (*mcp.CallToolResult, ReviewCommentResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" || strings.TrimSpace(input.CommentID) == "" {
			return nil, ReviewCommentResult{}, fmt.Errorf("reviewId and commentId are required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_unresolve"
		comment, err := reviewpkg.RunReviewUnresolve(ctx, runtime.Store, input.Alias, input.ReviewID, input.CommentID, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewCommentResult{}, err
		}
		return nil, ReviewCommentResult{Preview: input.Preview, Comment: comment, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewReviewersListInput struct {
	toolio.PlatformAliasInput
	ReviewID string `json:"reviewId" jsonschema:"review id to list reviewers for"`
}

type ReviewReviewersListResult struct {
	Preview   bool                     `json:"preview"`
	Reviewers []model.PlatformReviewer `json:"reviewers,omitempty"`
	Trace     []string                 `json:"trace,omitempty"`
}

func ReviewReviewersListTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewReviewersListInput) (*mcp.CallToolResult, ReviewReviewersListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewReviewersListInput) (*mcp.CallToolResult, ReviewReviewersListResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" {
			return nil, ReviewReviewersListResult{}, fmt.Errorf("reviewId is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_reviewers_list"
		reviewers, err := reviewpkg.RunReviewReviewersList(ctx, runtime.Store, input.Alias, input.ReviewID, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewReviewersListResult{}, err
		}
		return nil, ReviewReviewersListResult{Preview: input.Preview, Reviewers: reviewers, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewReviewerAddInput struct {
	toolio.PlatformAliasInput
	ReviewID string `json:"reviewId" jsonschema:"review id to assign a reviewer on"`
	UserID   string `json:"userId" jsonschema:"user id to assign as a reviewer; must already be enrolled in the caller's own tenant"`
}

type ReviewReviewerResult struct {
	Preview  bool                   `json:"preview"`
	Reviewer model.PlatformReviewer `json:"reviewer,omitempty"`
	Trace    []string               `json:"trace,omitempty"`
}

func ReviewReviewerAddTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewReviewerAddInput) (*mcp.CallToolResult, ReviewReviewerResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewReviewerAddInput) (*mcp.CallToolResult, ReviewReviewerResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" || strings.TrimSpace(input.UserID) == "" {
			return nil, ReviewReviewerResult{}, fmt.Errorf("reviewId and userId are required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_reviewers_add"
		reviewer, err := reviewpkg.RunReviewReviewerAdd(ctx, runtime.Store, input.Alias, input.ReviewID, input.UserID, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewReviewerResult{}, err
		}
		return nil, ReviewReviewerResult{Preview: input.Preview, Reviewer: reviewer, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewReviewerRemoveInput struct {
	toolio.PlatformAliasInput
	ReviewID string `json:"reviewId" jsonschema:"review id to remove a reviewer from"`
	UserID   string `json:"userId" jsonschema:"user id to remove as a reviewer"`
}

type ReviewReviewerRemoveResult struct {
	Preview bool     `json:"preview"`
	Trace   []string `json:"trace,omitempty"`
}

func ReviewReviewerRemoveTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewReviewerRemoveInput) (*mcp.CallToolResult, ReviewReviewerRemoveResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewReviewerRemoveInput) (*mcp.CallToolResult, ReviewReviewerRemoveResult, error) {
		if strings.TrimSpace(input.ReviewID) == "" || strings.TrimSpace(input.UserID) == "" {
			return nil, ReviewReviewerRemoveResult{}, fmt.Errorf("reviewId and userId are required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_reviewers_remove"
		if err := reviewpkg.RunReviewReviewerRemove(ctx, runtime.Store, input.Alias, input.ReviewID, input.UserID, clouddeps.CloudDependencies()); err != nil {
			return nil, ReviewReviewerRemoveResult{}, err
		}
		return nil, ReviewReviewerRemoveResult{Preview: input.Preview, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewMergeQueueListInput struct {
	toolio.PlatformAliasInput
	TargetBranch string `json:"targetBranch" jsonschema:"target branch to list the merge queue for"`
	Repository   string `json:"repository" jsonschema:"repository whose queue to list, in any form git accepts; defaults to this agent's own checkout origin"`
}

func ReviewMergeQueueListTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewMergeQueueListInput) (*mcp.CallToolResult, ReviewListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewMergeQueueListInput) (*mcp.CallToolResult, ReviewListResult, error) {
		if strings.TrimSpace(input.TargetBranch) == "" {
			return nil, ReviewListResult{}, fmt.Errorf("targetBranch is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_queue_list"
		reviews, err := reviewpkg.RunReviewMergeQueueList(ctx, runtime.Store, input.Alias, model.PlatformMergeQueueParams{
			Repository: input.Repository, TargetBranch: input.TargetBranch,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewListResult{}, err
		}
		return nil, ReviewListResult{Preview: input.Preview, Reviews: reviews, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewMergeQueueAdvanceInput struct {
	toolio.PlatformAliasInput
	TargetBranch string `json:"targetBranch" jsonschema:"target branch whose merge queue to advance"`
	Repository   string `json:"repository" jsonschema:"repository whose queue to advance, in any form git accepts; defaults to this agent's own checkout origin"`
}

func ReviewMergeQueueAdvanceTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewMergeQueueAdvanceInput) (*mcp.CallToolResult, ReviewResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewMergeQueueAdvanceInput) (*mcp.CallToolResult, ReviewResult, error) {
		if strings.TrimSpace(input.TargetBranch) == "" {
			return nil, ReviewResult{}, fmt.Errorf("targetBranch is required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_queue_advance"
		review, err := reviewpkg.RunReviewMergeQueueAdvance(ctx, runtime.Store, input.Alias, model.PlatformMergeQueueParams{
			Repository: input.Repository, TargetBranch: input.TargetBranch,
		}, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewResult{}, err
		}
		return nil, ReviewResult{Preview: input.Preview, Review: review, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}

type ReviewMergeQueueOverrideAdvanceInput struct {
	toolio.PlatformAliasInput
	TargetBranch string `json:"targetBranch" jsonschema:"target branch whose merge queue to advance"`
	Repository   string `json:"repository" jsonschema:"repository whose queue to advance, in any form git accepts; defaults to this agent's own checkout origin"`
	Reason       string `json:"reason" jsonschema:"why the unresolved-thread gate is being bypassed; required, and recorded in the platform's audit trail"`
}

func ReviewMergeQueueOverrideAdvanceTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ReviewMergeQueueOverrideAdvanceInput) (*mcp.CallToolResult, ReviewResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ReviewMergeQueueOverrideAdvanceInput) (*mcp.CallToolResult, ReviewResult, error) {
		if strings.TrimSpace(input.TargetBranch) == "" || strings.TrimSpace(input.Reason) == "" {
			return nil, ReviewResult{}, fmt.Errorf("targetBranch and reason are required")
		}
		traceOutput := strings.Builder{}
		ctx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "review_queue_override-advance"
		review, err := reviewpkg.RunReviewMergeQueueOverrideAdvance(ctx, runtime.Store, input.Alias, model.PlatformMergeQueueParams{
			Repository: input.Repository, TargetBranch: input.TargetBranch,
		}, input.Reason, clouddeps.CloudDependencies())
		if err != nil {
			return nil, ReviewResult{}, err
		}
		return nil, ReviewResult{Preview: input.Preview, Review: review, Trace: toolio.NormalizeTraceLines(traceOutput.String())}, nil
	}
}
