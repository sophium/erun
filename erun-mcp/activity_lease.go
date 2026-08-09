package erunmcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// The lease tools are the MCP half of the activity lease. An orchestrator that
// detaches a long job in this pod has no request to bump for the job's duration,
// so without a lease the environment it is driving hard reads as untouched.

// ActivityLeaseTakeInput names the work the lease is being held for.
type ActivityLeaseTakeInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment the lease is held on; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to hold; defaults to the server environment context"`
	Name        string `json:"name" jsonschema:"what the lease is holding the environment for; shown to the operator as the reason it reads as busy"`
	ID          string `json:"id,omitempty" jsonschema:"lease id to take or renew; defaults to the name, so re-taking the same name renews rather than stacking"`
	PID         int    `json:"pid,omitempty" jsonschema:"process id of the detached job; the lease is reclaimed once that process exits, so an abandoned lease cannot pin the environment awake"`
	TTLSeconds  int64  `json:"ttlSeconds,omitempty" jsonschema:"seconds the lease holds without a renewal; defaults to 900"`
}

// ActivityLeaseResult reports the lease state after the call.
type ActivityLeaseResult struct {
	Tenant      string                                `json:"tenant"`
	Environment string                                `json:"environment"`
	Lease       *eruncommon.EnvironmentActivityLease  `json:"lease,omitempty"`
	Held        []eruncommon.EnvironmentActivityLease `json:"held"`
}

func activityLeaseTakeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ActivityLeaseTakeInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ActivityLeaseTakeInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
		tenant, environment, err := resolveActivityLeaseTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		lease, err := eruncommon.TakeEnvironmentActivityLease(eruncommon.TakeEnvironmentActivityLeaseParams{
			Tenant:      tenant,
			Environment: environment,
			Name:        input.Name,
			ID:          input.ID,
			PID:         input.PID,
			TTL:         time.Duration(input.TTLSeconds) * time.Second,
		})
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		return activityLeaseResult(tenant, environment, &lease)
	}
}

// ActivityLeaseReleaseInput selects the lease to drop.
type ActivityLeaseReleaseInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment holds the lease; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to release; defaults to the server environment context"`
	ID          string `json:"id" jsonschema:"lease id to release; the name passed to activity_lease_take when no explicit id was given"`
}

func activityLeaseReleaseTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ActivityLeaseReleaseInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ActivityLeaseReleaseInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
		tenant, environment, err := resolveActivityLeaseTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		if strings.TrimSpace(input.ID) == "" {
			return nil, ActivityLeaseResult{}, fmt.Errorf("lease id is required")
		}
		if err := eruncommon.ReleaseEnvironmentActivityLease(tenant, environment, input.ID); err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		return activityLeaseResult(tenant, environment, nil)
	}
}

func resolveActivityLeaseTarget(runtime RuntimeConfig, tenant, environment string) (string, string, error) {
	resolvedTenant := firstNonEmpty(tenant, runtime.Context.Tenant)
	resolvedEnvironment := firstNonEmpty(environment, runtime.Context.Environment)
	if strings.TrimSpace(resolvedTenant) == "" || strings.TrimSpace(resolvedEnvironment) == "" {
		return "", "", fmt.Errorf("tenant and environment are required")
	}
	return resolvedTenant, resolvedEnvironment, nil
}

// activityLeaseResult always returns what is still held, so a caller sees the
// environment's whole claim set rather than only the lease it just moved.
func activityLeaseResult(tenant, environment string, lease *eruncommon.EnvironmentActivityLease) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	held, err := eruncommon.LoadEnvironmentActivityLeases(tenant, environment, time.Now())
	if err != nil {
		return nil, ActivityLeaseResult{}, err
	}
	if held == nil {
		held = []eruncommon.EnvironmentActivityLease{}
	}
	return nil, ActivityLeaseResult{Tenant: tenant, Environment: environment, Lease: lease, Held: held}, nil
}

// ActivityLeaseListInput selects the environment to read.
type ActivityLeaseListInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment to read; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to read; defaults to the server environment context"`
}

func activityLeaseListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ActivityLeaseListInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ActivityLeaseListInput) (*mcp.CallToolResult, ActivityLeaseResult, error) {
		tenant, environment, err := resolveActivityLeaseTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, ActivityLeaseResult{}, err
		}
		return activityLeaseResult(tenant, environment, nil)
	}
}
