package list

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared"
	sharedpkg "github.com/sophium/erun/erun-mcp/tools/shared"
)

type ListInput struct {
	Verbosity int `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	// VersionDriftTenant reports erun-version drift across the tenant's
	// environments; one with no local version is read live from helm.
	VersionDriftTenant string `json:"versionDriftTenant,omitempty" jsonschema:"when set, additionally report erun-version drift across this tenant's environments: which erun version each environment runs, and the newest version observed among them -- an environment with no version recorded locally is read live to tell a confirmed absence ('version' omitted, versionUnresolved false) apart from a version that could not be determined at all (versionUnresolved true, versionUnresolvedReason set, excluded from maxVersion/behindMax)"`
	// GateEnvironment is caller-stated: erun stores no merge-gate environment.
	GateEnvironment string `json:"gateEnvironment,omitempty" jsonschema:"requires versionDriftTenant; the environment driving that tenant's merge-queue gate -- flags whether it runs an older erun version than any environment it gates, since a stale gate can pass a change that would fail on current code"`
	// ControlPlanes reports each control plane's and linked surface's deployed
	// version against the newest published one; a surface without a URL is absent,
	// never up to date.
	ControlPlanes bool   `json:"controlPlanes,omitempty" jsonschema:"when set, additionally report every configured erun-hosted control plane's deployed version, and the deployed version of each surface it links to (its console and its docs site), against the newest version erun's own registry has published -- deployed-vs-published, not deployed-vs-main"`
	Alias         string `json:"erunAlias,omitempty" jsonschema:"only meaningful alongside controlPlanes -- narrow the check to this one configured erun-hosted alias instead of every configured one"`
	Preview       bool   `json:"preview,omitempty" jsonschema:"trace every live check this call would make (controlPlanes' plane and linked-surface probes and its registry lookup, versionDriftTenant's per-environment helm read for an environment with no version recorded locally) without making any of them"`
}

// ListToolResult embeds ListResult so its fields stay top-level.
type ListToolResult struct {
	lifecycle.ListResult
	VersionDrift             *lifecycle.TenantVersionDrift       `json:"versionDrift,omitempty"`
	ControlPlaneVersionDrift *lifecycle.ControlPlaneVersionDrift `json:"controlPlaneVersionDrift,omitempty"`
}

func ListTool(runtime sharedpkg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ListInput) (*mcp.CallToolResult, ListToolResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ListInput) (*mcp.CallToolResult, ListToolResult, error) {
		traceOutput := strings.Builder{}
		ctx := sharedpkg.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.TraceCommand("", "erun", "list")

		tenant := strings.TrimSpace(input.VersionDriftTenant)
		gateEnvironment := strings.TrimSpace(input.GateEnvironment)
		alias := strings.TrimSpace(input.Alias)
		if err := validateListInput(input.ControlPlanes, tenant, gateEnvironment, alias); err != nil {
			return nil, ListToolResult{}, err
		}

		workDir, err := sharedpkg.RuntimeRepoPath(runtime.Context)
		if err != nil {
			return nil, ListToolResult{}, err
		}

		result, err := lifecycle.ResolveListResult(runtime.Store, func() (string, string, error) {
			return sharedpkg.RuntimeFindProjectRoot(runtime.Context, workDir)
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

func buildListToolResult(ctx core.Context, result lifecycle.ListResult, controlPlanes bool, tenant, gateEnvironment, alias string) (*mcp.CallToolResult, ListToolResult, error) {
	toolResult := ListToolResult{ListResult: result}
	if controlPlanes {
		drift, err := lifecycle.ResolveControlPlaneVersionDrift(ctx, result, alias, sharedpkg.CloudDependencies(), registry.ResolveDefaultRuntimeRegistryVersions)
		if err != nil {
			return nil, ListToolResult{}, err
		}
		toolResult.ControlPlaneVersionDrift = &drift
	}
	if tenant != "" {
		drift, err := lifecycle.ResolveTenantVersionDrift(ctx, result, tenant, gateEnvironment)
		if err != nil {
			return nil, ListToolResult{}, err
		}
		toolResult.VersionDrift = &drift
	}
	return nil, toolResult, nil
}

func runtimeOpenParams(runtime sharedpkg.RuntimeContext) shared.OpenParams {
	tenant := strings.TrimSpace(runtime.Tenant)
	environment := strings.TrimSpace(runtime.Environment)

	switch {
	case tenant != "" && environment != "":
		return shared.OpenParams{Tenant: tenant, Environment: environment}
	case tenant != "":
		return shared.OpenParams{Tenant: tenant, UseDefaultEnvironment: true}
	case environment != "":
		return shared.OpenParams{Environment: environment, UseDefaultTenant: true}
	default:
		return shared.OpenParams{UseDefaultTenant: true, UseDefaultEnvironment: true}
	}
}
