package eruncommon

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"gopkg.in/yaml.v3"
)

// staleCloudProviderConfig models a binary built before CloudProviderConfig
// grew its ERun field: same shape, minus the field this test's "old" binary
// cannot represent. Marshaling it is exactly what an old desktop build did.
type staleCloudProviderConfig struct {
	Alias         string `yaml:"alias"`
	Provider      string `yaml:"provider"`
	Username      string `yaml:"username,omitempty"`
	AccountID     string `yaml:"accountid,omitempty"`
	OIDCIssuerURL string `yaml:"oidcissuerurl,omitempty"`
}

type staleERunConfig struct {
	DefaultTenant  string
	CloudProviders []staleCloudProviderConfig `yaml:"cloudproviders,omitempty"`
}

// TestMarshalConfigPreservingUnknownFieldsSurvivesNestedUnknownBlock checks
// that a component whose struct type has no ERun field does not
// delete config.yaml's "erun:" sub-block when it saves the same document.
func TestMarshalConfigPreservingUnknownFieldsSurvivesNestedUnknownBlock(t *testing.T) {
	existing, err := yaml.Marshal(config.ERunConfig{
		DefaultTenant: "frs",
		CloudProviders: []config.CloudProviderConfig{{
			Alias:         "erun+api.frs-prod.services.erunpaas.com@erun",
			Provider:      config.CloudProviderERun,
			Username:      "erun",
			AccountID:     "api.frs-prod.services.erunpaas.com",
			OIDCIssuerURL: "https://auth.example.test",
			ERun: &config.ERunProviderConfig{
				APIURL:          "https://api.frs-prod.services.erunpaas.com",
				ClientID:        "386994662461735100",
				RefreshTokenRef: "keychain:erun/frs-prod",
			},
		}},
	})
	if err != nil {
		t.Fatalf("seed marshal: %v", err)
	}

	// The stale binary re-saves the same alias, adding nothing, using a type
	// that has never heard of CloudProviderConfig.ERun.
	stale := staleERunConfig{
		DefaultTenant: "frs",
		CloudProviders: []staleCloudProviderConfig{{
			Alias:         "erun+api.frs-prod.services.erunpaas.com@erun",
			Provider:      config.CloudProviderERun,
			Username:      "erun",
			AccountID:     "api.frs-prod.services.erunpaas.com",
			OIDCIssuerURL: "https://auth.example.test",
		}},
	}

	out, err := config.MarshalConfigPreservingUnknownFields(existing, stale)
	if err != nil {
		t.Fatalf("marshalConfigPreservingUnknownFields: %v", err)
	}

	var roundTripped config.ERunConfig
	if err := yaml.Unmarshal(out, &roundTripped); err != nil {
		t.Fatalf("unmarshal merged output: %v", err)
	}
	if len(roundTripped.CloudProviders) != 1 {
		t.Fatalf("CloudProviders = %d entries, want 1", len(roundTripped.CloudProviders))
	}
	provider := roundTripped.CloudProviders[0]
	if provider.ERun == nil {
		t.Fatalf("erun: block was dropped by the stale-binary write; got %+v\n\noutput:\n%s", provider, out)
	}
	if provider.ERun.APIURL != "https://api.frs-prod.services.erunpaas.com" ||
		provider.ERun.ClientID != "386994662461735100" ||
		provider.ERun.RefreshTokenRef != "keychain:erun/frs-prod" {
		t.Fatalf("erun: block corrupted, got %+v", provider.ERun)
	}
}
