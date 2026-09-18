package eruncommon

import (
	"io"
	"testing"
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

func inClusterPreflightTestContext() Context {
	return Context{Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard)}
}

// countingRunAWS fails the test's intent if any AWS call is made: an in-cluster
// context has nothing to power on, so preflight must not reach AWS at all.
func countingRunAWS(calls *int) func(Context, CloudProviderConfig, string, []string) (string, error) {
	return func(_ Context, _ CloudProviderConfig, _ string, _ []string) (string, error) {
		*calls++
		return "", nil
	}
}

// inClusterPreflightStore is the config a runtime pod's own injected env
// produces — the exact store preflight reads on the pod.
func inClusterPreflightStore(t *testing.T) CloudContextStore {
	t.Helper()
	injected, ok := ResolveInjectedRuntimeConfig(inClusterPodEnv())
	if !ok {
		t.Fatal("expected the pod's env to resolve to an injected runtime config")
	}
	if len(injected.Contexts) != 1 {
		t.Fatalf("injected contexts = %+v, want exactly the pod's own context", injected.Contexts)
	}
	return stubCloudContextStore{config: ERunConfig{
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
	if got := injected.Env.KubernetesContext; got != inClusterKubernetesContext {
		t.Fatalf("injected kubernetes context = %q, want %q", got, inClusterKubernetesContext)
	}
	if len(injected.Contexts) != 1 {
		t.Fatalf("injected contexts = %+v, want exactly the pod's own context", injected.Contexts)
	}
	context := injected.Contexts[0]
	if context.InstanceID != "" {
		t.Fatalf("injected instance ID = %q, want empty (a pod has no instance)", context.InstanceID)
	}
	if !isInClusterCloudContext(context) {
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
	deps := CloudContextDependencies{RunAWS: countingRunAWS(&calls)}
	preflight := CloudContextPreflight(inClusterPreflightStore(t), deps)
	if err := preflight(inClusterPreflightTestContext(), inClusterKubernetesContext); err != nil {
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
	deps := CloudContextDependencies{RunAWS: countingRunAWS(&calls)}
	preflight := CloudContextPreflight(inClusterPreflightStore(t), deps)
	for i := 0; i < 2; i++ {
		if err := preflight(inClusterPreflightTestContext(), inClusterKubernetesContext); err != nil {
			t.Fatalf("in-cluster preflight call %d failed: %v", i+1, err)
		}
	}
	if calls != 0 {
		t.Fatalf("in-cluster preflight made %d AWS calls, want 0", calls)
	}
}

// TestCloudContextPreflightInClusterMatchedByKubernetesContext covers a context
// whose name differs from the kube-context it serves.
func TestCloudContextPreflightInClusterMatchedByKubernetesContext(t *testing.T) {
	store := stubCloudContextStore{config: ERunConfig{
		CloudProviders: []CloudProviderConfig{{Alias: "dev-aws", Provider: CloudProviderAWS}},
		CloudContexts: []CloudContextConfig{{
			Name:               "prod-platform",
			Provider:           CloudProviderAWS,
			CloudProviderAlias: "dev-aws",
			Region:             "eu-central-1",
			KubernetesContext:  inClusterKubernetesContext,
		}},
	}}
	calls := 0
	deps := CloudContextDependencies{RunAWS: countingRunAWS(&calls)}
	if err := CloudContextPreflight(store, deps)(inClusterPreflightTestContext(), inClusterKubernetesContext); err != nil {
		t.Fatalf("in-cluster preflight failed: %v", err)
	}
	if calls != 0 {
		t.Fatalf("in-cluster preflight made %d AWS calls, want 0", calls)
	}
}

// TestCloudContextPreflightPowerManagedWithoutInstanceIDStillFails is the other
// direction: a genuinely power-managed context with no instance ID is a real
// error, not an in-cluster one, and must keep failing with today's message.
func TestCloudContextPreflightPowerManagedWithoutInstanceIDStillFails(t *testing.T) {
	store := stubCloudContextStore{config: ERunConfig{
		CloudProviders: []CloudProviderConfig{{Alias: "dev-aws", Provider: CloudProviderAWS, Profile: "dev-profile"}},
		CloudContexts: []CloudContextConfig{{
			Name:               "petios-ctx",
			Provider:           CloudProviderAWS,
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

// TestIsInClusterCloudContextDistinguishesPowerManagedContexts keeps the
// predicate honest: it names the pod's own cluster, not "any context without an
// instance ID".
func TestIsInClusterCloudContextDistinguishesPowerManagedContexts(t *testing.T) {
	cases := []struct {
		name   string
		config CloudContextConfig
		want   bool
	}{
		{"pod-injected context", CloudContextConfig{Name: inClusterKubernetesContext, KubernetesContext: inClusterKubernetesContext}, true},
		{"kube context only", CloudContextConfig{Name: "prod-platform", KubernetesContext: inClusterKubernetesContext}, true},
		{"power-managed without an instance", CloudContextConfig{Name: "petios-ctx", KubernetesContext: "petios-ctx"}, false},
		{"power-managed with an instance", CloudContextConfig{Name: "petios-ctx", KubernetesContext: "petios-ctx", InstanceID: "i-0abc123"}, false},
		{"blank", CloudContextConfig{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInClusterCloudContext(tc.config); got != tc.want {
				t.Fatalf("isInClusterCloudContext(%+v) = %v, want %v", tc.config, got, tc.want)
			}
		})
	}
}
