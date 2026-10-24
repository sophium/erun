package config

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"gopkg.in/yaml.v3"
)

func TestMarshalConfigPreservingUnknownFieldsStillClearsKnownFields(t *testing.T) {
	existing, err := yaml.Marshal(EnvConfig{
		Name:               "prod",
		Type:               EnvironmentTypeRuntime,
		CloudProviderAlias: "old-alias@aws",
	})
	if err != nil {
		t.Fatalf("seed marshal: %v", err)
	}

	cleared := EnvConfig{Name: "prod", Type: EnvironmentTypeRuntime}
	out, err := MarshalConfigPreservingUnknownFields(existing, cleared)
	if err != nil {
		t.Fatalf("marshalConfigPreservingUnknownFields: %v", err)
	}

	var roundTripped EnvConfig
	if err := yaml.Unmarshal(out, &roundTripped); err != nil {
		t.Fatalf("unmarshal merged output: %v", err)
	}
	if roundTripped.CloudProviderAlias != "" {
		t.Fatalf("cloudprovideralias = %q, want cleared", roundTripped.CloudProviderAlias)
	}
	if strings.Contains(string(out), "cloudprovideralias") {
		t.Fatalf("output still names the cleared key:\n%s", out)
	}
}

// TestMarshalConfigPreservingUnknownFieldsDropsMigratedLegacyKey: a surviving legacy
// key would re-migrate on every load.
func TestMarshalConfigPreservingUnknownFieldsDropsMigratedLegacyKey(t *testing.T) {
	existing := []byte("name: alpha\ncontainerregistry: registry.example/test\ntype: local-agent\n")

	var loaded EnvConfig
	if err := yaml.Unmarshal(existing, &loaded); err != nil {
		t.Fatalf("seed unmarshal: %v", err)
	}
	if loaded.ContainerRegistries.IsZero() {
		t.Fatalf("legacy scalar did not migrate: %+v", loaded)
	}

	cleared := loaded
	cleared.ContainerRegistries = nil

	out, err := MarshalConfigPreservingUnknownFields(existing, cleared)
	if err != nil {
		t.Fatalf("marshalConfigPreservingUnknownFields: %v", err)
	}
	if strings.Contains(string(out), "containerregistry:") {
		t.Fatalf("output still carries the migrated legacy key:\n%s", out)
	}

	var roundTripped EnvConfig
	if err := yaml.Unmarshal(out, &roundTripped); err != nil {
		t.Fatalf("unmarshal merged output: %v", err)
	}
	if !roundTripped.ContainerRegistries.IsZero() {
		t.Fatalf("legacy key re-migrated after save: %+v", roundTripped.ContainerRegistries)
	}
}

// staleCloudProviderConfig is CloudProviderConfig before it grew its ERun field.
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

func TestMarshalConfigPreservingUnknownFieldsSurvivesNestedUnknownBlock(t *testing.T) {
	existing, err := yaml.Marshal(ERunConfig{
		DefaultTenant: "frs",
		CloudProviders: []model.CloudProviderConfig{{
			Alias:         "erun+api.frs-prod.services.erunpaas.com@erun",
			Provider:      CloudProviderERun,
			Username:      "erun",
			AccountID:     "api.frs-prod.services.erunpaas.com",
			OIDCIssuerURL: "https://auth.example.test",
			ERun: &model.ERunProviderConfig{
				APIURL:          "https://api.frs-prod.services.erunpaas.com",
				ClientID:        "386994662461735100",
				RefreshTokenRef: "keychain:erun/frs-prod",
			},
		}},
	})
	if err != nil {
		t.Fatalf("seed marshal: %v", err)
	}

	stale := staleERunConfig{
		DefaultTenant: "frs",
		CloudProviders: []staleCloudProviderConfig{{
			Alias:         "erun+api.frs-prod.services.erunpaas.com@erun",
			Provider:      CloudProviderERun,
			Username:      "erun",
			AccountID:     "api.frs-prod.services.erunpaas.com",
			OIDCIssuerURL: "https://auth.example.test",
		}},
	}

	out, err := MarshalConfigPreservingUnknownFields(existing, stale)
	if err != nil {
		t.Fatalf("marshalConfigPreservingUnknownFields: %v", err)
	}

	var roundTripped ERunConfig
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
