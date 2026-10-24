package deploy

import (
	"os"
	"path/filepath"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

func inPodProjection(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func inPodProjectionRuntimeSpecs(tenant string) []DeploySpec {
	return []DeploySpec{{
		Target: configpkg.OpenResult{Tenant: tenant},
		Deploy: HelmDeploySpec{ReleaseName: configpkg.RuntimeReleaseName(tenant), Version: "1.2.3"},
	}}
}

func inPodProjectionRuntimeChart(t *testing.T) string {
	t.Helper()
	chartPath := t.TempDir()
	manifest := "apiVersion: v2\nname: " + configpkg.DevopsComponentName +
		"\ndependencies:\n  - name: " + configpkg.DevopsComponentName + "\n    alias: runtime\n"
	if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	return chartPath
}

func TestGuardInPodRuntimeDeployRefusesAnInPodProjection(t *testing.T) {
	inPod := map[string]string{"ERUN_TENANT": "tenant-a", "ERUN_ENVIRONMENT": "dev"}
	for _, tc := range []struct {
		name      string
		envType   configpkg.EnvironmentType
		env       func(string) string
		tenant    string
		component string
		wantError bool
	}{
		{
			name:    "another environment's pod is not this environment's projection",
			envType: configpkg.EnvironmentTypeRemoteAgent,
			env: inPodProjection(map[string]string{
				"ERUN_TENANT": "tenant-a", "ERUN_ENVIRONMENT": "prod",
			}),
			tenant:    "tenant-a",
			component: "tenant-a-devops",
			wantError: false,
		},
		{
			name:      "a non-runtime component is left alone",
			envType:   configpkg.EnvironmentTypeRemoteAgent,
			env:       inPodProjection(inPod),
			tenant:    "tenant-a",
			component: "tenant-a-devops-api",
			wantError: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := configpkg.OpenResult{
				Tenant:      tc.tenant,
				Environment: "dev",
				EnvConfig:   configpkg.EnvConfig{Name: "dev", Type: tc.envType},
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

// TestInPodProjectionResolvesDeployValuesTheGuardRefuses shows the values the
// guard keeps out of a rollout.
func TestInPodProjectionResolvesDeployValuesTheGuardRefuses(t *testing.T) {
	// Clear the pod identity so the resolver never reads this machine's cgroup.
	t.Setenv("ERUN_TENANT", "")
	t.Setenv("ERUN_ENVIRONMENT", "")
	t.Setenv("ERUN_ENV_TYPE", "")

	chartPath := inPodProjectionRuntimeChart(t)
	deployContext := model.KubernetesDeployContext{ComponentName: "tenant-a-devops", ChartPath: chartPath}
	target := func(config configpkg.EnvConfig) configpkg.OpenResult {
		config.Name, config.Type = "dev", configpkg.EnvironmentTypeRemoteAgent
		return configpkg.OpenResult{Tenant: "tenant-a", Environment: "dev", EnvConfig: config}
	}
	hostStore := configpkg.EnvConfig{SSHD: configpkg.SSHDConfig{Enabled: true}, RuntimePod: model.RuntimePodResources{CPU: "4", Memory: "8192Mi"}}
	projection := configpkg.EnvConfig{}

	for _, tc := range []struct {
		name       string
		config     configpkg.EnvConfig
		wantSSHD   bool
		wantMemory string
	}{
		{name: "the host store resolves the environment's own values", config: hostStore, wantSSHD: true, wantMemory: "8192Mi"},
		{name: "the in-pod projection resolves substituted values", config: projection, wantSSHD: false, wantMemory: configpkg.DefaultRuntimePodMemory},
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
