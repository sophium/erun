package eruncommon

import (
	"context"
	"fmt"
	"strings"
)

// gate_run_commands.go is the shared planning/execution layer `erun exec
// gate-run start`/`report` and `erun gate list`/`show` (CLI) and their MCP
// tools both drive, mirroring review_commands.go: resolve the erun platform
// alias, build a PlatformClient, trace the resolved HTTP call so --dry-run
// (CLI) and a preview path (MCP) never touch the network, then perform it
// for real.

// GateRunStartParams is the `erun exec gate-run start` input.
type GateRunStartParams struct {
	SourceBranch string
	TargetBranch string
	SourceCommit string
	MergeCommit  string
	ReviewID     string
	// Status defaults to RUNNING when empty. A caller with no trackable
	// running phase (a squash conflict before any build starts) sets this to
	// FAILED or INCONCLUSIVE directly and leaves MergeCommit empty.
	Status      string
	FailingStep string
	LogRef      string
}

// RunGateRunStart records the beginning (or, for a run with no trackable
// running phase, the immediate outcome) of one gate attempt.
func RunGateRunStart(ctx Context, store CloudReadStore, alias string, params GateRunStartParams, deps CloudDependencies) (PlatformGateRun, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformGateRun{}, err
	}
	details := []string{
		"sourceBranch=" + params.SourceBranch,
		"targetBranch=" + params.TargetBranch,
		"sourceCommit=" + params.SourceCommit,
	}
	if strings.TrimSpace(params.MergeCommit) != "" {
		details = append(details, "mergeCommit="+params.MergeCommit)
	}
	if strings.TrimSpace(params.ReviewID) != "" {
		details = append(details, "reviewId="+params.ReviewID)
	}
	if strings.TrimSpace(params.Status) != "" {
		details = append(details, "status="+params.Status)
	}
	if strings.TrimSpace(params.FailingStep) != "" {
		details = append(details, "failingStep="+params.FailingStep)
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/gate-runs", details...)
	if ctx.DryRun {
		return PlatformGateRun{}, nil
	}
	return client.StartGateRun(context.Background(), PlatformStartGateRunParams{
		SourceBranch: params.SourceBranch,
		TargetBranch: params.TargetBranch,
		SourceCommit: params.SourceCommit,
		MergeCommit:  params.MergeCommit,
		ReviewID:     params.ReviewID,
		Status:       params.Status,
		FailingStep:  params.FailingStep,
		LogRef:       params.LogRef,
	})
}

// GateRunReportParams is the `erun exec gate-run report` input. Status must
// be PASSED, FAILED, or INCONCLUSIVE — never RUNNING, which only Start ever
// assigns. FAILED requires FailingStep; a wrapper that never reached a real
// verdict (its own timeout, an environment fault) reports INCONCLUSIVE, not
// FAILED — see AGENTS.md "Gate Runs".
type GateRunReportParams struct {
	GateRunID   string
	Status      string
	FailingStep string
	LogRef      string
	MergeCommit string
}

// RunGateRunReport moves an existing gate run from RUNNING to a terminal
// status. Reporting against a gate run that already has one is refused.
func RunGateRunReport(ctx Context, store CloudReadStore, alias string, params GateRunReportParams, deps CloudDependencies) (PlatformGateRun, error) {
	if strings.TrimSpace(params.GateRunID) == "" {
		return PlatformGateRun{}, fmt.Errorf("gate run id is required")
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformGateRun{}, err
	}
	details := []string{"status=" + params.Status}
	if strings.TrimSpace(params.FailingStep) != "" {
		details = append(details, "failingStep="+params.FailingStep)
	}
	if strings.TrimSpace(params.LogRef) != "" {
		details = append(details, "logRef="+params.LogRef)
	}
	if strings.TrimSpace(params.MergeCommit) != "" {
		details = append(details, "mergeCommit="+params.MergeCommit)
	}
	tracePlatformCall(ctx, provider, "PATCH", "/v1/gate-runs/"+params.GateRunID, details...)
	if ctx.DryRun {
		return PlatformGateRun{}, nil
	}
	return client.ReportGateRunOutcome(context.Background(), params.GateRunID, PlatformReportGateRunOutcomeParams{
		Status:      params.Status,
		FailingStep: params.FailingStep,
		LogRef:      params.LogRef,
		MergeCommit: params.MergeCommit,
	})
}

// GateRunListParams is the `erun gate list` input.
type GateRunListParams struct {
	TargetBranch string
	SourceBranch string
	Status       string
}

// RunGateRunList lists gate runs visible to the caller's tenant, most recent
// first, narrowed by the given filters. This is the queue view that answers
// what is being gated right now (status=RUNNING), what is waiting
// (the review-queue's own READY entries, via `erun review queue list`, are
// the complementary half this does not duplicate), and what recent gates
// decided.
func RunGateRunList(ctx Context, store CloudReadStore, alias string, params GateRunListParams, deps CloudDependencies) ([]PlatformGateRun, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	filter := PlatformGateRunFilter{TargetBranch: params.TargetBranch, SourceBranch: params.SourceBranch, Status: params.Status}
	details := gateRunFilterTraceDetails(filter)
	tracePlatformCall(ctx, provider, "GET", "/v1/gate-runs", details...)
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListGateRuns(context.Background(), filter)
}

// RunGateRunShow fetches one gate run by id.
func RunGateRunShow(ctx Context, store CloudReadStore, alias, gateRunID string, deps CloudDependencies) (PlatformGateRun, error) {
	if strings.TrimSpace(gateRunID) == "" {
		return PlatformGateRun{}, fmt.Errorf("gate run id is required")
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformGateRun{}, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/gate-runs/"+gateRunID)
	if ctx.DryRun {
		return PlatformGateRun{}, nil
	}
	return client.GetGateRun(context.Background(), gateRunID)
}

func gateRunFilterTraceDetails(filter PlatformGateRunFilter) []string {
	var details []string
	if strings.TrimSpace(filter.TargetBranch) != "" {
		details = append(details, "targetBranch="+filter.TargetBranch)
	}
	if strings.TrimSpace(filter.SourceBranch) != "" {
		details = append(details, "sourceBranch="+filter.SourceBranch)
	}
	if strings.TrimSpace(filter.Status) != "" {
		details = append(details, "status="+filter.Status)
	}
	return details
}
