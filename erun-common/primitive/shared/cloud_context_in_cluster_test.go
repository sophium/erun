package shared

import (
	"io"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// inClusterPodEnv is the env a runtime pod's chart injects: a cloud provider,
// alias and region are set, but the pod has no instance — it *is* the workload.
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

// countingRunAWS fails the test's intent if any AWS call is made: an in-cluster
// context has nothing to power on, so preflight must not reach AWS at all.
func countingRunAWS(calls *int) func(core.Context, configpkg.CloudProviderConfig, string, []string) (string, error) {
	return func(_ core.Context, _ configpkg.CloudProviderConfig, _ string, _ []string) (string, error) {
		*calls++
		return "", nil
	}
}

// inClusterPreflightStore is the config a runtime pod's own injected env
// produces — the exact store preflight reads on the pod.
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

// TestResolveInjectedRuntimeConfigNamesTheInClusterSentinel pins the half of the
// defect that synthesises the context: a pod with no ERUN_KUBERNETES_CONTEXT
// gets the in-cluster sentinel, and the context built from it is recognised as
// the pod's own cluster rather than as a power-manageable one.
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

// TestCloudContextPreflightInClusterIsAlreadyRunning pins the fix: an
// in-cluster context names the cluster the pod already runs in, so preflight
// returns cleanly instead of refreshing its (nonexistent) instance and powering
// it on. The working-hours gate lives inside StartCloudContext, so "no start"
// is also "no gate" — the trace that preceded the failure was
// `checking working-hours gate`.
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

// TestCloudContextPreflightInClusterRepeatedCallsStayClean covers the started
// cache: the second call in the same run stays clean too.
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
