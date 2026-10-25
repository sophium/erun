package main

import (
	"context"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestListAgentOutputsResolvesEnvAndLists(t *testing.T) {
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "local"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/local": {Name: "local", LocalRepoPath: t.TempDir(), KubernetesContext: "rancher-desktop"},
		},
	}
	var gotResult configpkg.OpenResult
	app := NewApp(erunUIDeps{
		store: store,
		listAgentOutputs: func(result configpkg.OpenResult, _ model.RuntimeOutputsParams) (model.RuntimeOutputsListResult, error) {
			gotResult = result
			return model.RuntimeOutputsListResult{
				Dir:     "/home/erun/.erun/outputs",
				Total:   1,
				Entries: []model.OutputEntry{{Name: "report.pdf", Size: 10}},
			}, nil
		},
	})
	defer app.shutdown(context.Background())

	got, err := app.ListAgentOutputs(uimodel.Selection{Tenant: "erun", Environment: "local"})
	if err != nil {
		t.Fatalf("ListAgentOutputs failed: %v", err)
	}
	if gotResult.Tenant != "erun" || gotResult.Environment != "local" {
		t.Fatalf("dep resolved the wrong env: %+v", gotResult)
	}
	if len(got.Entries) != 1 || got.Entries[0].Name != "report.pdf" {
		t.Fatalf("unexpected list result: %+v", got)
	}
}

func TestListAgentOutputsRequiresSelection(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	defer app.shutdown(context.Background())
	if _, err := app.ListAgentOutputs(uimodel.Selection{}); err == nil {
		t.Fatal("expected an error when tenant/environment are missing")
	}
}

func TestDownloadAgentOutputRequiresSelection(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	defer app.shutdown(context.Background())
	if _, err := app.DownloadAgentOutput(uimodel.Selection{}, "report.pdf"); err == nil {
		t.Fatal("expected an error when tenant/environment are missing")
	}
}
