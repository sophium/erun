package orchestrator

import (
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
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

	if len(store.config.Orchestrators[0].Environments) != 1 || store.config.Orchestrators[0].Name != "Orchestrator One" {
		t.Fatalf("write dropped the rest of the orchestrator: %+v", store.config.Orchestrators[0])
	}

	// A repeated set converges to one entry with one alias.
	mustSetOrchestratorAlias(t, store, "orch-1", "erun+erunpaas.com@erun")
	if len(store.config.Orchestrators) != 1 {
		t.Fatalf("a repeated write changed the orchestrator count: %+v", store.config.Orchestrators)
	}
	if got := store.config.Orchestrators[0].Alias; got != "erun+erunpaas.com@erun" {
		t.Fatalf("a repeated write changed the alias: %q", got)
	}

	if _, err := SetOrchestratorAlias(core.Context{}, store, model.SetOrchestratorAliasParams{
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

	_, err := SetOrchestratorAlias(core.Context{}, store, model.SetOrchestratorAliasParams{Alias: "erun+erunpaas.com@erun"})
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

func TestSetOrchestratorAliasDryRunValidatesAndWritesNothing(t *testing.T) {
	store := newOrchestratorAliasStubStore()
	store.saved = false

	ctx := core.Context{DryRun: true}
	if _, err := SetOrchestratorAlias(ctx, store, model.SetOrchestratorAliasParams{
		OrchestratorID: "orch-1",
		Alias:          "erun+erunpaas.com@erun",
	}); err != nil {
		t.Fatalf("dry run error = %v", err)
	}
	if store.saved {
		t.Fatal("dry run wrote the config")
	}

	if _, err := SetOrchestratorAlias(ctx, store, model.SetOrchestratorAliasParams{
		OrchestratorID: "orch-1",
		Alias:          "erun+elsewhere.example@erun",
	}); err == nil {
		t.Fatal("dry run accepted an alias a real run refuses")
	}
}

func mustSetOrchestratorAlias(t *testing.T, store *orchestratorAliasStubStore, id, alias string) configpkg.OrchestratorConfig {
	t.Helper()
	orchestrator, err := SetOrchestratorAlias(core.Context{}, store, model.SetOrchestratorAliasParams{
		OrchestratorID: id,
		Alias:          alias,
	})
	if err != nil {
		t.Fatalf("SetOrchestratorAlias(%q, %q) error = %v", id, alias, err)
	}
	return orchestrator
}

// saved records whether a write reached the config.
type orchestratorAliasStubStore struct {
	config configpkg.ERunConfig
	saved  bool
}

func newOrchestratorAliasStubStore() *orchestratorAliasStubStore {
	return &orchestratorAliasStubStore{
		config: configpkg.ERunConfig{
			CloudProviders: []model.CloudProviderConfig{
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
