package erunmcp

import (
	"context"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The MCP platform tools resolve their alias through the same shared resolver
// the CLI uses, but the operator reaching them has no --erun-alias flag: the
// tool's own `alias` input is the only way to select one. A remedy that spells
// the CLI's flag is not something an MCP caller can act on.
func mcpAliasMessageFor(t *testing.T, providers ...eruncommon.CloudProviderConfig) string {
	t.Helper()
	runtime := RuntimeConfig{Store: listToolStore{toolConfig: eruncommon.ERunConfig{CloudProviders: providers}}}
	_, _, err := platformEnvListTool(runtime)(context.Background(), nil, PlatformEnvListInput{})
	if err == nil {
		t.Fatal("expected the unresolvable-alias error, got none")
	}
	return err.Error()
}

// erunTypeProvider is an alias the platform resolver counts as erun-type; its
// token status is irrelevant here because resolution fails before any token is
// minted.
func erunTypeProvider(alias string) eruncommon.CloudProviderConfig {
	return eruncommon.NormalizeCloudProviderConfig(eruncommon.CloudProviderConfig{
		Alias:    alias,
		Provider: eruncommon.CloudProviderERun,
		ERun:     &eruncommon.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"},
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

// The reported failure: asked for its environments on a host whose config held
// two erun-type aliases, the MCP tool answered with the zero-alias cause and
// the zero-alias remedy. Following that remedy adds a third alias to a config
// that already had too many. The cause it cannot actually distinguish must not
// be asserted as the one that occurred.
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

// The CLI keeps the message it has always emitted, so the integration goldens
// for the genuinely-zero-alias scenarios and the documented contract are
// untouched. This pins that boundary rather than inferring it.
func TestCLIKeepsItsAliasRemedyWording(t *testing.T) {
	for _, tc := range []struct {
		name      string
		providers []eruncommon.CloudProviderConfig
		want      string
	}{
		{
			name: "none configured",
			want: "no erun platform cloud provider alias is configured; run `erun cloud init erun --api-url <url>` first",
		},
		{
			name:      "several configured",
			providers: []eruncommon.CloudProviderConfig{erunTypeProvider("one@erun"), erunTypeProvider("two@erun")},
			want:      "multiple erun platform cloud provider aliases are configured; pass --erun-alias to choose one",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &listToolStore{toolConfig: eruncommon.ERunConfig{CloudProviders: tc.providers}}
			_, err := eruncommon.ResolveERunPlatformAlias(store, "")
			if err == nil {
				t.Fatal("expected the unresolvable-alias error, got none")
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("CLI alias remedy changed:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}
