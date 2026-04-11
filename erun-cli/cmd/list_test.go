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
		"Defaults:",
		"  tenant: tenant-a",
		"  environment: local",
		"Current Directory:",
		"  repo: frs",
		"  configured tenant: none",
		"  effective target: tenant-a/local",
		"  kubernetes context: cluster-local",
		"Tenants:",
		"  tenant-a [default, effective]",
		"    default environment: local",
		"      - local [default, effective] context=cluster-local repo=" + tenantAPath,
		"      - prod context=cluster-prod repo=" + tenantAPath,
		"  tenant-b",
		"      - dev [default] context=cluster-b repo=" + tenantBPath,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected list output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestListCommandUsesConfiguredCurrentDirectoryTenantBeforeDefault(t *testing.T) {
	setupRootCmdTestConfigHome(t)

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
		"  repo: tenant-b",
		"  configured tenant: tenant-b",
		"  effective target: tenant-b/dev",
		"  kubernetes context: cluster-b",
		"  tenant-b [effective]",
		"      - dev [default, effective] context=cluster-b repo=" + tenantBPath,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected list output to contain %q, got:\n%s", want, output)
		}
	}
}
