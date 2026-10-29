package platformclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// pipelineFixtureBody holds a branchless job and a review whose issue link is inferred.
const pipelineFixtureBody = `[{"issueKey":"sophium/erun#2683","items":[
	{"issueKey":"sophium/erun#2683","issueRef":"sophium/erun#2683","issueRefSource":"DECLARED","rung":"PLANNED",
	 "job":{"jobId":"job-1","jobType":"triage","issueRef":"sophium/erun#2683","summary":"plan the view","status":"PLANNED","actorKind":"orchestrator","actorId":"erun/ideas","startedAt":"2026-09-25T09:00:00Z"}},
	{"issueKey":"sophium/erun#2683","issueRef":"2683","issueRefSource":"INFERRED","rung":"REVIEW_OPEN",
	 "review":{"reviewId":"review-1","repository":"github.com/sophium/erun","name":"Add the view","targetBranch":"main","sourceBranch":"feature/2683-planned-job-status-and-pipeline-view","status":"OPEN"}}
]}]`

func pipelineFixtureServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/pipeline" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func assertPipelinePlannedJob(t *testing.T, item PlatformPipelineItem) {
	t.Helper()
	if item.Job == nil || item.Review != nil {
		t.Fatalf("expected a job and only a job, got %+v", item)
	}
	if item.Rung != "PLANNED" || item.Job.JobType != "triage" {
		t.Fatalf("unexpected planned item: %+v", item)
	}
	if item.Job.EndedAt != nil {
		t.Fatalf("a planned job must carry no endedAt, got %v", item.Job.EndedAt)
	}
}

// IssueRefSource is the only way to tell an inferred link from a declared one.
func assertPipelineOpenReview(t *testing.T, item PlatformPipelineItem) {
	t.Helper()
	if item.Review == nil || item.Job != nil {
		t.Fatalf("expected a review and only a review, got %+v", item)
	}
	if item.IssueRefSource != "INFERRED" {
		t.Fatalf("issueRefSource = %q, want the branch-derived link to arrive as INFERRED", item.IssueRefSource)
	}
}

func TestPlatformClientGetPipelineCarriesBothHalvesOfTheUnion(t *testing.T) {
	srv := pipelineFixtureServer(t, pipelineFixtureBody)
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	issues, err := client.GetPipeline(context.Background())
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	if len(issues) != 1 || len(issues[0].Items) != 2 {
		t.Fatalf("issues = %+v", issues)
	}
	assertPipelinePlannedJob(t, issues[0].Items[0])
	assertPipelineOpenReview(t, issues[0].Items[1])
}

func TestPlatformClientGetPipelineKeepsWorkThatNamesNoIssue(t *testing.T) {
	srv := pipelineFixtureServer(t, `[{"issueKey":"","items":[{"issueKey":"","rung":"FAILED","job":{"jobId":"job-2","jobType":"fix","summary":"why did this fail","status":"FAILED","actorKind":"agent","actorId":"agent-1"}}]}]`)
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	issues, err := client.GetPipeline(context.Background())
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	if len(issues) != 1 || issues[0].IssueKey != "" {
		t.Fatalf("issues = %+v, want one keyless group", issues)
	}
	if item := issues[0].Items[0]; item.IssueRef != "" || item.Job == nil {
		t.Fatalf("item = %+v", item)
	}
}
