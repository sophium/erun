package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

func inPodProjection(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func inPodProjectionRuntimeSpecs(tenant string) []DeploySpec {
	return []DeploySpec{{
		Target: OpenResult{Tenant: tenant},
		Deploy: HelmDeploySpec{ReleaseName: RuntimeReleaseName(tenant), Version: "1.2.3"},
	}}
}

// inPodProjectionRuntimeChart writes the minimal runtime umbrella a repo-local
// runtime chart resolves through, so a deploy spec can be built from it.
func inPodProjectionRuntimeChart(t *testing.T) string {
	t.Helper()
	chartPath := t.TempDir()
	manifest := "apiVersion: v2\nname: " + DevopsComponentName +
		"\ndependencies:\n  - name: " + DevopsComponentName + "\n    alias: runtime\n"
	if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	return chartPath
}

// TestGuardInPodRuntimeDeployRefusesAnInPodProjection is the reported failure: a
// remote-agent environment's own pod resolving its own runtime chart was not
// refused, so the projection's values were carried straight into the rollout.
// What makes an in-pod resolve untrustworthy is that it read the chart's
// projection rather than the host config store, and that is as true of a
// remote-agent env's own runtime pod as of a local-agent one — the guard's
// original scoping to the environment type answered a different question.
func TestGuardInPodRuntimeDeployRefusesAnInPodProjection(t *testing.T) {
	inPod := map[string]string{"ERUN_TENANT": "tenant-a", "ERUN_ENVIRONMENT": "dev"}
	for _, tc := range []struct {
		name      string
		envType   EnvironmentType
		env       func(string) string
		tenant    string
		component string
		wantError bool
	}{
		{
			name:      "a remote-agent env's own pod is refused",
			envType:   EnvironmentTypeRemoteAgent,
			env:       inPodProjection(inPod),
			tenant:    "tenant-a",
			component: "tenant-a-devops",
			wantError: true,
		},
		{
			name:      "a local-agent env's own pod is refused",
			envType:   EnvironmentTypeLocalAgent,
			env:       inPodProjection(inPod),
			tenant:    "tenant-a",
			component: "tenant-a-devops",
			wantError: true,
		},
		{
			name:      "a runtime env's own pod is refused",
			envType:   EnvironmentTypeRuntime,
			env:       inPodProjection(inPod),
			tenant:    "tenant-a",
			component: "tenant-a-devops",
			wantError: true,
		},
		{
			name:      "off-pod the same resolve is allowed",
			envType:   EnvironmentTypeRemoteAgent,
			env:       inPodProjection(map[string]string{}),
			tenant:    "tenant-a",
			component: "tenant-a-devops",
			wantError: false,
		},
		{
			name:    "another environment's pod is not this environment's projection",
			envType: EnvironmentTypeRemoteAgent,
			env: inPodProjection(map[string]string{
				"ERUN_TENANT": "tenant-a", "ERUN_ENVIRONMENT": "prod",
			}),
			tenant:    "tenant-a",
			component: "tenant-a-devops",
			wantError: false,
		},
		{
			// A component-only deploy carries no environment shape, so an env's
			// own pod keeps deploying its own components.
			name:      "a non-runtime component is left alone",
			envType:   EnvironmentTypeRemoteAgent,
			env:       inPodProjection(inPod),
			tenant:    "tenant-a",
			component: "tenant-a-devops-api",
			wantError: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := OpenResult{
				Tenant:      tc.tenant,
				Environment: "dev",
				EnvConfig:   EnvConfig{Name: "dev", Type: tc.envType},
			}
			specs := inPodProjectionRuntimeSpecs(tc.tenant)
			specs[0].Deploy.ReleaseName = tc.component
			err := guardInPodRuntimeDeploy(tc.env, target, specs)
			if tc.wantError && err == nil {
				t.Fatal("expected the in-pod projection to be refused, got no error")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("expected no refusal, got %v", err)
			}
		})
	}
}

// TestInPodProjectionResolvesDeployValuesTheGuardRefuses is the value half of
// the same failure: what the refusal above exists to stop. An environment's own
// pod holds only the chart's projection, so a runtime chart resolved from it
// carries sshdEnabled=false where the environment is in fact running its sshd,
// and the default runtime sizing where the host store holds a configured limit.
// Those are the values that reach the rollout once the resolve is allowed.
func TestInPodProjectionResolvesDeployValuesTheGuardRefuses(t *testing.T) {
	// Deterministic everywhere: without a runtime-pod identity the resolver
	// cannot fall back to this machine's own cgroup limits, so the projection
	// side resolves the same way wherever the suite runs.
	t.Setenv("ERUN_TENANT", "")
	t.Setenv("ERUN_ENVIRONMENT", "")
	t.Setenv("ERUN_ENV_TYPE", "")

	chartPath := inPodProjectionRuntimeChart(t)
	deployContext := KubernetesDeployContext{ComponentName: "tenant-a-devops", ChartPath: chartPath}
	target := func(config EnvConfig) OpenResult {
		config.Name, config.Type = "dev", EnvironmentTypeRemoteAgent
		return OpenResult{Tenant: "tenant-a", Environment: "dev", EnvConfig: config}
	}
	// The host config store's authoritative shape, and the subset of it the
	// chart's projection actually carries.
	hostStore := EnvConfig{SSHD: SSHDConfig{Enabled: true}, RuntimePod: RuntimePodResources{CPU: "4", Memory: "8192Mi"}}
	projection := EnvConfig{}

	for _, tc := range []struct {
		name       string
		config     EnvConfig
		wantSSHD   bool
		wantMemory string
	}{
		{name: "the host store resolves the environment's own values", config: hostStore, wantSSHD: true, wantMemory: "8192Mi"},
		{name: "the in-pod projection resolves substituted values", config: projection, wantSSHD: false, wantMemory: DefaultRuntimePodMemory},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := newHelmDeploySpecWithValues(target(tc.config), deployContext, "", "")
			if err != nil {
				t.Fatalf("resolve deploy spec: %v", err)
			}
			if spec.SSHDEnabled != tc.wantSSHD {
				t.Errorf("expected sshdEnabled=%v, got %v", tc.wantSSHD, spec.SSHDEnabled)
			}
			if spec.RuntimePod.Memory != tc.wantMemory {
				t.Errorf("expected runtime memory %s, got %s", tc.wantMemory, spec.RuntimePod.Memory)
			}
		})
	}
}
