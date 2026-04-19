package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

func TestRunSSHDInitCommandPersistsConfigAndDeploysRuntime(t *testing.T) {
	publicKeyPath := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(publicKeyPath, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}

	var savedTenant string
	var savedEnv common.EnvConfig
	var deployed common.HelmDeployParams
	var remoteScript string
	ctx := common.Context{
		Logger: common.NewLoggerWithWriters(1, new(bytes.Buffer), new(bytes.Buffer)),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	err := runSSHDInitCommand(
		ctx,
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "dev",
			RepoPath:    "/home/erun/git/tenant-a",
			TenantConfig: common.TenantConfig{
				Name:   "tenant-a",
				Remote: true,
			},
			EnvConfig: common.EnvConfig{
				Name:              "dev",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-dev",
				Remote:            true,
			},
		},
		publicKeyPath,
		64022,
		func(tenant string, config common.EnvConfig) error {
			savedTenant = tenant
			savedEnv = config
			return nil
		},
		func(target common.OpenResult) (common.DeploySpec, error) {
			return common.DeploySpec{
				Target: target,
				Deploy: common.HelmDeploySpec{
					ReleaseName:       common.RuntimeReleaseName(target.Tenant),
					ChartPath:         "/tmp/chart",
					ValuesFilePath:    "/tmp/chart/values.dev.yaml",
					Tenant:            target.Tenant,
					Environment:       target.Environment,
					Namespace:         common.KubernetesNamespaceName(target.Tenant, target.Environment),
					KubernetesContext: target.EnvConfig.KubernetesContext,
					WorktreeStorage:   common.WorktreeStoragePVC,
					WorktreeRepoName:  "tenant-a",
					WorktreeHostPath:  "/tmp/ignored",
					SSHDEnabled:       target.EnvConfig.SSHD.Enabled,
					Timeout:           common.DefaultHelmDeploymentTimeout,
				},
			}, nil
		},
		func(params common.HelmDeployParams) error {
			deployed = params
			return nil
		},
		func(_ common.ShellLaunchParams, script string) (common.RemoteCommandResult, error) {
			remoteScript = script
			return common.RemoteCommandResult{}, nil
		},
	)
	if err != nil {
		t.Fatalf("runSSHDInitCommand failed: %v", err)
	}

	if savedTenant != "tenant-a" {
		t.Fatalf("unexpected saved tenant: %q", savedTenant)
	}
	if !savedEnv.SSHD.Enabled || savedEnv.SSHD.LocalPort != 64022 || savedEnv.SSHD.PublicKeyPath != publicKeyPath {
		t.Fatalf("unexpected saved env config: %+v", savedEnv)
	}
	if !deployed.SSHDEnabled {
		t.Fatalf("expected deployment params to enable SSHD, got %+v", deployed)
	}
	if !strings.Contains(remoteScript, "authorized_keys") || !strings.Contains(remoteScript, "ssh-ed25519 AAAATEST user@example") {
		t.Fatalf("unexpected remote authorized_keys script:\n%s", remoteScript)
	}
}
