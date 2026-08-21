package eruncommon

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// staleCloudProviderConfig models a binary built before CloudProviderConfig
// grew its ERun field: same shape, minus the field this test's "old" binary
// cannot represent. Marshaling it is exactly what #1075's desktop build did.
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

// TestMarshalConfigPreservingUnknownFieldsSurvivesNestedUnknownBlock
// reproduces #1075: a component whose struct type has no ERun field must not
// delete config.yaml's "erun:" sub-block when it saves the same document.
func TestMarshalConfigPreservingUnknownFieldsSurvivesNestedUnknownBlock(t *testing.T) {
	existing, err := yaml.Marshal(ERunConfig{
		DefaultTenant: "frs",
		CloudProviders: []CloudProviderConfig{{
			Alias:         "erun+api.frs-prod.services.erunpaas.com@erun",
			Provider:      CloudProviderERun,
			Username:      "erun",
			AccountID:     "api.frs-prod.services.erunpaas.com",
			OIDCIssuerURL: "https://auth.example.test",
			ERun: &ERunProviderConfig{
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
			Provider:      CloudProviderERun,
			Username:      "erun",
			AccountID:     "api.frs-prod.services.erunpaas.com",
			OIDCIssuerURL: "https://auth.example.test",
		}},
	}

	out, err := marshalConfigPreservingUnknownFields(existing, stale)
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

// TestMarshalConfigPreservingUnknownFieldsStillClearsKnownFields guards
// against over-correcting into "never write a delete": a field the current
// binary's type does know about, now empty, must still disappear from the
// written document rather than being resurrected from the old file.
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
	out, err := marshalConfigPreservingUnknownFields(existing, cleared)
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

// TestMarshalConfigPreservingUnknownFieldsSurvivesUnknownTopLevelKey covers
// the simpler top-level case: a whole key this binary's ERunConfig type has
// never declared (e.g. a field added by a newer release) survives a save from
// this older type.
func TestMarshalConfigPreservingUnknownFieldsSurvivesUnknownTopLevelKey(t *testing.T) {
	existing := []byte("defaulttenant: frs\nfutureField:\n  nested: true\n  value: 42\n")

	out, err := marshalConfigPreservingUnknownFields(existing, ERunConfig{DefaultTenant: "frs"})
	if err != nil {
		t.Fatalf("marshalConfigPreservingUnknownFields: %v", err)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal merged output: %v", err)
	}
	future, ok := raw["futureField"].(map[string]any)
	if !ok {
		t.Fatalf("futureField missing or wrong shape in output:\n%s", out)
	}
	if future["nested"] != true || future["value"] != 42 {
		t.Fatalf("futureField corrupted: %+v", future)
	}
}

// TestMarshalConfigPreservingUnknownFieldsNoExistingFile covers the plain
// first-write path: with nothing to preserve, the output is just the ordinary
// marshal of config.
func TestMarshalConfigPreservingUnknownFieldsNoExistingFile(t *testing.T) {
	out, err := marshalConfigPreservingUnknownFields(nil, ERunConfig{DefaultTenant: "frs"})
	if err != nil {
		t.Fatalf("marshalConfigPreservingUnknownFields: %v", err)
	}
	want, err := yaml.Marshal(ERunConfig{DefaultTenant: "frs"})
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if string(out) != string(want) {
		t.Fatalf("out = %q, want %q", out, want)
	}
}
