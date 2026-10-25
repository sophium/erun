package helmrun

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
)

// The executed helm command must match the traced one.
func TestHelmDeployParamsPreservesCommand(t *testing.T) {
	spec := helmdeploy.HelmDeploySpec{
		ReleaseName:          "frs-devops",
		ChartPath:            "oci://ghcr.io/sophium/charts/frs-devops",
		SubchartKey:          "erun-devops",
		Tenant:               "frs",
		Environment:          "prod",
		Namespace:            "frs-prod",
		KubernetesContext:    "erun",
		Version:              "1.0.29",
		Timeout:              "5m0s",
		ContainerRegistry:    "ghcr.io/sophium",
		ImageOverrides:       map[string]string{"erun-devops": "ghcr.io/sophium/frs-devops:1.0.29"},
		CloudflareEnabled:    true,
		CloudflareAccountID:  "5688accc2b055ae8babb811a5da8b8e4",
		CloudflareSecretName: "frs-devops-cloudflare",
		CloudflareTokenRef:   "cloudflare/frs-alias",
		MCPAuthEnabled:       true,
		MCPAuthSecretName:    "frs-devops-mcp-auth",
		MCPAuthIssuer:        "file:///etc/erun/mcp-auth/desktopid.pub",
		MCPAuthAudience:      "erun-mcp:frs/prod",
		RuntimeRegistry:      "ghcr.io/sophium",
		ContainerRegistries:  config.ContainerRegistries{{Registry: "ghcr.io/sophium", Roles: []model.RegistryRole{"deploy"}}},
		DisableBuildScript:   true,
		PlatformAccount:      true,
		Platform:             config.PlatformConfig{BaseDomain: "erunpaas.com", Env: "frs-prod", ServicesZone: "frs.erunpaas.com"},
	}

	traced := HelmDeployCommand(spec)
	executed := helmDeployCommandSpec(HelmDeployParamsFor(spec, io.Discard, io.Discard), spec.ChartPath)

	if !reflect.DeepEqual(traced.Args, executed.Args) {
		t.Fatalf("executed helm command diverges from the traced command (a field was dropped in the Params round-trip):\n  traced:   %v\n  executed: %v", traced.Args, executed.Args)
	}

	// DeepEqual alone passes if both paths drop the same field.
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
