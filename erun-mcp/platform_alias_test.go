package erunmcp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	eruncommon "github.com/sophium/erun/erun-common"
)

// redirectMCPConfigHomeForTest points the process at a temp erun config home.
// It stands in for the pod's own config root, which is the root the in-pod MCP
// edge reads and which is not the operator's.
func redirectMCPConfigHomeForTest(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	return filepath.Join(root, "erun", "config.yaml")
}

func platformAliasTestProvider(alias string) eruncommon.CloudProviderConfig {
	return eruncommon.NormalizeCloudProviderConfig(eruncommon.CloudProviderConfig{
		Alias:         alias,
		Provider:      eruncommon.CloudProviderERun,
		OIDCIssuerURL: "https://auth.example.test",
		ERun:          &eruncommon.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"},
	})
}

// TestPlatformEnvListReportsAliasStateAgainstTheConfigItRead pins the MCP half
// of this issue: `platform_env_list` with no alias reported "no erun platform
// cloud provider alias is configured", which reads as a claim about the
// operator's config while it is in fact a claim about the pod's -- the same
// call answered "multiple" on the host, and the "run erun cloud init" remedy it
// named would have added a further alias to the config that was already
// ambiguous. The MCP surface must classify the fixture the same way the CLI
// does and say which config it read.
func TestPlatformEnvListReportsAliasStateAgainstTheConfigItRead(t *testing.T) {
	configPath := redirectMCPConfigHomeForTest(t)
	runtime := RuntimeConfig{Store: eruncommon.ConfigStore{}}

	// The pod's fresh config: no erun-type alias at all.
	_, _, err := platformEnvListTool(runtime)(context.Background(), nil, PlatformEnvListInput{})
	if err == nil {
		t.Fatal("expected platform_env_list to refuse without a resolvable erun alias")
	}
	if !errors.Is(err, eruncommon.ErrPlatformAliasUnusable) {
		t.Fatalf("expected ErrPlatformAliasUnusable to survive the MCP surface, got: %v", err)
	}
	if !strings.Contains(err.Error(), configPath) {
		t.Fatalf("MCP message %q does not name the config it read (%s)", err.Error(), configPath)
	}

	// The same config the operator has several erun aliases in: MCP must report
	// ambiguity as ambiguity, naming the aliases and the alias argument.
	if saveErr := eruncommon.SaveERunConfig(eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{
		platformAliasTestProvider("erun+one@erun"),
		platformAliasTestProvider("erun+two@erun"),
	}}); saveErr != nil {
		t.Fatalf("SaveERunConfig: %v", saveErr)
	}
	_, _, err = platformEnvListTool(runtime)(context.Background(), nil, PlatformEnvListInput{})
	if err == nil {
		t.Fatal("expected platform_env_list to refuse an ambiguous alias config")
	}
	for _, want := range []string{
		"multiple erun platform cloud provider aliases are configured in " + configPath,
		"erun+one@erun",
		"erun+two@erun",
		"alias argument on an MCP platform tool",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("MCP message %q does not contain %q", err.Error(), want)
		}
	}
}
