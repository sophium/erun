package orchestrator

import (
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

func TestParseOrchestratorAliasFlagRefusesABlankValue(t *testing.T) {
	for _, value := range []string{"", "   "} {
		if got, err := ParseOrchestratorAliasFlag(value); err == nil {
			t.Fatalf("ParseOrchestratorAliasFlag(%q) = %q, want an error", value, got)
		}
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
