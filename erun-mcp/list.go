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
	VersionDriftTenant string `json:"versionDriftTenant,omitempty" jsonschema:"when set, additionally report erun-version drift across this tenant's environments: which erun version each environment runs, and the newest version observed among them. When an environment's version cannot be read from config alone, this live-probes its own MCP edge as a fallback -- provenance-independent, so a tenant shipping its own runtime image still resolves a real version"`
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
	Preview       bool `json:"preview,omitempty" jsonschema:"only meaningful alongside controlPlanes -- trace which planes and registry lookup would be checked without making either network call"`
}

// ListToolResult is eruncommon.ListResult plus the optional version-drift
// report; embedded (not wrapped) so existing callers reading ListResult
// fields directly still find them at the top level.
type ListToolResult struct {
	eruncommon.ListResult
	VersionDrift             *eruncommon.TenantVersionDrift       `json:"versionDrift,omitempty"`
	ControlPlaneVersionDrift *eruncommon.ControlPlaneVersionDrift `json:"controlPlaneVersionDrift,omitempty"`
}

func listTool(info eruncommon.BuildInfo, runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ListInput) (*mcp.CallToolResult, ListToolResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ListInput) (*mcp.CallToolResult, ListToolResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.TraceCommand("", "erun", "list")

		tenant := strings.TrimSpace(input.VersionDriftTenant)
		gateEnvironment := strings.TrimSpace(input.GateEnvironment)
		if err := validateListInput(input.ControlPlanes, tenant, gateEnvironment); err != nil {
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

		return buildListToolResult(ctx, result, input.ControlPlanes, tenant, gateEnvironment, info.Version)
	}
}

func validateListInput(controlPlanes bool, tenant, gateEnvironment string) error {
	if gateEnvironment != "" && tenant == "" {
		return fmt.Errorf("gateEnvironment requires versionDriftTenant")
	}
	if controlPlanes && (tenant != "" || gateEnvironment != "") {
		return fmt.Errorf("controlPlanes cannot be combined with versionDriftTenant/gateEnvironment")
	}
	return nil
}

func buildListToolResult(ctx eruncommon.Context, result eruncommon.ListResult, controlPlanes bool, tenant, gateEnvironment, clientVersion string) (*mcp.CallToolResult, ListToolResult, error) {
	toolResult := ListToolResult{ListResult: result}
	if controlPlanes {
		drift := eruncommon.ResolveControlPlaneVersionDrift(ctx, result, cloudDependencies(), eruncommon.ResolveDefaultRuntimeRegistryVersions)
		toolResult.ControlPlaneVersionDrift = &drift
	}
	if tenant != "" {
		drift, err := eruncommon.ResolveTenantVersionDrift(ctx, result, tenant, gateEnvironment, eruncommon.DefaultEnvironmentVersionProbe(clientVersion))
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
