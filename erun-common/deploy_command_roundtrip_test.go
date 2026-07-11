package eruncommon

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

// TestHelmDeployParamsPreservesCommand locks the invariant that the helm command
// erun executes (built from HelmDeployParams via helmDeployCommandSpec) is
// identical to the command it traces (HelmDeploySpec.command()). The two used to
// be built from separate hand-maintained field lists, so a field present in the
// spec but missing from the copy list was silently dropped from real deploys
// while the trace still showed it — that dropped Cloudflare, MCP-auth, the
// runtime registry, extra container registries, and platform config. A spec
// populated with exactly those fields must round-trip losslessly.
func TestHelmDeployParamsPreservesCommand(t *testing.T) {
	spec := HelmDeploySpec{
		ReleaseName:       "frs-devops",
		ChartPath:         "oci://ghcr.io/sophium/charts/frs-devops",
		SubchartKey:       "erun-devops",
		Tenant:            "frs",
		Environment:       "prod",
		Namespace:         "frs-prod",
		KubernetesContext: "erun",
		Version:           "1.0.29",
		Timeout:           "5m0s",
		ContainerRegistry: "ghcr.io/sophium",
		ImageOverrides:    map[string]string{"erun-devops": "ghcr.io/sophium/frs-devops:1.0.29"},
		// The fields the old copy-list dropped in transit — the crux of the bug.
		CloudflareEnabled:    true,
		CloudflareAccountID:  "5688accc2b055ae8babb811a5da8b8e4",
		CloudflareSecretName: "frs-devops-cloudflare",
		CloudflareTokenRef:   "cloudflare/frs-alias",
		MCPAuthEnabled:       true,
		MCPAuthSecretName:    "frs-devops-mcp-auth",
		MCPAuthIssuer:        "file:///etc/erun/mcp-auth/desktopid.pub",
		MCPAuthAudience:      "erun-mcp:frs/prod",
		RuntimeRegistry:      "ghcr.io/sophium",
		ContainerRegistries:  ContainerRegistries{{Registry: "ghcr.io/sophium", Roles: []RegistryRole{"deploy"}}},
		DisableBuildScript:   true,
		PlatformAccount:      true,
		Platform:             PlatformConfig{BaseDomain: "erunpaas.com", Env: "frs-prod", ServicesZone: "frs.erunpaas.com"},
	}

	traced := spec.command()
	executed := helmDeployCommandSpec(spec.Params(io.Discard, io.Discard), spec.ChartPath)

	if !reflect.DeepEqual(traced.Args, executed.Args) {
		t.Fatalf("executed helm command diverges from the traced command (a field was dropped in the Params round-trip):\n  traced:   %v\n  executed: %v", traced.Args, executed.Args)
	}

	// DeepEqual alone would pass if both paths dropped the same field, so assert
	// the previously-dropped values are actually present in the executed command.
	joined := strings.Join(executed.Args, " ")
	for _, want := range []string{
		"cloudContext.cloudflare.enabled=true",
		"cloudContext.cloudflare.secretName=frs-devops-cloudflare",
		"mcpAuth.enabled=true",
		"runtimeRegistry=ghcr.io/sophium",
		"containerRegistries=",
		"platformAccount=true",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("executed command missing %q; got %v", want, executed.Args)
		}
	}
}
