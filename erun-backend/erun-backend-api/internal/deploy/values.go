package deploy

import (
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// runtimeValues builds the Helm values map the erun-devops runtime chart needs
// for a hosted env deploy — the in-process equivalent of the `helm --set` list
// the CLI projects (eruncommon (HelmDeploySpec).command()). The keys mirror the
// chart's .Values references in templates/service.yaml exactly (cloudContext
// uses lowercase `instanceId`); a mistyped key would be silently ignored.
//
// `tenant` and `environment` are required for the chart to render; the rest
// describe the managed-cloud runtime. Cloudflare / MCP-auth values are left
// unset so this is the plain no-secret deploy. A custom runtime image rides in
// as imageOverrides.erun-devops only when imageOverride is non-empty.
func runtimeValues(tenant, environment string, ctxRow model.Context, registry, imageOverride string) map[string]any {
	values := map[string]any{
		"tenant":            tenant,
		"environment":       environment,
		"worktreeStorage":   "none",
		"managedCloud":      true,
		"containerRegistry": registry,
		"runtimeRegistry":   registry,
		"cloudContext": map[string]any{
			"name":          ctxRow.Name,
			"provider":      ctxRow.Provider,
			"providerAlias": ctxRow.CloudProviderAlias,
			"region":        ctxRow.Region,
			"instanceId":    ctxRow.InstanceID,
		},
	}
	if imageOverride != "" {
		values["imageOverrides"] = map[string]any{"erun-devops": imageOverride}
	}
	return values
}
