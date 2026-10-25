package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	configcloudcontext "github.com/sophium/erun/erun-common/base/config/cloudcontext"
	"github.com/sophium/erun/erun-common/base/model"
	cloudcloudcontext "github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestDeleteEnvironmentRequiresExactConfirmationAndDeletesConfig(t *testing.T) {
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"frs": {
				Name:               "frs",
				DefaultEnvironment: "prod",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				LocalRepoPath:     "/home/erun/git/frs",
				KubernetesContext: "cluster-prod",
				Type:              configpkg.EnvironmentTypeRuntime,
			},
		},
	}

	var deletedContext string
	var deletedNamespace string
	app := NewApp(erunUIDeps{
		store: store,
		deleteNamespace: func(contextName, namespace string) error {
			deletedContext = contextName
			deletedNamespace = namespace
			return nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.DeleteEnvironment(uimodel.Selection{Tenant: "frs", Environment: "prod"}, "wrong"); err == nil {
		t.Fatal("expected confirmation mismatch")
	}
	if _, ok := store.envs["frs/prod"]; !ok {
		t.Fatal("expected env config to remain after failed confirmation")
	}

	result, err := app.DeleteEnvironment(uimodel.Selection{Tenant: "frs", Environment: "prod"}, "frs-prod")
	if err != nil {
		t.Fatalf("DeleteEnvironment failed: %v", err)
	}
	assertDeletedEnvironment(t, store, result, deletedContext, deletedNamespace)
}

func assertDeletedEnvironment(t *testing.T, store stubUIStore, result terminal.DeleteEnvironmentResult, deletedContext, deletedNamespace string) {
	t.Helper()

	if deletedContext != "cluster-prod" || deletedNamespace != "frs-prod" {
		t.Fatalf("unexpected namespace deletion: context=%q namespace=%q", deletedContext, deletedNamespace)
	}
	if result.Namespace != "frs-prod" || result.Tenant != "frs" || result.Environment != "prod" {
		t.Fatalf("unexpected delete result: %+v", result)
	}
	if _, ok := store.envs["frs/prod"]; ok {
		t.Fatal("expected env config to be deleted")
	}
	if store.tenants["frs"].DefaultEnvironment != "" {
		t.Fatalf("expected deleted default environment to be cleared, got %+v", store.tenants["frs"])
	}
}

func TestDeleteEnvironmentStartsLinkedContextThenStopsIt(t *testing.T) {
	projectRoot := t.TempDir()
	rootConfig := &configpkg.ERunConfig{
		DefaultTenant: "frs",
		CloudProviders: []model.CloudProviderConfig{
			{Alias: "team-cloud", Provider: configpkg.CloudProviderAWS},
		},
		CloudContexts: []model.CloudContextConfig{
			{
				Name:               "team-context",
				Provider:           configpkg.CloudProviderAWS,
				CloudProviderAlias: "team-cloud",
				Region:             cloudcloudcontext.DefaultCloudContextRegion,
				InstanceID:         "i-test",
				InstanceType:       configcloudcontext.DefaultCloudContextInstanceType,
				DiskType:           configcloudcontext.DefaultCloudContextDiskType,
				DiskSizeGB:         configcloudcontext.DefaultCloudContextDiskSizeGB,
				KubernetesContext:  "cluster-prod",
				AdminToken:         "test-token",
			},
		},
	}
	store := stubUIStore{
		config: rootConfig,
		tenants: map[string]model.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "prod"},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-prod",
				Type:              configpkg.EnvironmentTypeRuntime,
			},
		},
	}
	var actions []string
	app := NewApp(erunUIDeps{
		store:            store,
		cloudContextDeps: testCloudContextDeps(&actions),
		deleteNamespace: func(context, namespace string) error {
			actions = append(actions, "delete-namespace "+context+" "+namespace)
			return nil
		},
	})

	result, err := app.DeleteEnvironment(uimodel.Selection{Tenant: "frs", Environment: "prod"}, "frs-prod")
	if err != nil {
		t.Fatalf("DeleteEnvironment failed: %v", err)
	}
	if result.CloudContextStopError != "" {
		t.Fatalf("unexpected cloud context stop error: %+v", result)
	}
	got := strings.Join(actions, "\n")
	for _, want := range []string{
		"aws ec2 start-instances --instance-ids i-test",
		"delete-namespace cluster-prod frs-prod",
		"aws ec2 stop-instances --instance-ids i-test",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected action %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "aws ec2 start-instances") > strings.Index(got, "delete-namespace") || strings.Index(got, "delete-namespace") > strings.Index(got, "aws ec2 stop-instances") {
		t.Fatalf("unexpected delete ordering:\n%s", got)
	}
	if _, _, err := store.LoadEnvConfig("frs", "prod"); !errors.Is(err, configpkg.ErrNotInitialized) {
		t.Fatalf("expected environment config to be deleted, got %v", err)
	}
	// Cloud-context Status is not persisted; the stop-instances action above is the signal.
}
