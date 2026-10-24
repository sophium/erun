package podruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/doctor"
)

func TestRuntimeBootConfigCloudContextDefaults(t *testing.T) {
	for _, tc := range []struct {
		label, contextName, kubeContext, wantName, wantKube string
	}{
		{"both-unset", "", "", "in-cluster", "in-cluster"},
		{"kubernetes-context-only", "", "kube-ctx", "kube-ctx", "kube-ctx"},
		{"cloud-context-name-set", "outer-ctx", "", "outer-ctx", "in-cluster"},
		{"both-set", "outer-ctx", "kube-ctx", "outer-ctx", "kube-ctx"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			boot := newTestRuntimeBoot(t, map[string]string{
				"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev",
				"ERUN_CLOUD_PROVIDER": "aws", "ERUN_CLOUD_PROVIDER_ALIAS": "operator@aws", "ERUN_CLOUD_REGION": "us-east-1",
				"ERUN_CLOUD_CONTEXT_NAME": tc.contextName, "ERUN_KUBERNETES_CONTEXT": tc.kubeContext,
			})
			if err := writeRuntimeBootConfig(core.Context{}, boot, nil); err != nil {
				t.Fatal(err)
			}
			var root configpkg.ERunConfig
			readTestYAML(t, configpkg.RuntimeRootConfigPath(boot.configHome()), &root)
			if len(root.CloudContexts) != 1 || root.CloudContexts[0].Name != tc.wantName || root.CloudContexts[0].KubernetesContext != tc.wantKube {
				t.Fatalf("got %+v, want name %q kubernetescontext %q", root.CloudContexts, tc.wantName, tc.wantKube)
			}
			inspection, err := doctor.InspectRuntimeConfigSync(boot.configHome(), boot.env)
			if err != nil || !inspection.InSync() {
				t.Fatalf("a booted pod should be in sync: %+v %v", inspection.Drift, err)
			}
		})
	}
}

func TestRuntimeBootConfigWritesTheEnvironmentAndKeepsUnknownKeys(t *testing.T) {
	boot := newTestRuntimeBoot(t, map[string]string{
		"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev", "ERUN_ENV_TYPE": "remote-agent",
		"ERUN_RUNTIME_REGISTRY": "10.43.0.10:5000", "ERUN_REPO_PATH": "/home/erun/git/team",
	})
	envPath := configpkg.RuntimeEnvConfigPath(boot.configHome(), "team", "dev")
	writeAssetFile(t, envPath, "name: dev\nsshd:\n  enabled: true\nfuturekey: keep\n")
	if err := writeRuntimeBootConfig(core.Context{}, boot, nil); err != nil {
		t.Fatal(err)
	}
	var env configpkg.EnvConfig
	readTestYAML(t, envPath, &env)
	if env.Type != configpkg.EnvironmentTypeRemoteAgent || env.RuntimeRegistry != "10.43.0.10:5000" || env.LocalRepoPath != "/home/erun/git/team" || env.Idle.Timeout != "5m0s" {
		t.Fatalf("unexpected env config: %+v", env)
	}
	data, _ := os.ReadFile(envPath)
	if !strings.Contains(string(data), "futurekey: keep") {
		t.Fatalf("a key this binary does not know must survive the boot:\n%s", data)
	}
	var tenant configpkg.TenantConfig
	readTestYAML(t, filepath.Join(boot.configHome(), configpkg.ConfigRoot, "team", configpkg.ConfigFile), &tenant)
	if tenant.Name != "team" || tenant.DefaultEnvironment != "dev" {
		t.Fatalf("unexpected tenant config: %+v", tenant)
	}
}

func TestRuntimeBootConfigReplacesAnUnparsableFile(t *testing.T) {
	boot := newTestRuntimeBoot(t, map[string]string{"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev"})
	envPath := configpkg.RuntimeEnvConfigPath(boot.configHome(), "team", "dev")
	writeAssetFile(t, envPath, "name: [unterminated\n")
	if err := writeRuntimeBootConfig(core.Context{}, boot, nil); err != nil {
		t.Fatal(err)
	}
	var env configpkg.EnvConfig
	readTestYAML(t, envPath, &env)
	if env.Name != "dev" {
		t.Fatalf("a corrupt env config should be rebuilt from the injected environment: %+v", env)
	}
}
