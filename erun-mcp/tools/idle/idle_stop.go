package idle

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/idlestop"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

// IdleStopCancelInput selects the env whose armed idle-stop to cancel.
type IdleStopCancelInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should have its pending stop cancelled; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to cancel; defaults to the server environment context, and must match it: this server only acts on its own environment"`
}

// IdleStopCancelResult reports the cancel; Cleared is true even when nothing was armed.
type IdleStopCancelResult struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Cleared     bool   `json:"cleared"`
}

func IdleStopCancelTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleStopCancelInput) (*mcp.CallToolResult, IdleStopCancelResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleStopCancelInput) (*mcp.CallToolResult, IdleStopCancelResult, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, IdleStopCancelResult{}, err
		}
		if err := idlestop.ClearEnvironmentStopPending(tenant, environment); err != nil {
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

// IdleStopHistoryResult wraps entries (newest first) so the schema stays an object.
type IdleStopHistoryResult struct {
	Tenant      string                              `json:"tenant"`
	Environment string                              `json:"environment"`
	Entries     []model.EnvironmentStopHistoryEntry `json:"entries"`
}

func IdleStopHistoryTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleStopHistoryInput) (*mcp.CallToolResult, IdleStopHistoryResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleStopHistoryInput) (*mcp.CallToolResult, IdleStopHistoryResult, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, IdleStopHistoryResult{}, err
		}
		entries, err := idlestop.LoadEnvironmentStopHistory(tenant, environment)
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

// IdleStopRecordInput records a manual stop in the pod, sharing the PVC history with auto-stops.
type IdleStopRecordInput struct {
	Tenant           string `json:"tenant,omitempty" jsonschema:"tenant whose environment should have a stop entry recorded; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment      string `json:"environment,omitempty" jsonschema:"environment to record against; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Reason           string `json:"reason,omitempty" jsonschema:"reason text rendered on the History row; empty falls back to a generic 'Manual stop'"`
	CloudContextName string `json:"cloudContextName,omitempty" jsonschema:"cloud context name the stop targeted; informational"`
}

// IdleStopRecordResult echoes the recorded StoppedAt.
type IdleStopRecordResult struct {
	Tenant      string    `json:"tenant"`
	Environment string    `json:"environment"`
	StoppedAt   time.Time `json:"stoppedAt"`
}

func IdleStopRecordTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleStopRecordInput) (*mcp.CallToolResult, IdleStopRecordResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleStopRecordInput) (*mcp.CallToolResult, IdleStopRecordResult, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, IdleStopRecordResult{}, err
		}
		reason := strings.TrimSpace(input.Reason)
		if reason == "" {
			reason = "Manual stop"
		}
		now := time.Now().UTC()
		entry := model.EnvironmentStopHistoryEntry{
			StoppedAt:        now,
			Source:           idlestop.StopHistorySourceHostManual,
			Reason:           reason,
			CloudContextName: strings.TrimSpace(input.CloudContextName),
		}
		// Fold any armed idle breakdown into the manual stop row.
		if pending, ok, err := idlestop.LoadEnvironmentStopPending(tenant, environment); err != nil {
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
		if err := idlestop.AppendStopHistoryEntry(tenant, environment, entry); err != nil {
			return nil, IdleStopRecordResult{}, err
		}
		if err := idlestop.ClearEnvironmentStopPending(tenant, environment); err != nil {
			return nil, IdleStopRecordResult{}, err
		}
		return nil, IdleStopRecordResult{
			Tenant:      tenant,
			Environment: environment,
			StoppedAt:   now,
		}, nil
	}
}

func stopHistoryMarkerFromPending(marker model.EnvironmentIdleMarker, since time.Time) model.EnvironmentStopHistoryMarker {
	out := model.EnvironmentStopHistoryMarker{
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
