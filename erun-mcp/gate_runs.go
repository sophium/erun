package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// gate_runs.go mirrors review.go's platform-call shape for the gate-run
// surface: the first-class record of one attempt to gate a
// prospective merge, independent of whether an erun review exists for the
// change it gates.

// GateRunResult is one gate run's tool result.
type GateRunResult struct {
	Preview bool                       `json:"preview"`
	Run     eruncommon.PlatformGateRun `json:"run,omitempty"`
	Trace   []string                   `json:"trace,omitempty"`
}

// GateRunListResult is a gate-run listing's tool result.
type GateRunListResult struct {
	Preview bool                         `json:"preview"`
	Runs    []eruncommon.PlatformGateRun `json:"runs,omitempty"`
	Trace   []string                     `json:"trace,omitempty"`
}

type ExecGateRunStartInput struct {
	platformAliasInput
	SourceBranch string `json:"sourceBranch" jsonschema:"branch being gated"`
	TargetBranch string `json:"targetBranch" jsonschema:"branch the prospective merge lands onto"`
	SourceCommit string `json:"sourceCommit" jsonschema:"source branch tip commit this run tested"`
	MergeCommit  string `json:"mergeCommit,omitempty" jsonschema:"prospective squash-merge commit this run tested; required unless status is FAILED or INCONCLUSIVE"`
	ReviewID     string `json:"reviewId,omitempty" jsonschema:"erun review this run gates, if one exists"`
	Status       string `json:"status,omitempty" jsonschema:"status to start at: RUNNING (default), FAILED, or INCONCLUSIVE — set FAILED/INCONCLUSIVE directly for a run with no trackable running phase, such as a squash conflict before any build starts"`
	FailingStep  string `json:"failingStep,omitempty" jsonschema:"which gate step failed; required when status is FAILED"`
	LogRef       string `json:"logRef,omitempty" jsonschema:"where to read this run's own output — a job id, URL, or path"`
}

func execGateRunStartTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecGateRunStartInput) (*mcp.CallToolResult, GateRunResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecGateRunStartInput) (*mcp.CallToolResult, GateRunResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "exec_gate-run_start"
		run, err := eruncommon.RunGateRunStart(ctx, runtime.Store, input.Alias, eruncommon.GateRunStartParams{
			SourceBranch: input.SourceBranch,
			TargetBranch: input.TargetBranch,
			SourceCommit: input.SourceCommit,
			MergeCommit:  input.MergeCommit,
			ReviewID:     input.ReviewID,
			Status:       input.Status,
			FailingStep:  input.FailingStep,
			LogRef:       input.LogRef,
		}, cloudDependencies())
		if err != nil {
			return nil, GateRunResult{}, err
		}
		return nil, GateRunResult{Preview: input.Preview, Run: run, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type ExecGateRunReportInput struct {
	platformAliasInput
	GateRunID   string `json:"gateRunId" jsonschema:"gate run id to report the outcome against"`
	Status      string `json:"status" jsonschema:"outcome: PASSED, FAILED, or INCONCLUSIVE"`
	FailingStep string `json:"failingStep,omitempty" jsonschema:"which gate step failed; required when status is FAILED"`
	LogRef      string `json:"logRef,omitempty" jsonschema:"where to read this run's own output — a job id, URL, or path"`
	MergeCommit string `json:"mergeCommit,omitempty" jsonschema:"prospective squash-merge commit, if not already set when the run started"`
}

func execGateRunReportTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecGateRunReportInput) (*mcp.CallToolResult, GateRunResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecGateRunReportInput) (*mcp.CallToolResult, GateRunResult, error) {
		if strings.TrimSpace(input.GateRunID) == "" {
			return nil, GateRunResult{}, fmt.Errorf("gateRunId is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "exec_gate-run_report"
		run, err := eruncommon.RunGateRunReport(ctx, runtime.Store, input.Alias, eruncommon.GateRunReportParams{
			GateRunID:   input.GateRunID,
			Status:      input.Status,
			FailingStep: input.FailingStep,
			LogRef:      input.LogRef,
			MergeCommit: input.MergeCommit,
		}, cloudDependencies())
		if err != nil {
			return nil, GateRunResult{}, err
		}
		return nil, GateRunResult{Preview: input.Preview, Run: run, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type GateListInput struct {
	platformAliasInput
	TargetBranch string `json:"targetBranch,omitempty" jsonschema:"filter by target branch"`
	SourceBranch string `json:"sourceBranch,omitempty" jsonschema:"filter by source branch"`
	Status       string `json:"status,omitempty" jsonschema:"filter by status: RUNNING, PASSED, FAILED, or INCONCLUSIVE"`
}

func gateListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, GateListInput) (*mcp.CallToolResult, GateRunListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input GateListInput) (*mcp.CallToolResult, GateRunListResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "gate_list"
		runs, err := eruncommon.RunGateRunList(ctx, runtime.Store, input.Alias, eruncommon.GateRunListParams{
			TargetBranch: input.TargetBranch,
			SourceBranch: input.SourceBranch,
			Status:       input.Status,
		}, cloudDependencies())
		if err != nil {
			return nil, GateRunListResult{}, err
		}
		return nil, GateRunListResult{Preview: input.Preview, Runs: runs, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

// ReconcileBypassToolResult is the reconcile-bypass tool's result.
type ReconcileBypassToolResult struct {
	Preview bool                             `json:"preview"`
	Result  eruncommon.ReconcileBypassResult `json:"result,omitempty"`
	Trace   []string                         `json:"trace,omitempty"`
}

type ExecReconcileBypassInput struct {
	platformAliasInput
	RemoteURL      string   `json:"remoteUrl,omitempty" jsonschema:"the github.com remote the ruleset lives on; defaults to the current checkout's origin"`
	RulesetID      int64    `json:"rulesetId" jsonschema:"the ruleset to check bypasses against"`
	TargetBranch   string   `json:"targetBranch" jsonschema:"the ruleset's protected branch"`
	Since          string   `json:"since,omitempty" jsonschema:"narrow the github lookup window: hour, day, week, or month; defaults to github's own window"`
	ExpectedActors []string `json:"expectedActors,omitempty" jsonschema:"identities allowed to hold the bypass grant; any other actor's bypass is reported UNEXPECTED_ACTOR"`
}

func execReconcileBypassTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecReconcileBypassInput) (*mcp.CallToolResult, ReconcileBypassToolResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecReconcileBypassInput) (*mcp.CallToolResult, ReconcileBypassToolResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "exec_reconcile-bypass"
		result, err := eruncommon.ReconcileBypass(ctx, runtime.Store, input.Alias, eruncommon.ReconcileBypassParams{
			RemoteURL:      input.RemoteURL,
			RulesetID:      input.RulesetID,
			TargetBranch:   input.TargetBranch,
			Since:          input.Since,
			ExpectedActors: input.ExpectedActors,
		}, cloudDependencies(), eruncommon.ReconcileBypassDependencies{})
		if err != nil {
			return nil, ReconcileBypassToolResult{}, err
		}
		return nil, ReconcileBypassToolResult{Preview: input.Preview, Result: result, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type GateShowInput struct {
	platformAliasInput
	GateRunID string `json:"gateRunId" jsonschema:"gate run id to show"`
}

func gateShowTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, GateShowInput) (*mcp.CallToolResult, GateRunResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input GateShowInput) (*mcp.CallToolResult, GateRunResult, error) {
		if strings.TrimSpace(input.GateRunID) == "" {
			return nil, GateRunResult{}, fmt.Errorf("gateRunId is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "gate_show"
		run, err := eruncommon.RunGateRunShow(ctx, runtime.Store, input.Alias, input.GateRunID, cloudDependencies())
		if err != nil {
			return nil, GateRunResult{}, err
		}
		return nil, GateRunResult{Preview: input.Preview, Run: run, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}
