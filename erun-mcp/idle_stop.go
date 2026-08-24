package erunmcp

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// IdleStopCancelInput selects the env whose armed idle-stop should be cancelled.
type IdleStopCancelInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should have its pending stop cancelled; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to cancel; defaults to the server environment context, and must match it: this server only acts on its own environment"`
}

// IdleStopCancelResult reports the cancel outcome. Cleared is true even when
// nothing was armed, so it must not be read as proof a pending stop existed.
type IdleStopCancelResult struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Cleared     bool   `json:"cleared"`
}

func idleStopCancelTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleStopCancelInput) (*mcp.CallToolResult, IdleStopCancelResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleStopCancelInput) (*mcp.CallToolResult, IdleStopCancelResult, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, IdleStopCancelResult{}, err
		}
		if err := eruncommon.ClearEnvironmentStopPending(tenant, environment); err != nil {
			return nil, IdleStopCancelResult{}, err
		}
		return nil, IdleStopCancelResult{
			Tenant:      tenant,
			Environment: environment,
			Cleared:     true,
		}, nil
	}
}

// IdleStopHistoryInput selects the env whose stop history to return.
type IdleStopHistoryInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should be queried; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to query; defaults to the server environment context, and must match it: this server only acts on its own environment"`
}

// IdleStopHistoryResult wraps the entries so the MCP schema stays a stable
// object. Entries are newest-first, capped at common.StopHistoryCap.
type IdleStopHistoryResult struct {
	Tenant      string                                   `json:"tenant"`
	Environment string                                   `json:"environment"`
	Entries     []eruncommon.EnvironmentStopHistoryEntry `json:"entries"`
}

func idleStopHistoryTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleStopHistoryInput) (*mcp.CallToolResult, IdleStopHistoryResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleStopHistoryInput) (*mcp.CallToolResult, IdleStopHistoryResult, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, IdleStopHistoryResult{}, err
		}
		entries, err := eruncommon.LoadEnvironmentStopHistory(tenant, environment)
		if err != nil {
			return nil, IdleStopHistoryResult{}, err
		}
		return nil, IdleStopHistoryResult{
			Tenant:      tenant,
			Environment: environment,
			Entries:     entries,
		}, nil
	}
}

// IdleStopRecordInput drives a host-triggered manual stop record. The tool
// runs in the pod so the entry lands on the shared home PVC alongside the
// in-pod monitor's auto-stop records, keeping manual and automatic stops in
// one history.
type IdleStopRecordInput struct {
	Tenant           string `json:"tenant,omitempty" jsonschema:"tenant whose environment should have a stop entry recorded; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment      string `json:"environment,omitempty" jsonschema:"environment to record against; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Reason           string `json:"reason,omitempty" jsonschema:"reason text rendered on the History row; empty falls back to a generic 'Manual stop'"`
	CloudContextName string `json:"cloudContextName,omitempty" jsonschema:"cloud context name the stop targeted; informational"`
}

// IdleStopRecordResult echoes the recorded StoppedAt so the caller can match
// this row against a later history fetch.
type IdleStopRecordResult struct {
	Tenant      string    `json:"tenant"`
	Environment string    `json:"environment"`
	StoppedAt   time.Time `json:"stoppedAt"`
}

func idleStopRecordTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleStopRecordInput) (*mcp.CallToolResult, IdleStopRecordResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleStopRecordInput) (*mcp.CallToolResult, IdleStopRecordResult, error) {
		tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, IdleStopRecordResult{}, err
		}
		reason := strings.TrimSpace(input.Reason)
		if reason == "" {
			reason = "Manual stop"
		}
		now := time.Now().UTC()
		entry := eruncommon.EnvironmentStopHistoryEntry{
			StoppedAt:        now,
			Source:           eruncommon.StopHistorySourceHostManual,
			Reason:           reason,
			CloudContextName: strings.TrimSpace(input.CloudContextName),
		}
		// Fold an armed idle breakdown into the manual stop so the row shows
		// both the manual action and what would have auto-fired. No pending
		// file just means a plain manual stop.
		if pending, ok, err := eruncommon.LoadEnvironmentStopPending(tenant, environment); err != nil {
			return nil, IdleStopRecordResult{}, err
		} else if ok {
			entry.GraceSeconds = pending.GraceSeconds
			entry.ArmedAt = pending.Since
			entry.Policy = pending.Policy
			if entry.CloudContextName == "" {
				entry.CloudContextName = pending.CloudContextName
			}
			for _, marker := range pending.Markers {
				entry.Markers = append(entry.Markers, stopHistoryMarkerFromPending(marker, pending.Since))
			}
		}
		if err := eruncommon.AppendStopHistoryEntry(tenant, environment, entry); err != nil {
			return nil, IdleStopRecordResult{}, err
		}
		if err := eruncommon.ClearEnvironmentStopPending(tenant, environment); err != nil {
			return nil, IdleStopRecordResult{}, err
		}
		return nil, IdleStopRecordResult{
			Tenant:      tenant,
			Environment: environment,
			StoppedAt:   now,
		}, nil
	}
}

func stopHistoryMarkerFromPending(marker eruncommon.EnvironmentIdleMarker, since time.Time) eruncommon.EnvironmentStopHistoryMarker {
	out := eruncommon.EnvironmentStopHistoryMarker{
		Name:   marker.Name,
		Idle:   marker.Idle,
		Reason: marker.Reason,
	}
	if !marker.LastActivity.IsZero() {
		delta := int64(since.Sub(marker.LastActivity).Seconds())
		if delta > 0 {
			out.SecondsIdleFor = delta
		}
	}
	return out
}
