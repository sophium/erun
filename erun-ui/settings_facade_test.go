package main

import (
	"context"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/runtimeref"
	settingssvc "github.com/sophium/erun/erun-ui/internal/settings"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The settings service reaches a remote worktree's pod through the MCP edge App
// wires; this drives that wiring end to end.
func TestSaveRemoteEnvironmentConfigSetsCloudAliasViaMCP(t *testing.T) {
	projectRoot := runtimeref.RemoteWorktreePathForRepoName("frs")
	rootConfig := &configpkg.ERunConfig{
		CloudContexts: []model.CloudContextConfig{{
			Name:               "team-context",
			CloudProviderAlias: "team-cloud",
			KubernetesContext:  "cluster-dev",
		}},
	}
	store := stubUIStore{
		config: rootConfig,
		tenants: map[string]model.TenantConfig{
			"frs": {
				Name:               "frs",
				DefaultEnvironment: "dev",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/dev": {
				Name:               "dev",
				LocalRepoPath:      projectRoot,
				KubernetesContext:  "cluster-dev",
				CloudProviderAlias: "old-cloud",
				Type:               configpkg.EnvironmentTypeRuntime,
			},
		},
	}
	var remoteEndpoint string
	var remoteTenant string
	var remoteEnvironment string
	var remoteAlias string
	app := NewApp(erunUIDeps{
		store:               store,
		canConnectLocalPort: func(int) bool { return true },
		canReachMCPEndpoint: func(int) bool { return true },
		setRemoteCloudAlias: func(_ context.Context, endpoint, _, tenant, environment, alias string) (configpkg.EnvConfig, error) {
			remoteEndpoint = endpoint
			remoteTenant = tenant
			remoteEnvironment = environment
			remoteAlias = alias
			return configpkg.EnvConfig{Name: environment, CloudProviderAlias: alias}, nil
		},
	})

	saved, err := app.SaveEnvironmentConfig(uimodel.Selection{Tenant: "frs", Environment: "dev"}, settingssvc.EnvironmentConfig{
		Name:               "dev",
		CloudProviderAlias: "team-cloud",
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	if saved.CloudProviderAlias != "team-cloud" || store.envs["frs/dev"].CloudProviderAlias != "team-cloud" || !store.envs["frs/dev"].ManagedCloud {
		t.Fatalf("unexpected saved config: result=%+v stored=%+v", saved, store.envs["frs/dev"])
	}
	if remoteEndpoint != "http://127.0.0.1:17000/mcp" || remoteTenant != "frs" || remoteEnvironment != "dev" || remoteAlias != "team-cloud" {
		t.Fatalf("unexpected remote alias call: endpoint=%q tenant=%q environment=%q alias=%q", remoteEndpoint, remoteTenant, remoteEnvironment, remoteAlias)
	}
}
