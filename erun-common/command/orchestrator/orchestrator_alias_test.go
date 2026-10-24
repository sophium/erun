package orchestrator

import (
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

func TestParseOrchestratorAliasFlag(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "padded", value: "  erun+erunpaas.com@erun  ", want: "erun+erunpaas.com@erun"},
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

	orchestrator := mustSetOrchestratorAlias(t, store, "orch-1", "erun+erunpaas.com@erun")
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

	// Running the same write twice converges rather than accumulating: the
	// alias is a field of an existing entry, so a repeated set must leave one
	// orchestrator carrying one alias, never a second entry or a duplicate
	// mapping.
	mustSetOrchestratorAlias(t, store, "orch-1", "erun+erunpaas.com@erun")
	if len(store.config.Orchestrators) != 1 {
		t.Fatalf("a repeated write changed the orchestrator count: %+v", store.config.Orchestrators)
	}
	if got := store.config.Orchestrators[0].Alias; got != "erun+erunpaas.com@erun" {
		t.Fatalf("a repeated write changed the alias: %q", got)
	}

	if _, err := SetOrchestratorAlias(core.Context{}, store, SetOrchestratorAliasParams{
		OrchestratorID: "orch-1",
		Alias:          "",
	}); err != nil {
		t.Fatalf("clearing error = %v", err)
	}
	if got := store.config.Orchestrators[0].Alias; got != "" {
		t.Fatalf("cleared alias = %q, want empty", got)
	}
}

func TestSetOrchestratorAliasMissingOrchestratorID(t *testing.T) {
	store := newOrchestratorAliasStubStore()
	store.saved = false

	_, err := SetOrchestratorAlias(core.Context{}, store, SetOrchestratorAliasParams{Alias: "erun+erunpaas.com@erun"})
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

	ctx := core.Context{DryRun: true}
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

func mustSetOrchestratorAlias(t *testing.T, store *orchestratorAliasStubStore, id, alias string) configpkg.OrchestratorConfig {
	t.Helper()
	orchestrator, err := SetOrchestratorAlias(core.Context{}, store, SetOrchestratorAliasParams{
		OrchestratorID: id,
		Alias:          alias,
	})
	if err != nil {
		t.Fatalf("SetOrchestratorAlias(%q, %q) error = %v", id, alias, err)
	}
	return orchestrator
}

// orchestratorAliasStubStore is the minimal OrchestratorAliasStore the
// set-alias writer needs: one orchestrator, and a cloudproviders list holding
// one erun-type alias and one of another type. saved records whether a write
// actually reached the config, so a refused alias can be shown to have written
// nothing.
type orchestratorAliasStubStore struct {
	config configpkg.ERunConfig
	saved  bool
}

func newOrchestratorAliasStubStore() *orchestratorAliasStubStore {
	return &orchestratorAliasStubStore{
		config: configpkg.ERunConfig{
			CloudProviders: []configpkg.CloudProviderConfig{
				{Alias: "erun+erunpaas.com@erun", Provider: configpkg.CloudProviderERun},
				{Alias: "aws-prod", Provider: configpkg.CloudProviderAWS},
			},
			Orchestrators: []configpkg.OrchestratorConfig{{
				ID:           "orch-1",
				Name:         "Orchestrator One",
				Environments: []configpkg.OrchestratorEnvConfig{{Tenant: "frs", Environment: "laptop", Directory: "/repo"}},
			}},
		},
	}
}

func (s *orchestratorAliasStubStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s *orchestratorAliasStubStore) SaveERunConfig(config configpkg.ERunConfig) error {
	s.config = config
	s.saved = true
	return nil
}
