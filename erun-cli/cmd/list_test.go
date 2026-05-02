package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

func TestListCommandPrintsDefaultsAndConfiguredTenants(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	stubKubectlContexts(t, []string{"cluster-local"}, "cluster-local")
	configDir, err := common.ERunConfigDir()
	if err != nil {
		t.Fatalf("ERunConfigDir failed: %v", err)
	}

	tenantAPath := filepath.Join(t.TempDir(), "tenant-a")
	tenantBPath := filepath.Join(t.TempDir(), "tenant-b")
	for _, dir := range []string{tenantAPath, tenantBPath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir repo: %v", err)
		}
	}

	requireNoError(t, common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}), "save erun config")
	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", ProjectRoot: tenantAPath, DefaultEnvironment: "local"}), "save tenant-a config")
	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-b", ProjectRoot: tenantBPath, DefaultEnvironment: "dev"}), "save tenant-b config")
	requireNoError(t, common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "local", RepoPath: tenantAPath, KubernetesContext: "cluster-local"}), "save tenant-a local env")
	requireNoError(t, common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "prod", RepoPath: tenantAPath, KubernetesContext: "cluster-prod"}), "save tenant-a prod env")
	requireNoError(t, common.SaveEnvConfig("tenant-b", common.EnvConfig{Name: "dev", RepoPath: tenantBPath, KubernetesContext: "cluster-b"}), "save tenant-b dev env")

	repoRoot := filepath.Join(t.TempDir(), "frs")
	requireNoError(t, os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755), "mkdir .git")
	subDir := filepath.Join(repoRoot, "nested")
	requireNoError(t, os.MkdirAll(subDir, 0o755), "mkdir nested")
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	requireNoError(t, os.Chdir(subDir), "chdir")
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	stdout := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{})
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"list"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	output := stdout.String()
	for _, want := range []string{
		"Configuration:",
		"  directory: " + configDir,
		"Defaults:",
		"  tenant: tenant-a",
		"  environment: local",
		"Current Directory:",
		"  repo: frs",
		"  configured tenant: none",
		"  effective target: tenant-a/local",
		"  kubernetes context: cluster-local",
		"  snapshot: off",
		"  assigned local port range: 17000-17099",
		"  assigned mcp local port: 17000 (when MCP is running or forwarded)",
		"  assigned api local port: 17033 (when API is running or forwarded)",
		"  api url: http://127.0.0.1:17033",
		"  assigned ssh local port: 17022 (when SSH port-forward is active)",
		"Tenants:",
		"  tenant-a [default, effective]",
		"    default environment: local",
		"      - local [default, effective] context=cluster-local snapshot=off repo=" + tenantAPath + " ports=17000-17099 mcp-port=17000 api-port=17033 api-url=http://127.0.0.1:17033 ssh-port=17022",
		"      - prod context=cluster-prod snapshot=off repo=" + tenantAPath + " ports=17100-17199 mcp-port=17100 api-port=17133 api-url=http://127.0.0.1:17133 ssh-port=17122",
		"  tenant-b",
		"      - dev [default] context=cluster-b snapshot=off repo=" + tenantBPath + " ports=17200-17299 mcp-port=17200 api-port=17233 api-url=http://127.0.0.1:17233 ssh-port=17222",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected list output to contain %q, got:\n%s", want, output)
		}
	}
	if strings.Contains(output, "project root:") {
		t.Fatalf("expected list output to omit tenant-level project root, got:\n%s", output)
	}
}

func TestListCommandUsesConfiguredCurrentDirectoryTenantBeforeDefault(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	configDir, err := common.ERunConfigDir()
	if err != nil {
		t.Fatalf("ERunConfigDir failed: %v", err)
	}

	tenantAPath := filepath.Join(t.TempDir(), "tenant-a")
	tenantBPath := filepath.Join(t.TempDir(), "tenant-b")
	for _, dir := range []string{tenantAPath, tenantBPath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir repo: %v", err)
		}
	}

	requireNoError(t, common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}), "save erun config")
	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", ProjectRoot: tenantAPath, DefaultEnvironment: "local"}), "save tenant-a config")
	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-b", ProjectRoot: tenantBPath, DefaultEnvironment: "dev"}), "save tenant-b config")
	requireNoError(t, common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "local", RepoPath: tenantAPath, KubernetesContext: "cluster-local"}), "save tenant-a env")
	requireNoError(t, common.SaveEnvConfig("tenant-b", common.EnvConfig{Name: "dev", RepoPath: tenantBPath, KubernetesContext: "cluster-b"}), "save tenant-b env")

	requireNoError(t, os.MkdirAll(filepath.Join(tenantBPath, ".git"), 0o755), "mkdir .git")
	subDir := filepath.Join(tenantBPath, "nested")
	requireNoError(t, os.MkdirAll(subDir, 0o755), "mkdir nested")
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	requireNoError(t, os.Chdir(subDir), "chdir")
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	stdout := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{})
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"list"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	output := stdout.String()
	for _, want := range []string{
		"Configuration:",
		"  directory: " + configDir,
		"  repo: tenant-b",
		"  configured tenant: tenant-b",
		"  effective target: tenant-b/dev",
		"  kubernetes context: cluster-b",
		"  snapshot: off",
		"  assigned local port range: 17100-17199",
		"  assigned mcp local port: 17100 (when MCP is running or forwarded)",
		"  assigned api local port: 17133 (when API is running or forwarded)",
		"  api url: http://127.0.0.1:17133",
		"  assigned ssh local port: 17122 (when SSH port-forward is active)",
		"  tenant-b [effective]",
		"      - dev [default, effective] context=cluster-b snapshot=off repo=" + tenantBPath + " ports=17100-17199 mcp-port=17100 api-port=17133 api-url=http://127.0.0.1:17133 ssh-port=17122",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected list output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestListCommandPrintsEmptyStateWhenNotInitialized(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	configDir, err := common.ERunConfigDir()
	if err != nil {
		t.Fatalf("ERunConfigDir failed: %v", err)
	}

	repoRoot := filepath.Join(t.TempDir(), "erun")
	requireNoError(t, os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755), "mkdir .git")
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	requireNoError(t, os.Chdir(repoRoot), "chdir")
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	stdout := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{})
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"list"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	output := stdout.String()
	for _, want := range []string{
		"Configuration:",
		"  directory: " + configDir,
		"Defaults:",
		"  tenant: none",
		"  environment: none",
		"Current Directory:",
		"  repo: erun",
		"  configured tenant: none",
		"  effective target: unavailable (default tenant is not configured)",
		"Tenants:",
		"  none",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected list output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestListCommandPrintsSnapshotPreference(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	stubKubectlContexts(t, []string{"cluster-local"}, "cluster-local")

	repoRoot := filepath.Join(t.TempDir(), "tenant-a")
	requireNoError(t, os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755), "mkdir .git")
	requireNoError(t, common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}), "save erun config")
	snapshot := false
	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", ProjectRoot: repoRoot, DefaultEnvironment: "local"}), "save tenant config")
	requireNoError(t, common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "local", RepoPath: repoRoot, KubernetesContext: "cluster-local", Snapshot: &snapshot}), "save env config")

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	requireNoError(t, os.Chdir(repoRoot), "chdir")
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	stdout := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{})
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"list"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	output := stdout.String()
	for _, want := range []string{
		"  snapshot: off",
		"  assigned local port range: 17000-17099",
		"  assigned mcp local port: 17000 (when MCP is running or forwarded)",
		"  assigned api local port: 17033 (when API is running or forwarded)",
		"  api url: http://127.0.0.1:17033",
		"  assigned ssh local port: 17022 (when SSH port-forward is active)",
		"      - local [default, effective] context=cluster-local snapshot=off repo=" + repoRoot + " ports=17000-17099 mcp-port=17000 api-port=17033 api-url=http://127.0.0.1:17033 ssh-port=17022",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected list output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestListCommandPrintsSSHDConfiguration(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	repoRoot := filepath.Join(t.TempDir(), "tenant-a")
	requireNoError(t, os.MkdirAll(repoRoot, 0o755), "mkdir repo root")
	requireNoError(t, common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}), "save erun config")
	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", ProjectRoot: repoRoot, DefaultEnvironment: "dev"}), "save tenant config")
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "dev",
		RepoPath:          repoRoot,
		KubernetesContext: "cluster-dev",
		Remote:            true,
		SSHD: common.SSHDConfig{
			Enabled:       true,
			LocalPort:     common.DefaultSSHLocalPort,
			PublicKeyPath: "/tmp/id_ed25519.pub",
		},
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	stdout := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{})
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"list"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	output := stdout.String()
	for _, want := range []string{
		"  assigned local port range: 17000-17099",
		"  assigned mcp local port: 17000 (when MCP is running or forwarded)",
		"  assigned api local port: 17033 (when API is running or forwarded)",
		"  api url: http://127.0.0.1:17033",
		"  assigned ssh local port: 17022 (when SSH port-forward is active)",
		"  sshd: on",
		"  ssh host: erun-tenant-a-dev",
		"  ssh user: erun",
		"  ssh workspace: /home/erun/git/tenant-a",
		"ports=17000-17099 mcp-port=17000 api-port=17033 api-url=http://127.0.0.1:17033 ssh-port=17022 ssh=on host=erun-tenant-a-dev user=erun local-port=17022 workspace=/home/erun/git/tenant-a",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected list output to contain %q, got:\n%s", want, output)
		}
	}
}
