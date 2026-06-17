package erunmcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// IdleStopCancelInput targets the env whose stop-pending.json should
// be cleared. Defaults to the runtime's tenant/environment context
// when the caller does not specify, matching every other tool's
// behavior.
type IdleStopCancelInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should have its pending stop cancelled; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to cancel; defaults to the server environment context"`
}

// IdleStopCancelResult is intentionally tiny — the desktop fires
// this through MCP and then re-fetches idle status to pick up the
// cleared pending state. Cleared is true on both the "was armed,
// now cleared" and "wasn't armed, no-op" branches; the in-pod
// monitor's next stop-ready tick decides whether to re-arm.
type IdleStopCancelResult struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Cleared     bool   `json:"cleared"`
}

func idleStopCancelTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleStopCancelInput) (*mcp.CallToolResult, IdleStopCancelResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleStopCancelInput) (*mcp.CallToolResult, IdleStopCancelResult, error) {
		tenant := firstNonEmpty(input.Tenant, runtime.Context.Tenant)
		environment := firstNonEmpty(input.Environment, runtime.Context.Environment)
		if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
			return nil, IdleStopCancelResult{}, fmt.Errorf("tenant and environment are required")
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

// IdleStopHistoryInput requests the last N auto-stop entries for
// an env. Defaults to the runtime's tenant/environment context.
type IdleStopHistoryInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment should be queried; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to query; defaults to the server environment context"`
}

// IdleStopHistoryResult wraps the array so the JSON-Schema surface
// describes a stable object shape. Entries are newest-first, capped
// at common.StopHistoryCap.
type IdleStopHistoryResult struct {
	Tenant      string                                   `json:"tenant"`
	Environment string                                   `json:"environment"`
	Entries     []eruncommon.EnvironmentStopHistoryEntry `json:"entries"`
}

func idleStopHistoryTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleStopHistoryInput) (*mcp.CallToolResult, IdleStopHistoryResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleStopHistoryInput) (*mcp.CallToolResult, IdleStopHistoryResult, error) {
		tenant := firstNonEmpty(input.Tenant, runtime.Context.Tenant)
		environment := firstNonEmpty(input.Environment, runtime.Context.Environment)
		if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
			return nil, IdleStopHistoryResult{}, fmt.Errorf("tenant and environment are required")
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

// IdleStopRecordInput captures the inputs for a host-driven stop
// audit record. The desktop calls this from StopCloudContext after
// the AWS stop succeeds; the tool runs in the pod so the on-disk
// write lands on the shared home PVC alongside the in-pod monitor's
// auto-stop records. Reason is free-form (the desktop sends "Manual
// stop via desktop"); CloudContextName helps disambiguate when one
// env spans multiple cloud-context links over its lifetime.
type IdleStopRecordInput struct {
	Tenant           string `json:"tenant,omitempty" jsonschema:"tenant whose environment should have a stop entry recorded; defaults to the server tenant context"`
	Environment      string `json:"environment,omitempty" jsonschema:"environment to record against; defaults to the server environment context"`
	Reason           string `json:"reason,omitempty" jsonschema:"reason text rendered on the History row; empty falls back to a generic 'Manual stop'"`
	CloudContextName string `json:"cloudContextName,omitempty" jsonschema:"cloud context name the stop targeted; informational"`
}

// IdleStopRecordResult echoes back the resolved tenant/environment
// and the timestamp the new row carries, so the desktop can match
// its eventual LoadStopHistory result against the row it just
// recorded.
type IdleStopRecordResult struct {
	Tenant      string    `json:"tenant"`
	Environment string    `json:"environment"`
	StoppedAt   time.Time `json:"stoppedAt"`
}

func idleStopRecordTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, IdleStopRecordInput) (*mcp.CallToolResult, IdleStopRecordResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input IdleStopRecordInput) (*mcp.CallToolResult, IdleStopRecordResult, error) {
		tenant := firstNonEmpty(input.Tenant, runtime.Context.Tenant)
		environment := firstNonEmpty(input.Environment, runtime.Context.Environment)
		if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
			return nil, IdleStopRecordResult{}, fmt.Errorf("tenant and environment are required")
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
		// Preserve the per-marker breakdown when the user clicks
		// Stop while an idle grace window is already armed — the
		// row then shows both "manual" and what would have fired
		// it on its own. Missing pending file is fine; this is a
		// manual stop without prior grace.
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
