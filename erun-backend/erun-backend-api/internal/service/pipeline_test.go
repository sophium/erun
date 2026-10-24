package service

import (
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

func pipelineJob(status model.JobStatus, issueRef string) model.Job {
	return model.Job{JobID: "job-" + string(status), Status: status, IssueRef: issueRef, Summary: "work"}
}

func pipelineReview(status model.ReviewStatus, repository, sourceBranch, declaredIssueRef string) model.Review {
	return model.Review{
		ReviewID:         "review-" + string(status),
		Status:           status,
		Repository:       repository,
		SourceBranch:     sourceBranch,
		DeclaredIssueRef: declaredIssueRef,
		Name:             "work",
	}
}

func groupFor(t *testing.T, issues []PipelineIssue, issueKey string) PipelineIssue {
	t.Helper()
	for _, issue := range issues {
		if issue.IssueKey == issueKey {
			return issue
		}
	}
	t.Fatalf("no group keyed %q in %+v", issueKey, issues)
	return PipelineIssue{}
}

// Neither side spells the key the same way: the job states owner/repo#number,
// the review's branch number is joined to its recorded repository.
func TestGroupPipelineUnionsAJobAndAReviewOnTheCanonicalKey(t *testing.T) {
	issues := GroupPipeline(
		[]model.Job{pipelineJob(model.JobStatusPlanned, "sophium/erun#2683")},
		[]model.Review{pipelineReview(model.ReviewStatusOpen, "https://github.com/sophium/erun", "bug/2683-planned-jobs", "")},
	)

	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want one group: the job and the review name the same issue", issues)
	}
	issue := issues[0]
	if issue.IssueKey != "sophium/erun#2683" {
		t.Fatalf("issueKey = %q, want %q", issue.IssueKey, "sophium/erun#2683")
	}
	if len(issue.Items) != 2 {
		t.Fatalf("items = %+v, want the job and the review in one group", issue.Items)
	}
	if issue.Items[0].Job == nil || issue.Items[0].Rung != rungPlanned {
		t.Fatalf("first item = %+v, want the PLANNED job on the ladder's first rung", issue.Items[0])
	}
	if issue.Items[1].Review == nil || issue.Items[1].Rung != rungReviewOpen {
		t.Fatalf("second item = %+v, want the review at REVIEW_OPEN", issue.Items[1])
	}
}

func TestGroupPipelineLabelsEveryRung(t *testing.T) {
	jobRungs := map[model.JobStatus]string{
		model.JobStatusPlanned: rungPlanned,
		model.JobStatusRunning: rungInProgress,
	}
	for status, want := range jobRungs {
		issues := GroupPipeline([]model.Job{pipelineJob(status, "")}, nil)
		if len(issues) != 1 || len(issues[0].Items) != 1 {
			t.Fatalf("GroupPipeline(%s) = %+v, want one item", status, issues)
		}
		if got := issues[0].Items[0].Rung; got != want {
			t.Errorf("job status %s rung = %q, want %q", status, got, want)
		}
	}

	reviewRungs := map[model.ReviewStatus]string{
		model.ReviewStatusOpen:   rungReviewOpen,
		model.ReviewStatusReady:  rungReady,
		model.ReviewStatusMerge:  rungMerging,
		model.ReviewStatusMerged: rungMerged,
	}
	for status, want := range reviewRungs {
		issues := GroupPipeline(nil, []model.Review{pipelineReview(status, "", "feature/widget", "")})
		if len(issues) != 1 || len(issues[0].Items) != 1 {
			t.Fatalf("GroupPipeline(%s) = %+v, want one item", status, issues)
		}
		if got := issues[0].Items[0].Rung; got != want {
			t.Errorf("review status %s rung = %q, want %q", status, got, want)
		}
	}
}

func TestGroupPipelineKeepsAnOutcomeOutOfTheLadder(t *testing.T) {
	issues := GroupPipeline(
		[]model.Job{pipelineJob(model.JobStatusRunning, "sophium/erun#1"), pipelineJob(model.JobStatusAbandoned, "sophium/erun#1")},
		[]model.Review{pipelineReview(model.ReviewStatusFailed, "https://github.com/sophium/erun", "feature/1-x", "")},
	)
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want one group", issues)
	}
	items := issues[0].Items
	if len(items) != 3 {
		t.Fatalf("items = %+v, want all three", items)
	}
	if items[0].Rung != rungInProgress {
		t.Errorf("first rung = %q, want %q: the ladder sorts first", items[0].Rung, rungInProgress)
	}
	for _, item := range items[1:] {
		if item.Rung == rungInProgress || item.Rung == rungReady {
			t.Errorf("outcome %+v was placed on a ladder rung", item)
		}
	}
}

func TestGroupPipelineRendersABranchDerivedLinkAsInferred(t *testing.T) {
	issues := GroupPipeline(nil, []model.Review{
		pipelineReview(model.ReviewStatusOpen, "https://github.com/sophium/erun", "bug/2212-issue-ref-from-branch", ""),
	})
	if len(issues) != 1 || len(issues[0].Items) != 1 {
		t.Fatalf("issues = %+v, want one item", issues)
	}
	item := issues[0].Items[0]
	if item.IssueRef != "2212" {
		t.Errorf("issueRef = %q, want the branch's own number", item.IssueRef)
	}
	if item.IssueRefSource != modelpkg.IssueReferenceInferred {
		t.Errorf("issueRefSource = %q, want %q", item.IssueRefSource, modelpkg.IssueReferenceInferred)
	}
	if item.IssueKey != "sophium/erun#2212" {
		t.Errorf("issueKey = %q, want the branch's number joined to the review's repository", item.IssueKey)
	}
}

// A declared reference wins over the branch number.
func TestGroupPipelineRendersADeclaredLinkAsDeclared(t *testing.T) {
	issues := GroupPipeline(nil, []model.Review{
		pipelineReview(model.ReviewStatusReady, "https://github.com/sophium/erun", "bug/2212-issue-ref-from-branch", "sophium/erun#2683"),
	})
	item := groupFor(t, issues, "sophium/erun#2683").Items[0]
	if item.IssueRef != "sophium/erun#2683" {
		t.Errorf("issueRef = %q, want the declared reference", item.IssueRef)
	}
	if item.IssueRefSource != modelpkg.IssueReferenceDeclared {
		t.Errorf("issueRefSource = %q, want %q", item.IssueRefSource, modelpkg.IssueReferenceDeclared)
	}
}

func TestGroupPipelineShowsUnlinkedWorkWithoutAnIssue(t *testing.T) {
	issues := GroupPipeline(
		[]model.Job{
			pipelineJob(model.JobStatusRunning, "sophium/erun#2683"),
			pipelineJob(model.JobStatusRunning, ""),
		},
		[]model.Review{pipelineReview(model.ReviewStatusOpen, "https://github.com/sophium/erun", "feature/widget", "")},
	)
	if len(issues) != 2 {
		t.Fatalf("issues = %+v, want the named issue and the unlinked group", issues)
	}
	if issues[0].IssueKey != "sophium/erun#2683" {
		t.Fatalf("first group key = %q, want the named issue first", issues[0].IssueKey)
	}
	unlinked := issues[len(issues)-1]
	if unlinked.IssueKey != "" {
		t.Fatalf("last group key = %q, want the unlinked group last", unlinked.IssueKey)
	}
	if len(unlinked.Items) != 2 {
		t.Fatalf("unlinked items = %+v, want both pieces of work", unlinked.Items)
	}
	for _, item := range unlinked.Items {
		if item.IssueRef != "" || item.IssueRefSource != "" {
			t.Errorf("unlinked item = %+v, want no issue link at all rather than a guessed one", item)
		}
	}
}

func TestGroupPipelineRefusesToGuessAKeyFromAnUnreadableRepository(t *testing.T) {
	issues := GroupPipeline(nil, []model.Review{
		pipelineReview(model.ReviewStatusOpen, "file:///tmp/erun", "bug/2212-issue-ref-from-branch", ""),
	})
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want one group", issues)
	}
	if issues[0].IssueKey != "" {
		t.Fatalf("issueKey = %q, want none: a file remote names no owner/repo", issues[0].IssueKey)
	}
	// Only the key is unavailable; the link is still reported as inferred.
	if issues[0].Items[0].IssueRef != "2212" {
		t.Errorf("issueRef = %q, want the branch's number kept", issues[0].Items[0].IssueRef)
	}
}

func TestGroupPipelineOfNothingIsAnEmptyList(t *testing.T) {
	issues := GroupPipeline(nil, nil)
	if issues == nil {
		t.Fatal("GroupPipeline() = nil, want an empty slice")
	}
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none", issues)
	}
}
