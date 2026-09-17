package eruncommon

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
)

// scopedCloudStore is a read store that reads a config belonging to a root the
// process itself is not running under -- the pod's MCP edge reading the pod's
// config home while the operator's CLI reads theirs. It exists so a test can
// hold both roots at once, which ConfigStore (bound to the process-global xdg
// root) cannot.
type scopedCloudStore struct {
	config ERunConfig
	path   string
}

func (s scopedCloudStore) LoadERunConfig() (ERunConfig, string, error) {
	return s.config, s.path, nil
}

func aliasScopeTestERunProvider(alias string) CloudProviderConfig {
	return NormalizeCloudProviderConfig(CloudProviderConfig{
		Alias:         alias,
		Provider:      CloudProviderERun,
		OIDCIssuerURL: "https://auth.example.test",
		ERun:          &ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"},
	})
}

// TestResolveERunPlatformAliasClassifiesZeroOneAndTwoAliases is the fixture
// both transports are pinned against: given the same config, ResolveERunPlatformAlias
// -- which the CLI and the MCP platform tools both call -- resolves one alias,
// and reports zero and several as distinct, unusable outcomes.
func TestResolveERunPlatformAliasClassifiesZeroOneAndTwoAliases(t *testing.T) {
	redirectConfigHomeForTest(t)
	configPath := filepath.Join(xdg.ConfigHome, configRoot, configFile)

	cases := []struct {
		name       string
		providers  []CloudProviderConfig
		wantAlias  string
		wantSubstr string
	}{
		{
			name:       "no erun-type alias",
			providers:  []CloudProviderConfig{NormalizeCloudProviderConfig(CloudProviderConfig{Alias: "dev-aws", Provider: CloudProviderAWS, Profile: "dev-profile"})},
			wantSubstr: "no erun platform cloud provider alias is configured in " + configPath,
		},
		{
			name:      "exactly one erun-type alias resolves",
			providers: []CloudProviderConfig{aliasScopeTestERunProvider("erun+only@erun")},
			wantAlias: "erun+only@erun",
		},
		{
			name: "several erun-type aliases name both and the remedy",
			providers: []CloudProviderConfig{
				aliasScopeTestERunProvider("erun+one@erun"),
				aliasScopeTestERunProvider("erun+two@erun"),
			},
			wantSubstr: "multiple erun platform cloud provider aliases are configured in " + configPath +
				" (erun+one@erun, erun+two@erun); pass --erun-alias to choose one",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := SaveERunConfig(ERunConfig{CloudProviders: tc.providers}); err != nil {
				t.Fatalf("SaveERunConfig: %v", err)
			}

			provider, err := ResolveERunPlatformAlias(ConfigStore{}, "")
			if tc.wantAlias != "" {
				if err != nil {
					t.Fatalf("expected the sole erun alias to resolve, got: %v", err)
				}
				if provider.Alias != tc.wantAlias {
					t.Fatalf("resolved alias = %q, want %q", provider.Alias, tc.wantAlias)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected an unusable-alias error, resolved %q", provider.Alias)
			}
			if !errors.Is(err, ErrPlatformAliasUnusable) {
				t.Fatalf("expected ErrPlatformAliasUnusable, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("message %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestResolveERunPlatformAliasNamesTheConfigRootItRead pins the regression
// behind this issue: the pod's MCP edge and the operator's CLI share this
// function but read different config roots, so a zero-alias result in one root
// was reported as an unqualified "no alias is configured" -- a flat
// contradiction of the other root, which demonstrably holds several, and one
// whose named remedy (create one) deepens the other's ambiguity. Each message
// now names the config it read, so the two states are distinguishable rather
// than contradictory.
func TestResolveERunPlatformAliasNamesTheConfigRootItRead(t *testing.T) {
	const hostPath = "/home/operator/.config/erun/config.yaml"
	const podPath = "/home/erun/.config/erun/config.yaml"

	host := scopedCloudStore{
		config: ERunConfig{CloudProviders: []CloudProviderConfig{
			aliasScopeTestERunProvider("erun+one@erun"),
			aliasScopeTestERunProvider("erun+two@erun"),
		}},
		path: hostPath,
	}
	pod := scopedCloudStore{config: ERunConfig{}, path: podPath}

	_, hostErr := ResolveERunPlatformAlias(host, "")
	if hostErr == nil {
		t.Fatal("expected the host's two erun aliases to be ambiguous")
	}
	_, podErr := ResolveERunPlatformAlias(pod, "")
	if podErr == nil {
		t.Fatal("expected the pod's empty config to be unusable")
	}

	if !strings.Contains(hostErr.Error(), hostPath) {
		t.Fatalf("host message %q does not name the config it read (%s)", hostErr.Error(), hostPath)
	}
	if !strings.Contains(podErr.Error(), podPath) {
		t.Fatalf("pod message %q does not name the config it read (%s)", podErr.Error(), podPath)
	}
	// The defect was a pod answer that read as a claim about the operator's own
	// config. An unqualified "no ... is configured" is exactly that claim, so
	// the message must name its scope immediately after it.
	if strings.Contains(podErr.Error(), "is configured;") {
		t.Fatalf("pod message still asserts an unqualified absence: %q", podErr.Error())
	}
	if hostErr.Error() == podErr.Error() {
		t.Fatal("the two roots produced the same message, so a reader cannot tell them apart")
	}
}
