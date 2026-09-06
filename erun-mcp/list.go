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
	// ControlPlanes, when set, additionally reports every configured
	// erun-hosted control plane's deployed version (GET /v1/platform)
	// against the newest version erun's own registry has actually
	// published -- deployed-vs-published, not deployed-vs-main -- and each
	// plane's linked console the same way (GET /version.json, discovered
	// from the plane's own reported consoleUrl), nested under it. Requires
	// network access to each plane and console, and to erun's registry;
	// Preview traces what would be checked instead of making either call.
	ControlPlanes bool `json:"controlPlanes,omitempty" jsonschema:"when set, additionally report every configured erun-hosted control plane's deployed version, and its linked console's deployed version, against the newest version erun's own registry has published -- deployed-vs-published, not deployed-vs-main"`
	// Alias, only meaningful alongside ControlPlanes, narrows the control
	// plane check to one configured erun-hosted alias instead of every
	// configured one (erun#2130) -- the same --erun-alias every other
	// platform-touching command already accepts.
	Alias   string `json:"erunAlias,omitempty" jsonschema:"only meaningful alongside controlPlanes -- narrow the check to this one configured erun-hosted alias instead of every configured one"`
	Preview bool   `json:"preview,omitempty" jsonschema:"only meaningful alongside controlPlanes -- trace which planes and registry lookup would be checked without making either network call"`
}

// ListToolResult is eruncommon.ListResult plus the optional version-drift
// report; embedded (not wrapped) so existing callers reading ListResult
// fields directly still find them at the top level.
type ListToolResult struct {
	eruncommon.ListResult
	VersionDrift             *eruncommon.TenantVersionDrift       `json:"versionDrift,omitempty"`
	ControlPlaneVersionDrift *eruncommon.ControlPlaneVersionDrift `json:"controlPlaneVersionDrift,omitempty"`
}

func listTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ListInput) (*mcp.CallToolResult, ListToolResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ListInput) (*mcp.CallToolResult, ListToolResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.TraceCommand("", "erun", "list")

		tenant := strings.TrimSpace(input.VersionDriftTenant)
		gateEnvironment := strings.TrimSpace(input.GateEnvironment)
		alias := strings.TrimSpace(input.Alias)
		if err := validateListInput(input.ControlPlanes, tenant, gateEnvironment, alias); err != nil {
			return nil, ListToolResult{}, err
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

		return buildListToolResult(ctx, result, input.ControlPlanes, tenant, gateEnvironment, alias)
	}
}

func validateListInput(controlPlanes bool, tenant, gateEnvironment, alias string) error {
	if gateEnvironment != "" && tenant == "" {
		return fmt.Errorf("gateEnvironment requires versionDriftTenant")
	}
	if controlPlanes && (tenant != "" || gateEnvironment != "") {
		return fmt.Errorf("controlPlanes cannot be combined with versionDriftTenant/gateEnvironment")
	}
	if alias != "" && !controlPlanes {
		return fmt.Errorf("erunAlias requires controlPlanes")
	}
	return nil
}

func buildListToolResult(ctx eruncommon.Context, result eruncommon.ListResult, controlPlanes bool, tenant, gateEnvironment, alias string) (*mcp.CallToolResult, ListToolResult, error) {
	toolResult := ListToolResult{ListResult: result}
	if controlPlanes {
		drift, err := eruncommon.ResolveControlPlaneVersionDrift(ctx, result, alias, cloudDependencies(), eruncommon.ResolveDefaultRuntimeRegistryVersions)
		if err != nil {
			return nil, ListToolResult{}, err
		}
		toolResult.ControlPlaneVersionDrift = &drift
	}
	if tenant != "" {
		drift, err := eruncommon.ResolveTenantVersionDrift(result, tenant, gateEnvironment)
		if err != nil {
			return nil, ListToolResult{}, err
		}
		toolResult.VersionDrift = &drift
	}
	return nil, toolResult, nil
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
