package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestList(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"list", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/help", normalize.Apply(result.Combined))
	})

	t.Run("empty_config", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "list/empty_config", normalize.Apply(result.Combined))
	})

	t.Run("with_seeded_tenant_env", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/with_seeded_tenant_env", normalize.Apply(result.Combined))
	})

	t.Run("multi_tenant_with_multiple_envs", func(t *testing.T) {
		// Exercises cmd/list.go and erun-common/list.go: with two tenants
		// (each with multiple envs), the listing must show every tenant,
		// every env, and assign distinct port ranges by tenant index.
		setup := env.New(t)
		seedListMultiTenant(t, setup)
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/multi_tenant_with_multiple_envs", normalize.Apply(result.Combined))
	})

	t.Run("cwd_overrides_default_tenant", func(t *testing.T) {
		// Exercises cmd/list.go writeListCurrentDirectorySection: when the
		// current working directory sits under a non-default tenant's
		// project root, the listing must show that tenant as `effective`
		// even though tenant-a remains the configured default.
		setup := env.New(t)
		seedListMultiTenant(t, setup)
		// SeedListMultiTenant places tenant-b's project root at
		// $HOME/git/tenant-b. Make it a real git repo so cwd-resolution
		// finds it.
		tenantBPath := filepath.Join(setup.Home, "git", "tenant-b")
		fixture.SeedGitRepo(t, tenantBPath)
		nested := filepath.Join(tenantBPath, "nested")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("mkdir nested: %v", err)
		}
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: nested, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/cwd_overrides_default_tenant", normalize.Apply(result.Combined))
	})

	t.Run("sshd_configured", func(t *testing.T) {
		// Exercises cmd/list.go writeEffectiveOpenSSH and
		// environmentSSHFields: a tenant with sshd.enabled=true must surface
		// the SSH host/user/workspace lines and the per-env ssh fields.
		setup := env.New(t)
		seedListSSHDTenant(t, setup)
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/sshd_configured", normalize.Apply(result.Combined))
	})

	t.Run("with_claude_config", func(t *testing.T) {
		// Exercises cmd/list.go claudeLabel + optionalBoolLabel and
		// erun-common claude helpers (EnvironmentClaudeConfig.IsZero,
		// NormalizedModels) via an env config with a populated claude:
		// block.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envCfg := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		mustWrite(t, envCfg,
			"name: dev\n"+
				"repopath: "+setup.Cwd+"\n"+
				"kubernetescontext: test-context\n"+
				"containerregistry: registry.example/test\n"+
				"runtimeversion: 1.0.0\n"+
				"claude:\n"+
				"  usemantle: true\n"+
				"  usebedrock: true\n"+
				"  models: [opus, sonnet, haiku]\n"+
				"  maxoutputtokens: 16384\n",
		)
		result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "list/with_claude_config", normalize.Apply(result.Combined))
	})
}

func seedListMultiTenant(t testing.TB, setup env.Setup) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: tenant-a\n")
	tenantAPath := filepath.Join(setup.Home, "git", "tenant-a")
	tenantBPath := filepath.Join(setup.Home, "git", "tenant-b")
	for _, dir := range []string{tenantAPath, tenantBPath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir repo %s: %v", dir, err)
		}
	}
	writeListTenantConfig(t, root, "tenant-a", "local", tenantAPath)
	writeListTenantConfig(t, root, "tenant-b", "dev", tenantBPath)
	writeListEnvConfig(t, root, "tenant-a", "local", tenantAPath, "cluster-local", "")
	writeListEnvConfig(t, root, "tenant-a", "prod", tenantAPath, "cluster-prod", "")
	writeListEnvConfig(t, root, "tenant-b", "dev", tenantBPath, "cluster-b", "")
}

func seedListSSHDTenant(t testing.TB, setup env.Setup) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: tenant-a\n")
	tenantPath := filepath.Join(setup.Home, "git", "tenant-a")
	if err := os.MkdirAll(tenantPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	writeListTenantConfig(t, root, "tenant-a", "dev", tenantPath)
	envDir := filepath.Join(root, "tenant-a", "dev")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", envDir, err)
	}
	mustWrite(t, filepath.Join(root, "tenant-a", "dev", "config.yaml"),
		"name: dev\n"+
			"repopath: "+tenantPath+"\n"+
			"kubernetescontext: cluster-dev\n"+
			"remote: true\n"+
			"sshd:\n"+
			"  enabled: true\n"+
			"  localport: 17022\n"+
			"  publickeypath: /tmp/id_ed25519.pub\n",
	)
}

func writeListTenantConfig(t testing.TB, root, tenant, defaultEnv, projectRoot string) {
	t.Helper()
	tenantDir := filepath.Join(root, tenant)
	if err := os.MkdirAll(tenantDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", tenantDir, err)
	}
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"name: "+tenant+"\n"+
			"projectroot: "+projectRoot+"\n"+
			"defaultenvironment: "+defaultEnv+"\n",
	)
}

func writeListEnvConfig(t testing.TB, root, tenant, environment, repoPath, kubeContext, registry string) {
	t.Helper()
	envDir := filepath.Join(root, tenant, environment)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", envDir, err)
	}
	body := "name: " + environment + "\n" +
		"repopath: " + repoPath + "\n" +
		"kubernetescontext: " + kubeContext + "\n"
	if registry != "" {
		body += "containerregistry: " + registry + "\n"
	}
	mustWrite(t, filepath.Join(envDir, "config.yaml"), body)
}

func mustWrite(t testing.TB, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
