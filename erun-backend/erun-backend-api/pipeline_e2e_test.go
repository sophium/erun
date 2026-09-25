package backendapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// pipelineE2EIssue is one row of GET /v1/pipeline, as a caller reads it.
type pipelineE2EIssue struct {
	IssueKey string `json:"issueKey"`
	Items    []struct {
		IssueKey       string     `json:"issueKey"`
		IssueRef       string     `json:"issueRef"`
		IssueRefSource string     `json:"issueRefSource"`
		Rung           string     `json:"rung"`
		Job            *model.Job `json:"job"`
	} `json:"items"`
}

// readPipeline drives the view and returns it, failing on anything but 200.
func readPipeline(t *testing.T, baseURL string) []pipelineE2EIssue {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodGet, "/v1/pipeline", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/pipeline: HTTP %d: %s", code, body)
	}
	var issues []pipelineE2EIssue
	mustNoErr(t, json.Unmarshal([]byte(body), &issues), "parse pipeline")
	return issues
}

// pipelineItemFor returns the item carrying jobID, wherever the union filed
// it, so a test can assert both where the work landed and what it is labelled
// without depending on what else the tenant holds.
func pipelineItemFor(t *testing.T, issues []pipelineE2EIssue, jobID string) (string, string) {
	t.Helper()
	for _, issue := range issues {
		for _, item := range issue.Items {
			if item.Job != nil && item.Job.JobID == jobID {
				return issue.IssueKey, item.Rung
			}
		}
	}
	t.Fatalf("job %s appears in no pipeline group: %+v", jobID, issues)
	return "", ""
}

// TestPipelineShowsAPlannedJobAgainstItsIssueWithNoBranchInExistence is the
// required case for the view's first rung, driven through the real API against
// a real migrated PostgreSQL.
//
// The premise of the whole request is that a unit of work should be visible
// against its issue before any code exists. Nothing here creates a branch, a
// review, or a build: one triage job is recorded in PLANNED and the view has
// to show it, on its issue, on the ladder's first rung -- and it has to still
// be there with no ended_at, because a planned row the platform made look
// finished would be the migration failing at the one job it had.
func TestPipelineShowsAPlannedJobAgainstItsIssueWithNoBranchInExistence(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	issueRef := "pipeline-e2e-" + unique + "/erun#2683"

	code, body := e2eRequest(t, srv.URL, http.MethodPost, "/v1/jobs", map[string]any{
		"jobType":   "triage",
		"issueRef":  issueRef,
		"summary":   "plan the pipeline view",
		"status":    string(model.JobStatusPlanned),
		"actorKind": "orchestrator",
		"actorId":   "erun/ideas",
		"scope":     issueRef,
	})
	if code != http.StatusCreated {
		t.Fatalf("claim planned job: HTTP %d: %s", code, body)
	}
	var claimed model.Job
	mustNoErr(t, json.Unmarshal([]byte(body), &claimed), "parse claimed job")
	if claimed.Status != model.JobStatusPlanned {
		t.Fatalf("claimed status = %q, want %q", claimed.Status, model.JobStatusPlanned)
	}
	if claimed.EndedAt != nil {
		t.Errorf("endedAt = %v on the planned job, want none", claimed.EndedAt)
	}

	issueKey, rung := pipelineItemFor(t, readPipeline(t, srv.URL), claimed.JobID)
	if issueKey != issueRef {
		t.Errorf("the job was filed under %q, want its own issue %q", issueKey, issueRef)
	}
	if rung != "PLANNED" {
		t.Errorf("rung = %q, want PLANNED: the ladder's first rung is work that has not started", rung)
	}
}

// TestPipelineMovesAPlannedJobUpTheLadderAsItStarts is the other half of the
// transition, at the same layer: the same job, opened to RUNNING, is reported
// on the in-progress rung -- still on its issue, and still without an
// ended_at, because it has not stopped.
func TestPipelineMovesAPlannedJobUpTheLadderAsItStarts(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	issueRef := "pipeline-e2e-" + unique + "/erun#2684"

	code, body := e2eRequest(t, srv.URL, http.MethodPost, "/v1/jobs", map[string]any{
		"jobType":   "plan",
		"issueRef":  issueRef,
		"summary":   "plan the pipeline view",
		"status":    string(model.JobStatusPlanned),
		"actorKind": "orchestrator",
		"actorId":   "erun/ideas",
	})
	if code != http.StatusCreated {
		t.Fatalf("claim planned job: HTTP %d: %s", code, body)
	}
	var claimed model.Job
	mustNoErr(t, json.Unmarshal([]byte(body), &claimed), "parse claimed job")

	code, body = e2eRequest(t, srv.URL, http.MethodPatch, "/v1/jobs/"+claimed.JobID, map[string]any{
		"status": string(model.JobStatusRunning), "summary": "coding the pipeline view",
	})
	if code != http.StatusOK {
		t.Fatalf("open the plan: HTTP %d: %s", code, body)
	}

	issueKey, rung := pipelineItemFor(t, readPipeline(t, srv.URL), claimed.JobID)
	if issueKey != issueRef {
		t.Errorf("the job was filed under %q, want its own issue %q", issueKey, issueRef)
	}
	if rung != "IN_PROGRESS" {
		t.Errorf("rung = %q, want IN_PROGRESS", rung)
	}
}
