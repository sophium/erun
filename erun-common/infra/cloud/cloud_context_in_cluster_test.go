package cloud

import (
	"io"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

func inClusterPreflightTestContext() core.Context {
	return core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard)}
}

// countingRunAWS counts AWS calls; an in-cluster context must make none.
func countingRunAWS(calls *int) func(core.Context, model.CloudProviderConfig, string, []string) (string, error) {
	return func(_ core.Context, _ model.CloudProviderConfig, _ string, _ []string) (string, error) {
		*calls++
		return "", nil
	}
}

func TestCloudContextPreflightInClusterMatchedByKubernetesContext(t *testing.T) {
	store := stubCloudContextStore{config: configpkg.ERunConfig{
		CloudProviders: []model.CloudProviderConfig{{Alias: "dev-aws", Provider: configpkg.CloudProviderAWS}},
		CloudContexts: []model.CloudContextConfig{{
			Name:               "prod-platform",
			Provider:           configpkg.CloudProviderAWS,
			CloudProviderAlias: "dev-aws",
			Region:             "eu-central-1",
			KubernetesContext:  InClusterKubernetesContext,
		}},
	}}
	calls := 0
	deps := CloudContextDependencies{RunAWS: countingRunAWS(&calls)}
	if err := CloudContextPreflight(store, deps)(inClusterPreflightTestContext(), InClusterKubernetesContext); err != nil {
		t.Fatalf("in-cluster preflight failed: %v", err)
	}
	if calls != 0 {
		t.Fatalf("in-cluster preflight made %d AWS calls, want 0", calls)
	}
}

func TestCloudContextPreflightPowerManagedWithoutInstanceIDStillFails(t *testing.T) {
	store := stubCloudContextStore{config: configpkg.ERunConfig{
		CloudProviders: []model.CloudProviderConfig{{Alias: "dev-aws", Provider: configpkg.CloudProviderAWS, Profile: "dev-profile"}},
		CloudContexts: []model.CloudContextConfig{{
			Name:               "petios-ctx",
			Provider:           configpkg.CloudProviderAWS,
			CloudProviderAlias: "dev-aws",
			Region:             "eu-west-1",
			KubernetesContext:  "petios-ctx",
		}},
	}}
	calls := 0
	deps := CloudContextDependencies{RunAWS: countingRunAWS(&calls)}
	err := CloudContextPreflight(store, deps)(inClusterPreflightTestContext(), "petios-ctx")
	if err == nil {
		t.Fatal("expected a power-managed context with no instance ID to fail preflight")
	}
	const want = `cloud context "petios-ctx" has no instance ID`
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if calls != 0 {
		t.Fatalf("preflight made %d AWS calls before failing, want 0", calls)
	}
}

func TestIsInClusterCloudContextDistinguishesPowerManagedContexts(t *testing.T) {
	cases := []struct {
		name   string
		config model.CloudContextConfig
		want   bool
	}{
		{"pod-injected context", model.CloudContextConfig{Name: InClusterKubernetesContext, KubernetesContext: InClusterKubernetesContext}, true},
		{"kube context only", model.CloudContextConfig{Name: "prod-platform", KubernetesContext: InClusterKubernetesContext}, true},
		{"power-managed without an instance", model.CloudContextConfig{Name: "petios-ctx", KubernetesContext: "petios-ctx"}, false},
		{"power-managed with an instance", model.CloudContextConfig{Name: "petios-ctx", KubernetesContext: "petios-ctx", InstanceID: "i-0abc123"}, false},
		{"blank", model.CloudContextConfig{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsInClusterCloudContext(tc.config); got != tc.want {
				t.Fatalf("isInClusterCloudContext(%+v) = %v, want %v", tc.config, got, tc.want)
			}
		})
	}
}
