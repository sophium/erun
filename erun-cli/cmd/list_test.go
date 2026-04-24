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

	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", ProjectRoot: tenantAPath, DefaultEnvironment: "local"}); err != nil {
		t.Fatalf("save tenant-a config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{Name: "tenant-b", ProjectRoot: tenantBPath, DefaultEnvironment: "dev"}); err != nil {
		t.Fatalf("save tenant-b config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "local", RepoPath: tenantAPath, KubernetesContext: "cluster-local"}); err != nil {
		t.Fatalf("save tenant-a local env: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "prod", RepoPath: tenantAPath, KubernetesContext: "cluster-prod"}); err != nil {
		t.Fatalf("save tenant-a prod env: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-b", common.EnvConfig{Name: "dev", RepoPath: tenantBPath, KubernetesContext: "cluster-b"}); err != nil {
		t.Fatalf("save tenant-b dev env: %v", err)
	}

	repoRoot := filepath.Join(t.TempDir(), "frs")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	subDir := filepath.Join(repoRoot, "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	stdout := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{})
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

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
		"  snapshot: on",
		"  assigned local port range: 17000-17099",
		"  assigned mcp local port: 17000 (when MCP is running or forwarded)",
		"  assigned ssh local port: 17022 (when SSH port-forward is active)",
		"Tenants:",
		"  tenant-a [default, effective]",
		"    default environment: local",
		"      - local [default, effective] context=cluster-local snapshot=on repo=" + tenantAPath + " ports=17000-17099 mcp-port=17000 ssh-port=17022",
		"      - prod context=cluster-prod snapshot=on repo=" + tenantAPath + " ports=17100-17199 mcp-port=17100 ssh-port=17122",
		"  tenant-b",
		"      - dev [default] context=cluster-b snapshot=on repo=" + tenantBPath + " ports=17200-17299 mcp-port=17200 ssh-port=17222",
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

	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", ProjectRoot: tenantAPath, DefaultEnvironment: "local"}); err != nil {
		t.Fatalf("save tenant-a config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{Name: "tenant-b", ProjectRoot: tenantBPath, DefaultEnvironment: "dev"}); err != nil {
		t.Fatalf("save tenant-b config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "local", RepoPath: tenantAPath, KubernetesContext: "cluster-local"}); err != nil {
		t.Fatalf("save tenant-a env: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-b", common.EnvConfig{Name: "dev", RepoPath: tenantBPath, KubernetesContext: "cluster-b"}); err != nil {
		t.Fatalf("save tenant-b env: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(tenantBPath, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	subDir := filepath.Join(tenantBPath, "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	stdout := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{})
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"Configuration:",
		"  directory: " + configDir,
		"  repo: tenant-b",
		"  configured tenant: tenant-b",
		"  effective target: tenant-b/dev",
		"  kubernetes context: cluster-b",
		"  snapshot: on",
		"  assigned local port range: 17100-17199",
		"  assigned mcp local port: 17100 (when MCP is running or forwarded)",
		"  assigned ssh local port: 17122 (when SSH port-forward is active)",
		"  tenant-b [effective]",
		"      - dev [default, effective] context=cluster-b snapshot=on repo=" + tenantBPath + " ports=17100-17199 mcp-port=17100 ssh-port=17122",
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
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	stdout := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{})
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

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
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	snapshot := false
	if err := common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", ProjectRoot: repoRoot, DefaultEnvironment: "local"}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "local", RepoPath: repoRoot, KubernetesContext: "cluster-local", Snapshot: &snapshot}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	stdout := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{})
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"  snapshot: off",
		"  assigned local port range: 17000-17099",
		"  assigned mcp local port: 17000 (when MCP is running or forwarded)",
		"  assigned ssh local port: 17022 (when SSH port-forward is active)",
		"      - local [default, effective] context=cluster-local snapshot=off repo=" + repoRoot + " ports=17000-17099 mcp-port=17000 ssh-port=17022",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected list output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestListCommandPrintsSSHDConfiguration(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	repoRoot := filepath.Join(t.TempDir(), "tenant-a")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", ProjectRoot: repoRoot, DefaultEnvironment: "dev", Remote: true}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
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

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"  assigned local port range: 17000-17099",
		"  assigned mcp local port: 17000 (when MCP is running or forwarded)",
		"  assigned ssh local port: 17022 (when SSH port-forward is active)",
		"  sshd: on",
		"  ssh host: erun-tenant-a-dev",
		"  ssh user: erun",
		"  ssh workspace: /home/erun/git/tenant-a",
		"ports=17000-17099 mcp-port=17000 ssh-port=17022 ssh=on host=erun-tenant-a-dev user=erun local-port=17022 workspace=/home/erun/git/tenant-a",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected list output to contain %q, got:\n%s", want, output)
		}
	}
}
