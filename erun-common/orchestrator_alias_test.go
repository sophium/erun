package eruncommon

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestOrchestratorConfigAliasRoundTrip locks the config-file contract an
// orchestrator's alias depends on: a declared alias survives load/save, and an
// undeclared one neither writes an `alias:` key nor round-trips into a value.
// Mirrors OrchestratorEnvConfig.Role's own round trip, because the two fields
// fail the same way -- with an `omitempty` tag that never actually omits, or a
// key the reader silently drops.
func TestOrchestratorConfigAliasRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		alias string
	}{
		{name: "declared", alias: "erun+erunpaas.com@erun"},
		{name: "unset", alias: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ERunConfig{
				Orchestrators: []OrchestratorConfig{{
					ID:    "orch-1",
					Name:  "Orchestrator One",
					Alias: tt.alias,
				}},
			}

			data, err := yaml.Marshal(config)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}

			hasAliasKey := strings.Contains(string(data), "alias:")
			if tt.alias == "" && hasAliasKey {
				t.Fatalf("unset alias must not be written, got:\n%s", data)
			}
			if tt.alias != "" && !hasAliasKey {
				t.Fatalf("declared alias %q must be written, got:\n%s", tt.alias, data)
			}

			var roundTripped ERunConfig
			if err := yaml.Unmarshal(data, &roundTripped); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if len(roundTripped.Orchestrators) != 1 {
				t.Fatalf("round trip lost structure: %+v", roundTripped)
			}
			if got := roundTripped.Orchestrators[0].Alias; got != tt.alias {
				t.Fatalf("round-tripped alias = %q, want %q", got, tt.alias)
			}
		})
	}
}

func TestParseOrchestratorAliasFlag(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "alias", value: "erun+erunpaas.com@erun", want: "erun+erunpaas.com@erun"},
		{name: "padded", value: "  erun+erunpaas.com@erun  ", want: "erun+erunpaas.com@erun"},
		{name: "none sentinel", value: "none", want: ""},
		{name: "none sentinel is case-insensitive", value: "NONE", want: ""},
		{name: "empty", value: "", wantErr: true},
		{name: "whitespace only", value: "   ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseOrchestratorAliasFlag(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseOrchestratorAliasFlag(%q) = %q, want an error", tt.value, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOrchestratorAliasFlag(%q) error = %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("ParseOrchestratorAliasFlag(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestSetOrchestratorAliasWritesAndClears(t *testing.T) {
	store := newOrchestratorAliasStubStore()

	orchestrator, err := SetOrchestratorAlias(Context{}, store, SetOrchestratorAliasParams{
		OrchestratorID: "orch-1",
		Alias:          "erun+erunpaas.com@erun",
	})
	if err != nil {
		t.Fatalf("SetOrchestratorAlias() error = %v", err)
	}
	if orchestrator.Alias != "erun+erunpaas.com@erun" {
		t.Fatalf("returned alias = %q", orchestrator.Alias)
	}
	if got := store.config.Orchestrators[0].Alias; got != "erun+erunpaas.com@erun" {
		t.Fatalf("persisted alias = %q", got)
	}

	// Other fields must survive the write: the alias is one field of the
	// orchestrator, not a replacement for it.
	if len(store.config.Orchestrators[0].Environments) != 1 || store.config.Orchestrators[0].Name != "Orchestrator One" {
		t.Fatalf("write dropped the rest of the orchestrator: %+v", store.config.Orchestrators[0])
	}

	if _, err := SetOrchestratorAlias(Context{}, store, SetOrchestratorAliasParams{
		OrchestratorID: "orch-1",
		Alias:          "",
	}); err != nil {
		t.Fatalf("clearing error = %v", err)
	}
	if got := store.config.Orchestrators[0].Alias; got != "" {
		t.Fatalf("cleared alias = %q, want empty", got)
	}
}

// TestSetOrchestratorAliasRefusesAnAliasThisHostCannotResolve is the whole
// point of validating in the writer: an alias recorded but unresolvable is
// indistinguishable from one never set, so the command must refuse it and say
// what to do instead of writing a declaration nothing honours.
func TestSetOrchestratorAliasRefusesAnAliasThisHostCannotResolve(t *testing.T) {
	store := newOrchestratorAliasStubStore()
	store.saved = false

	_, err := SetOrchestratorAlias(Context{}, store, SetOrchestratorAliasParams{
		OrchestratorID: "orch-1",
		Alias:          "erun+elsewhere.example@erun",
	})
	if err == nil {
		t.Fatal("SetOrchestratorAlias() accepted an alias this host does not have configured")
	}
	for _, want := range []string{"not configured", "erun cloud init erun --api-url", OrchestratorAliasNone} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err.Error(), want)
		}
	}
	if store.saved {
		t.Fatal("a refused alias still wrote the config")
	}
	if got := store.config.Orchestrators[0].Alias; got != "" {
		t.Fatalf("a refused alias was recorded anyway: %q", got)
	}
}

// TestSetOrchestratorAliasRefusesANonERunAlias keeps the field to the one
// provider type it describes: an orchestrator's platform attribution has no
// meaning against an AWS or Cloudflare alias, and storing one would promise a
// resolution that can never happen.
func TestSetOrchestratorAliasRefusesANonERunAlias(t *testing.T) {
	store := newOrchestratorAliasStubStore()
	store.saved = false

	_, err := SetOrchestratorAlias(Context{}, store, SetOrchestratorAliasParams{
		OrchestratorID: "orch-1",
		Alias:          "aws-prod",
	})
	if err == nil {
		t.Fatal("SetOrchestratorAlias() accepted a non-erun alias")
	}
	if !strings.Contains(err.Error(), "not an erun platform alias") {
		t.Fatalf("error %q does not say the alias is the wrong type", err.Error())
	}
	if store.saved {
		t.Fatal("a refused alias still wrote the config")
	}
}

func TestSetOrchestratorAliasUnknownOrchestrator(t *testing.T) {
	store := newOrchestratorAliasStubStore()
	store.saved = false

	_, err := SetOrchestratorAlias(Context{}, store, SetOrchestratorAliasParams{
		OrchestratorID: "nope",
		Alias:          "erun+erunpaas.com@erun",
	})
	if err == nil || !strings.Contains(err.Error(), `orchestrator "nope" not found`) {
		t.Fatalf("error = %v, want an unknown-orchestrator refusal", err)
	}
	if store.saved {
		t.Fatal("an unknown orchestrator still wrote the config")
	}
}

func TestSetOrchestratorAliasMissingOrchestratorID(t *testing.T) {
	store := newOrchestratorAliasStubStore()
	store.saved = false

	_, err := SetOrchestratorAlias(Context{}, store, SetOrchestratorAliasParams{Alias: "erun+erunpaas.com@erun"})
	if err == nil || !strings.Contains(err.Error(), "no orchestrator id given") {
		t.Fatalf("error = %v, want a missing-id refusal", err)
	}
	if !strings.Contains(err.Error(), "erun orchestrator set-alias") {
		t.Fatalf("error %q does not name the command to run", err.Error())
	}
	if store.saved {
		t.Fatal("a missing id still wrote the config")
	}
}

// TestSetOrchestratorAliasDryRunValidatesAndWritesNothing pins the dry-run
// contract the integration scenarios depend on: the same resolution runs, the
// write does not, and an alias a real run would refuse is refused here too --
// a dry run that planned a write it cannot perform would be worse than none.
func TestSetOrchestratorAliasDryRunValidatesAndWritesNothing(t *testing.T) {
	store := newOrchestratorAliasStubStore()
	store.saved = false

	ctx := Context{DryRun: true}
	if _, err := SetOrchestratorAlias(ctx, store, SetOrchestratorAliasParams{
		OrchestratorID: "orch-1",
		Alias:          "erun+erunpaas.com@erun",
	}); err != nil {
		t.Fatalf("dry run error = %v", err)
	}
	if store.saved {
		t.Fatal("dry run wrote the config")
	}

	if _, err := SetOrchestratorAlias(ctx, store, SetOrchestratorAliasParams{
		OrchestratorID: "orch-1",
		Alias:          "erun+elsewhere.example@erun",
	}); err == nil {
		t.Fatal("dry run accepted an alias a real run refuses")
	}
}

// orchestratorAliasStubStore is the minimal OrchestratorAliasStore the
// set-alias writer needs: one orchestrator, and a cloudproviders list holding
// one erun-type alias and one of another type. saved records whether a write
// actually reached the config, so a refused alias can be shown to have written
// nothing.
type orchestratorAliasStubStore struct {
	config ERunConfig
	saved  bool
}

func newOrchestratorAliasStubStore() *orchestratorAliasStubStore {
	return &orchestratorAliasStubStore{
		config: ERunConfig{
			CloudProviders: []CloudProviderConfig{
				{Alias: "erun+erunpaas.com@erun", Provider: CloudProviderERun},
				{Alias: "aws-prod", Provider: CloudProviderAWS},
			},
			Orchestrators: []OrchestratorConfig{{
				ID:           "orch-1",
				Name:         "Orchestrator One",
				Environments: []OrchestratorEnvConfig{{Tenant: "frs", Environment: "laptop", Directory: "/repo"}},
			}},
		},
	}
}

func (s *orchestratorAliasStubStore) LoadERunConfig() (ERunConfig, string, error) {
	return s.config, "", nil
}

func (s *orchestratorAliasStubStore) SaveERunConfig(config ERunConfig) error {
	s.config = config
	s.saved = true
	return nil
}
