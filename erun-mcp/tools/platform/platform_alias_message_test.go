package platform

import (
	"context"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

// MCP callers have no --erun-alias flag, so the remedy must name the `alias` input.
func mcpAliasMessageFor(t *testing.T, providers ...model.CloudProviderConfig) string {
	t.Helper()
	runtime := runtimecfg.RuntimeConfig{Store: listToolStore{toolConfig: config.ERunConfig{CloudProviders: providers}}}
	_, _, err := PlatformEnvListTool(runtime)(context.Background(), nil, PlatformEnvListInput{})
	if err == nil {
		t.Fatal("expected the unresolvable-alias error, got none")
	}
	return err.Error()
}

func erunTypeProvider(alias string) model.CloudProviderConfig {
	return providerconfig.NormalizeCloudProviderConfig(model.CloudProviderConfig{
		Alias:    alias,
		Provider: config.CloudProviderERun,
		ERun:     &model.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"},
	})
}

func TestMCPPlatformEnvListNamesTheAliasArgumentNotTheCLIFlag(t *testing.T) {
	message := mcpAliasMessageFor(t, erunTypeProvider("one@erun"), erunTypeProvider("two@erun"))

	if !strings.Contains(message, "pass alias to choose one") {
		t.Fatalf("ambiguous-alias remedy does not name the MCP `alias` argument: %q", message)
	}
	if strings.Contains(message, "--erun-alias") {
		t.Fatalf("ambiguous-alias remedy names the CLI flag an MCP caller cannot pass: %q", message)
	}
}

// With two erun-type aliases, the tool must not assert the zero-alias cause.
func TestMCPPlatformEnvListDoesNotAssertASingleCauseWhenNoneIsSelected(t *testing.T) {
	message := mcpAliasMessageFor(t)

	if strings.HasPrefix(message, "no erun platform cloud provider alias is configured") {
		t.Fatalf("unresolvable alias asserted as a definite single cause: %q", message)
	}
	if !strings.Contains(message, "either no erun-type alias is configured") ||
		!strings.Contains(message, "or several are and none was selected") {
		t.Fatalf("unresolvable alias does not name both undistinguished causes: %q", message)
	}
	if !strings.Contains(message, "pass alias to choose one") {
		t.Fatalf("unresolvable alias does not point at the MCP `alias` argument: %q", message)
	}
}
