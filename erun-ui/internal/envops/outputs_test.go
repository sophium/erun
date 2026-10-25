package envops

import (
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestListAgentOutputsResolvesEnvAndLists(t *testing.T) {
	store := fakeStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "local"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/local": {Name: "local", LocalRepoPath: t.TempDir(), KubernetesContext: "rancher-desktop"},
		},
	}
	var gotResult configpkg.OpenResult
	service := newTestService(Deps{
		Store: store,
		ListAgentOutputs: func(result configpkg.OpenResult, _ model.RuntimeOutputsParams) (model.RuntimeOutputsListResult, error) {
			gotResult = result
			return model.RuntimeOutputsListResult{
				Dir:     "/home/erun/.erun/outputs",
				Total:   1,
				Entries: []model.OutputEntry{{Name: "report.pdf", Size: 10}},
			}, nil
		},
	})

	got, err := service.ListAgentOutputs(uimodel.Selection{Tenant: "erun", Environment: "local"})
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
	service := newTestService(Deps{})
	if _, err := service.ListAgentOutputs(uimodel.Selection{}); err == nil {
		t.Fatal("expected an error when tenant/environment are missing")
	}
}

func TestDownloadAgentOutputRequiresSelection(t *testing.T) {
	service := newTestService(Deps{})
	if _, err := service.DownloadAgentOutput(uimodel.Selection{}, "report.pdf", func(string) (string, error) { return "", nil }); err == nil {
		t.Fatal("expected an error when tenant/environment are missing")
	}
}
