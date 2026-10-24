package cloudconfig

import (
	"io"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// inClusterPodEnv has a cloud provider set, but the pod is the workload, not an instance.
func inClusterPodEnv() func(string) string {
	values := map[string]string{
		"ERUN_TENANT":               "frs",
		"ERUN_ENVIRONMENT":          "prod",
		"ERUN_ENV_TYPE":             "runtime",
		"ERUN_CLOUD_PROVIDER":       "aws",
		"ERUN_CLOUD_PROVIDER_ALIAS": "Rihards+306489807852@aws",
		"ERUN_CLOUD_REGION":         "eu-central-1",
	}
	return func(key string) string { return values[key] }
}

func inClusterPreflightTestContext() core.Context {
	return core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard)}
}

func countingRunAWS(calls *int) func(core.Context, model.CloudProviderConfig, string, []string) (string, error) {
	return func(_ core.Context, _ model.CloudProviderConfig, _ string, _ []string) (string, error) {
		*calls++
		return "", nil
	}
}

func inClusterPreflightStore(t *testing.T) cloud.CloudContextStore {
	t.Helper()
	injected, ok := ResolveInjectedRuntimeConfig(inClusterPodEnv())
	if !ok {
		t.Fatal("expected the pod's env to resolve to an injected runtime config")
	}
	if len(injected.Contexts) != 1 {
		t.Fatalf("injected contexts = %+v, want exactly the pod's own context", injected.Contexts)
	}
	return stubCloudContextStore{config: configpkg.ERunConfig{
		CloudProviders: injected.Providers,
		CloudContexts:  injected.Contexts,
	}}
}

func TestResolveInjectedRuntimeConfigNamesTheInClusterSentinel(t *testing.T) {
	injected, ok := ResolveInjectedRuntimeConfig(inClusterPodEnv())
	if !ok {
		t.Fatal("expected the pod's env to resolve to an injected runtime config")
	}
	if got := injected.Env.KubernetesContext; got != cloud.InClusterKubernetesContext {
		t.Fatalf("injected kubernetes context = %q, want %q", got, cloud.InClusterKubernetesContext)
	}
	if len(injected.Contexts) != 1 {
		t.Fatalf("injected contexts = %+v, want exactly the pod's own context", injected.Contexts)
	}
	context := injected.Contexts[0]
	if context.InstanceID != "" {
		t.Fatalf("injected instance ID = %q, want empty (a pod has no instance)", context.InstanceID)
	}
	if !cloud.IsInClusterCloudContext(context) {
		t.Fatalf("injected context %+v is not recognized as in-cluster", context)
	}
}

// The working-hours gate lives inside StartCloudContext, so no start means no gate.
func TestCloudContextPreflightInClusterIsAlreadyRunning(t *testing.T) {
	calls := 0
	deps := cloud.CloudContextDependencies{RunAWS: countingRunAWS(&calls)}
	preflight := cloud.CloudContextPreflight(inClusterPreflightStore(t), deps)
	if err := preflight(inClusterPreflightTestContext(), cloud.InClusterKubernetesContext); err != nil {
		t.Fatalf("in-cluster preflight failed: %v", err)
	}
	if calls != 0 {
		t.Fatalf("in-cluster preflight made %d AWS calls, want 0", calls)
	}
}

func TestCloudContextPreflightInClusterRepeatedCallsStayClean(t *testing.T) {
	calls := 0
	deps := cloud.CloudContextDependencies{RunAWS: countingRunAWS(&calls)}
	preflight := cloud.CloudContextPreflight(inClusterPreflightStore(t), deps)
	for i := 0; i < 2; i++ {
		if err := preflight(inClusterPreflightTestContext(), cloud.InClusterKubernetesContext); err != nil {
			t.Fatalf("in-cluster preflight call %d failed: %v", i+1, err)
		}
	}
	if calls != 0 {
		t.Fatalf("in-cluster preflight made %d AWS calls, want 0", calls)
	}
}
