package backendapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

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

// TestPipelineShowsAPlannedJobAgainstItsIssueWithNoBranchInExistence: work must be visible on its
// issue before any branch exists, and a PLANNED row must keep a null ended_at.
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
