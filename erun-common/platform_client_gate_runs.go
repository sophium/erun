package eruncommon

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// platform_client_gate_runs.go extends PlatformClient with the gate-run
// surface: the first-class record of one attempt to gate a
// prospective merge, independent of whether an erun review exists for the
// change it gates.

// PlatformGateRun mirrors model.GateRun's JSON shape.
type PlatformGateRun struct {
	GateRunID    string    `json:"gateRunId"`
	TenantID     string    `json:"tenantId"`
	SourceBranch string    `json:"sourceBranch"`
	TargetBranch string    `json:"targetBranch"`
	SourceCommit string    `json:"sourceCommit"`
	MergeCommit  string    `json:"mergeCommit,omitempty"`
	ReviewID     string    `json:"reviewId,omitempty"`
	ReviewName   string    `json:"reviewName,omitempty"`
	Status       string    `json:"status"`
	FailingStep  string    `json:"failingStep,omitempty"`
	LogRef       string    `json:"logRef,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// PlatformStartGateRunParams is the gate-run start input. Status defaults to
// RUNNING server-side when empty; a caller with no trackable "running" phase
// (e.g. a squash conflict before any build starts) may set Status directly
// to FAILED/INCONCLUSIVE and leave MergeCommit empty.
type PlatformStartGateRunParams struct {
	SourceBranch string `json:"sourceBranch"`
	TargetBranch string `json:"targetBranch"`
	SourceCommit string `json:"sourceCommit"`
	MergeCommit  string `json:"mergeCommit,omitempty"`
	ReviewID     string `json:"reviewId,omitempty"`
	Status       string `json:"status,omitempty"`
	FailingStep  string `json:"failingStep,omitempty"`
	LogRef       string `json:"logRef,omitempty"`
}

// StartGateRun records the beginning (or, for a run with no trackable
// running phase, the immediate outcome) of one gate attempt.
func (c *PlatformClient) StartGateRun(ctx context.Context, params PlatformStartGateRunParams) (PlatformGateRun, error) {
	var run PlatformGateRun
	err := c.do(ctx, http.MethodPost, "/v1/gate-runs", params, true, &run)
	return run, err
}

// PlatformReportGateRunOutcomeParams is the gate-run outcome-report input.
// MergeCommit is optional: omit it to keep whatever StartGateRun recorded,
// or set it here when the caller only learns it once the squash-merge
// succeeds after an already-started run (not the common case, since Start
// is normally called after the squash already succeeded).
type PlatformReportGateRunOutcomeParams struct {
	Status      string `json:"status"`
	FailingStep string `json:"failingStep,omitempty"`
	LogRef      string `json:"logRef,omitempty"`
	MergeCommit string `json:"mergeCommit,omitempty"`
}

// ReportGateRunOutcome moves an existing gate run from RUNNING to a terminal
// status. Reporting against a gate run that already has one is refused
// (ErrPlatformConflict).
func (c *PlatformClient) ReportGateRunOutcome(ctx context.Context, gateRunID string, params PlatformReportGateRunOutcomeParams) (PlatformGateRun, error) {
	var run PlatformGateRun
	err := c.do(ctx, http.MethodPatch, "/v1/gate-runs/"+url.PathEscape(gateRunID), params, true, &run)
	return run, err
}

// GetGateRun fetches one gate run by id.
func (c *PlatformClient) GetGateRun(ctx context.Context, gateRunID string) (PlatformGateRun, error) {
	var run PlatformGateRun
	err := c.do(ctx, http.MethodGet, "/v1/gate-runs/"+url.PathEscape(gateRunID), nil, true, &run)
	return run, err
}

// PlatformGateRunFilter mirrors the discovery filters GET /v1/gate-runs
// accepts.
type PlatformGateRunFilter struct {
	TargetBranch string
	SourceBranch string
	Status       string
}

func (f PlatformGateRunFilter) queryString() string {
	values := url.Values{}
	if strings.TrimSpace(f.TargetBranch) != "" {
		values.Set("targetBranch", f.TargetBranch)
	}
	if strings.TrimSpace(f.SourceBranch) != "" {
		values.Set("sourceBranch", f.SourceBranch)
	}
	if strings.TrimSpace(f.Status) != "" {
		values.Set("status", f.Status)
	}
	return values.Encode()
}

// ListGateRuns lists gate runs visible to the caller's tenant, most recent
// first, narrowed by filter.
func (c *PlatformClient) ListGateRuns(ctx context.Context, filter PlatformGateRunFilter) ([]PlatformGateRun, error) {
	path := "/v1/gate-runs"
	if query := filter.queryString(); query != "" {
		path += "?" + query
	}
	var runs []PlatformGateRun
	err := c.do(ctx, http.MethodGet, path, nil, true, &runs)
	return runs, err
}
