package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testListSSHDEnabledButHostAliasMissing(t *testing.T) {
	// sshd is enabled but no Host block exists in ~/.ssh/config, so the alias
	// must not be reported as usable.
	setup := env.New(t)
	seedListSSHDTenant(t, setup)
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/sshd_enabled_but_host_alias_missing", normalize.Apply(result.Combined))
}

func testListSSHDEnabledAndHostAliasConfigured(t *testing.T) {
	setup := env.New(t)
	seedListSSHDTenant(t, setup)
	sshDir := filepath.Join(setup.Home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", sshDir, err)
	}
	mustWrite(t, filepath.Join(sshDir, "config"),
		"Host erun-tenant-a-dev\n"+
			"  HostName 127.0.0.1\n"+
			"  Port 17022\n"+
			"  User erun\n",
	)
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/sshd_enabled_and_host_alias_configured", normalize.Apply(result.Combined))
}

func testListOrphanedHostAliasBlocksAreReported(t *testing.T) {
	// Stale Host blocks persist, and reissued local ports can make them resolve
	// into the wrong live environment. Only erun-written aliases are reported.
	setup := env.New(t)
	seedListSSHDTenant(t, setup)
	sshDir := filepath.Join(setup.Home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", sshDir, err)
	}
	mustWrite(t, filepath.Join(sshDir, "config"),
		"Host erun-tenant-a-dev\n"+
			"  HostName 127.0.0.1\n"+
			"  Port 17022\n"+
			"  User erun\n"+
			"  HostKeyAlias erun-tenant-a-dev\n"+
			"\n"+
			"Host erun-tenant-a-old\n"+
			"  HostName 127.0.0.1\n"+
			"  Port 17022\n"+
			"  User erun\n"+
			"  HostKeyAlias erun-tenant-a-old\n"+
			"\n"+
			"Host erun-erun-local\n"+
			"  HostName 127.0.0.1\n"+
			"  Port 17099\n"+
			"  User erun\n"+
			"\n"+
			"Host myserver\n"+
			"  HostName 10.0.0.5\n"+
			"  Port 22\n"+
			"  User op\n",
	)
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/orphaned_host_alias_blocks_are_reported", normalize.Apply(result.Combined))
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
			"type: remote-agent\n"+
			"sshd:\n"+
			"  enabled: true\n"+
			"  localport: 17022\n"+
			"  publickeypath: /tmp/id_ed25519.pub\n",
	)
}
