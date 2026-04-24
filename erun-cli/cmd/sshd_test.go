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
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

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
		writeLocalSSHConfig,
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
	sshConfigData, err := os.ReadFile(filepath.Join(homeDir, ".ssh", "config"))
	if err != nil {
		t.Fatalf("read ssh config: %v", err)
	}
	for _, want := range []string{
		"Host erun-tenant-a-dev",
		"HostName 127.0.0.1",
		"Port 64022",
		"User erun",
		"HostKeyAlias erun-tenant-a-dev",
		"IdentityFile " + strings.TrimSuffix(publicKeyPath, ".pub"),
	} {
		if !strings.Contains(string(sshConfigData), want) {
			t.Fatalf("expected ssh config to contain %q, got:\n%s", want, sshConfigData)
		}
	}
	stdout := ctx.Stdout.(*bytes.Buffer).String()
	for _, want := range []string{
		"host: erun-tenant-a-dev",
		"config: " + filepath.Join(homeDir, ".ssh", "config"),
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected stdout to contain %q, got:\n%s", want, stdout)
		}
	}
}

func TestRunSSHDInitCommandUsesResolvedEnvironmentLocalPortByDefault(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	publicKeyPath := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(publicKeyPath, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}

	var savedEnv common.EnvConfig
	err := runSSHDInitCommand(
		common.Context{
			Logger: common.NewLoggerWithWriters(1, new(bytes.Buffer), new(bytes.Buffer)),
			Stdout: new(bytes.Buffer),
			Stderr: new(bytes.Buffer),
		},
		common.OpenResult{
			Tenant:      "tenant-a",
			Environment: "prod",
			RepoPath:    "/home/erun/git/tenant-a",
			TenantConfig: common.TenantConfig{
				Name:   "tenant-a",
				Remote: true,
			},
			EnvConfig: common.EnvConfig{
				Name:              "prod",
				RepoPath:          "/home/erun/git/tenant-a",
				KubernetesContext: "cluster-prod",
				Remote:            true,
			},
			LocalPorts: common.EnvironmentLocalPorts{
				RangeStart: 17100,
				RangeEnd:   17199,
				MCP:        17100,
				SSH:        17122,
			},
		},
		publicKeyPath,
		0,
		func(_ string, config common.EnvConfig) error {
			savedEnv = config
			return nil
		},
		func(target common.OpenResult) (common.DeploySpec, error) {
			return common.DeploySpec{
				Target: target,
				Deploy: common.HelmDeploySpec{
					ReleaseName:       common.RuntimeReleaseName(target.Tenant),
					ChartPath:         "/tmp/chart",
					ValuesFilePath:    "/tmp/chart/values.prod.yaml",
					Tenant:            target.Tenant,
					Environment:       target.Environment,
					Namespace:         common.KubernetesNamespaceName(target.Tenant, target.Environment),
					KubernetesContext: target.EnvConfig.KubernetesContext,
					SSHDEnabled:       target.EnvConfig.SSHD.Enabled,
					Timeout:           common.DefaultHelmDeploymentTimeout,
				},
			}, nil
		},
		func(common.HelmDeployParams) error { return nil },
		func(_ common.ShellLaunchParams, _ string) (common.RemoteCommandResult, error) {
			return common.RemoteCommandResult{}, nil
		},
		func(common.OpenResult) (SSHDLocalConfigResult, error) {
			return SSHDLocalConfigResult{}, nil
		},
	)
	if err != nil {
		t.Fatalf("runSSHDInitCommand failed: %v", err)
	}
	if savedEnv.SSHD.LocalPort != 17122 {
		t.Fatalf("expected resolved environment SSH port, got %+v", savedEnv.SSHD)
	}
}
