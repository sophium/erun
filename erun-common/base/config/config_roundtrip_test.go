package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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

// TestMarshalConfigPreservingUnknownFieldsSurvivesUnknownTopLevelKey covers
// the simpler top-level case: a whole key this binary's ERunConfig type has
// never declared (e.g. a field added by a newer release) survives a save from
// this older type.
func TestMarshalConfigPreservingUnknownFieldsSurvivesUnknownTopLevelKey(t *testing.T) {
	existing := []byte("defaulttenant: frs\nfutureField:\n  nested: true\n  value: 42\n")

	out, err := MarshalConfigPreservingUnknownFields(existing, ERunConfig{DefaultTenant: "frs"})
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

// TestMarshalConfigPreservingUnknownFieldsDropsMigratedLegacyKey guards the
// one-way migration documented on migrateLegacyContainerRegistry (erun#1222):
// a legacy scalar key an UnmarshalYAML migrated into a current field must
// actually disappear on the next save, not survive as an "unknown" field the
// plain struct has no name for. Left unfixed, the desktop's own save (which
// clears EnvConfig.ContainerRegistries to route a local-agent env's marked
// list into the project config instead) writes a merged file that still
// carries the legacy key, so the very next load re-migrates it and clobbers
// whatever roles the operator just set.
func TestMarshalConfigPreservingUnknownFieldsDropsMigratedLegacyKey(t *testing.T) {
	existing := []byte("name: alpha\ncontainerregistry: registry.example/test\ntype: local-agent\n")

	var loaded EnvConfig
	if err := yaml.Unmarshal(existing, &loaded); err != nil {
		t.Fatalf("seed unmarshal: %v", err)
	}
	if loaded.ContainerRegistries.IsZero() {
		t.Fatalf("legacy scalar did not migrate: %+v", loaded)
	}

	// Mirrors what the desktop does for a local-agent env: the marked list
	// moves to the project config, so the env's own ContainerRegistries clears.
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

// TestMarshalConfigPreservingUnknownFieldsNoExistingFile covers the plain
// first-write path: with nothing to preserve, the output is just the ordinary
// marshal of config.
func TestMarshalConfigPreservingUnknownFieldsNoExistingFile(t *testing.T) {
	out, err := MarshalConfigPreservingUnknownFields(nil, ERunConfig{DefaultTenant: "frs"})
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
