package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ListInput struct {
	Verbosity int `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	// VersionDriftTenant, when set, additionally reports erun-version drift
	// across this tenant's environments -- which environments run which
	// erun version, and the newest version observed among them.
	VersionDriftTenant string `json:"versionDriftTenant,omitempty" jsonschema:"when set, additionally report erun-version drift across this tenant's environments: which erun version each environment runs, and the newest version observed among them"`
	// GateEnvironment, only meaningful alongside VersionDriftTenant, names
	// the environment driving that tenant's merge-queue gate. erun has no
	// stored concept of which environment gates a tenant's merges (see root
	// AGENTS.md's release-cadence policy), so the caller states it.
	GateEnvironment string `json:"gateEnvironment,omitempty" jsonschema:"requires versionDriftTenant; the environment driving that tenant's merge-queue gate -- flags whether it runs an older erun version than any environment it gates, since a stale gate can pass a change that would fail on current code"`
}

// ListToolResult is eruncommon.ListResult plus the optional version-drift
// report; embedded (not wrapped) so existing callers reading ListResult
// fields directly still find them at the top level.
type ListToolResult struct {
	eruncommon.ListResult
	VersionDrift *eruncommon.TenantVersionDrift `json:"versionDrift,omitempty"`
}

func listTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ListInput) (*mcp.CallToolResult, ListToolResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ListInput) (*mcp.CallToolResult, ListToolResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(false, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.TraceCommand("", "erun", "list")

		tenant := strings.TrimSpace(input.VersionDriftTenant)
		gateEnvironment := strings.TrimSpace(input.GateEnvironment)
		if gateEnvironment != "" && tenant == "" {
			return nil, ListToolResult{}, fmt.Errorf("gateEnvironment requires versionDriftTenant")
		}

		workDir, err := runtimeRepoPath(runtime.Context)
		if err != nil {
			return nil, ListToolResult{}, err
		}

		result, err := eruncommon.ResolveListResult(runtime.Store, func() (string, string, error) {
			return runtimeFindProjectRoot(runtime.Context, workDir)
		}, runtimeOpenParams(runtime.Context))
		if err != nil {
			return nil, ListToolResult{}, err
		}

		toolResult := ListToolResult{ListResult: result}
		if tenant != "" {
			drift, err := eruncommon.ResolveTenantVersionDrift(result, tenant, gateEnvironment)
			if err != nil {
				return nil, ListToolResult{}, err
			}
			toolResult.VersionDrift = &drift
		}

		return nil, toolResult, nil
	}
}

func runtimeOpenParams(runtime RuntimeContext) eruncommon.OpenParams {
	tenant := strings.TrimSpace(runtime.Tenant)
	environment := strings.TrimSpace(runtime.Environment)

	switch {
	case tenant != "" && environment != "":
		return eruncommon.OpenParams{Tenant: tenant, Environment: environment}
	case tenant != "":
		return eruncommon.OpenParams{Tenant: tenant, UseDefaultEnvironment: true}
	case environment != "":
		return eruncommon.OpenParams{Environment: environment, UseDefaultTenant: true}
	default:
		return eruncommon.OpenParams{UseDefaultTenant: true, UseDefaultEnvironment: true}
	}
}
