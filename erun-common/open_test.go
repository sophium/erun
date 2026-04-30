package eruncommon

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenResolveUsesDefaultTenantAndEnvironment(t *testing.T) {
	repoPath := t.TempDir()
	store := openStore{
		toolConfig: ERunConfig{DefaultTenant: "tenant-a"},
		tenantConfigs: map[string]TenantConfig{
			"tenant-a": {
				Name:               "tenant-a",
				ProjectRoot:        filepath.Join(t.TempDir(), "fallback"),
				DefaultEnvironment: "dev",
			},
		},
		envConfigs: map[string]EnvConfig{
			"tenant-a/dev": {
				Name:              "dev",
				RepoPath:          repoPath,
				KubernetesContext: "cluster-dev",
			},
		},
	}

	result, err := ResolveOpen(store, OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result.Tenant != "tenant-a" || result.Environment != "dev" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.RepoPath != repoPath || result.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell target: %+v", result)
	}
	if result.LocalPorts.RangeStart != 17000 || result.LocalPorts.SSH != 17022 {
		t.Fatalf("unexpected local ports: %+v", result.LocalPorts)
	}
}

func TestOpenResolveAllowsRemoteRepoPathWithoutLocalCheckout(t *testing.T) {
	store := openStore{
		toolConfig: ERunConfig{DefaultTenant: "frs"},
		tenantConfigs: map[string]TenantConfig{
			"frs": {
				Name:               "frs",
				ProjectRoot:        "/home/erun/git/frs",
				DefaultEnvironment: "dev",
			},
		},
		envConfigs: map[string]EnvConfig{
			"frs/dev": {
				Name:              "dev",
				RepoPath:          "/home/erun/git/frs",
				KubernetesContext: "cluster-dev",
				Remote:            true,
			},
		},
	}

	result, err := ResolveOpen(store, OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if !result.RemoteRepo() {
		t.Fatalf("expected remote repo result, got %+v", result)
	}
	if result.RepoPath != "/home/erun/git/frs" {
		t.Fatalf("unexpected repo path: %+v", result)
	}
}

func TestOpenResolveIgnoresLegacyTenantRemoteFlagForLocalEnvironment(t *testing.T) {
	repoPath := t.TempDir()
	store := openStore{
		toolConfig: ERunConfig{DefaultTenant: "frs"},
		tenantConfigs: map[string]TenantConfig{
			"frs": {
				Name:               "frs",
				ProjectRoot:        repoPath,
				DefaultEnvironment: "local",
				Remote:             true,
			},
		},
		envConfigs: map[string]EnvConfig{
			"frs/local": {
				Name:              "local",
				RepoPath:          repoPath,
				KubernetesContext: "cluster-local",
			},
		},
	}

	result, err := ResolveOpen(store, OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result.RemoteRepo() {
		t.Fatalf("expected env-level remote setting to control remote repo behavior, got %+v", result)
	}
}

func TestOpenResolveUsesCurrentDirectoryTenantBeforeDefault(t *testing.T) {
	restoreWorkingDirAfterTest(t)

	repoRoot := filepath.Join(t.TempDir(), "tenant-a")
	subDir := filepath.Join(repoRoot, "nested")
	defaultRepo := filepath.Join(t.TempDir(), "tenant-b")
	mkdirAllForTest(t, filepath.Join(repoRoot, ".git"), subDir, defaultRepo)
	requireNoError(t, os.Chdir(subDir), "chdir")

	store := openStore{
		toolConfig: ERunConfig{DefaultTenant: "tenant-b"},
		tenantConfigs: map[string]TenantConfig{
			"tenant-a": {Name: "tenant-a", ProjectRoot: repoRoot, DefaultEnvironment: "dev"},
			"tenant-b": {Name: "tenant-b", ProjectRoot: defaultRepo, DefaultEnvironment: "prod"},
		},
		envConfigs: map[string]EnvConfig{
			"tenant-a/dev":  {Name: "dev", RepoPath: repoRoot, KubernetesContext: "cluster-a"},
			"tenant-b/prod": {Name: "prod", RepoPath: defaultRepo, KubernetesContext: "cluster-b"},
		},
	}

	result, err := ResolveOpen(store, OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	requireNoError(t, err, "Resolve failed")
	requireCondition(t, result.Tenant == "tenant-a" && result.Environment == "dev", "expected current directory tenant to win, got %+v", result)
	requireCondition(t, result.LocalPorts.RangeStart == 17000 && result.LocalPorts.SSH == 17022, "unexpected local ports: %+v", result.LocalPorts)
}

func TestOpenResolveAssignsEnvironmentLocalPortsFromSortedTenantEnvironmentOrder(t *testing.T) {
	repoA := t.TempDir()
	repoB := t.TempDir()
	store := openStore{
		tenantConfigs: map[string]TenantConfig{
			"tenant-b": {Name: "tenant-b", ProjectRoot: repoB, DefaultEnvironment: "stage"},
			"tenant-a": {Name: "tenant-a", ProjectRoot: repoA, DefaultEnvironment: "dev"},
		},
		envConfigs: map[string]EnvConfig{
			"tenant-a/dev":   {Name: "dev", RepoPath: repoA, KubernetesContext: "cluster-a"},
			"tenant-a/prod":  {Name: "prod", RepoPath: repoA, KubernetesContext: "cluster-a"},
			"tenant-b/stage": {Name: "stage", RepoPath: repoB, KubernetesContext: "cluster-b"},
		},
	}

	result, err := ResolveOpen(store, OpenParams{Tenant: "tenant-b", Environment: "stage"})
	if err != nil {
		t.Fatalf("ResolveOpen failed: %v", err)
	}
	if result.LocalPorts.RangeStart != 17200 || result.LocalPorts.MCP != 17200 || result.LocalPorts.SSH != 17222 {
		t.Fatalf("unexpected local ports: %+v", result.LocalPorts)
	}
}

func TestOpenResolveFallsBackToDefaultTenantWhenCurrentDirectoryIsNotConfiguredTenant(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("return to original dir: %v", err)
		}
	})

	repoRoot := filepath.Join(t.TempDir(), "frs")
	subDir := filepath.Join(repoRoot, "nested")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	defaultRepo := filepath.Join(t.TempDir(), "tenant-a")
	if err := os.MkdirAll(defaultRepo, 0o755); err != nil {
		t.Fatalf("mkdir default repo: %v", err)
	}
	store := openStore{
		toolConfig: ERunConfig{DefaultTenant: "tenant-a"},
		tenantConfigs: map[string]TenantConfig{
			"tenant-a": {Name: "tenant-a", ProjectRoot: defaultRepo, DefaultEnvironment: "dev"},
		},
		envConfigs: map[string]EnvConfig{
			"tenant-a/dev": {Name: "dev", RepoPath: defaultRepo, KubernetesContext: "cluster-dev"},
		},
	}

	result, err := ResolveOpen(store, OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result.Tenant != "tenant-a" || result.Environment != "dev" {
		t.Fatalf("expected default tenant fallback, got %+v", result)
	}
}

func TestOpenResolveFallsBackToTenantProjectRoot(t *testing.T) {
	repoPath := t.TempDir()
	store := openStore{
		tenantConfigs: map[string]TenantConfig{
			"tenant-a": {
				Name:               "tenant-a",
				ProjectRoot:        repoPath,
				DefaultEnvironment: "dev",
			},
		},
		envConfigs: map[string]EnvConfig{
			"tenant-a/dev": {Name: "dev", KubernetesContext: "cluster-dev"},
		},
	}

	result, err := ResolveOpen(store, OpenParams{
		Tenant:      "tenant-a",
		Environment: "dev",
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result.RepoPath != repoPath {
		t.Fatalf("expected tenant project root fallback, got %+v", result)
	}
}

func TestInitParamsForOpenTargetUsesExplicitTenantAndDefaultEnvironment(t *testing.T) {
	params, err := InitParamsForOpenTarget(openStore{}, OpenParams{
		Tenant:                "tenant-a",
		UseDefaultEnvironment: true,
	})
	if err != nil {
		t.Fatalf("InitParamsForOpenTarget failed: %v", err)
	}
	if params.Tenant != "tenant-a" || params.Environment != "" || params.ResolveTenant {
		t.Fatalf("unexpected init params: %+v", params)
	}
}

func TestInitParamsForOpenTargetUsesDefaultTenantForExplicitEnvironment(t *testing.T) {
	params, err := InitParamsForOpenTarget(openStore{
		toolConfig: ERunConfig{DefaultTenant: "tenant-a"},
	}, OpenParams{
		Environment:      "dev",
		UseDefaultTenant: true,
	})
	if err != nil {
		t.Fatalf("InitParamsForOpenTarget failed: %v", err)
	}
	if params.Tenant != "tenant-a" || params.Environment != "dev" || params.ResolveTenant {
		t.Fatalf("unexpected init params: %+v", params)
	}
}

func TestOpenResolveRequiresDefaultTenant(t *testing.T) {
	if _, err := ResolveOpen(openStore{loadERunErr: ErrNotInitialized}, OpenParams{UseDefaultTenant: true}); !errors.Is(err, ErrDefaultTenantNotConfigured) {
		t.Fatalf("expected ErrDefaultTenantNotConfigured, got %v", err)
	}
}

func TestOpenResolveRequiresDefaultEnvironment(t *testing.T) {
	store := openStore{
		toolConfig: ERunConfig{DefaultTenant: "tenant-a"},
		tenantConfigs: map[string]TenantConfig{
			"tenant-a": {Name: "tenant-a"},
		},
	}

	if _, err := ResolveOpen(store, OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	}); !errors.Is(err, ErrDefaultEnvironmentNotConfigured) {
		t.Fatalf("expected ErrDefaultEnvironmentNotConfigured, got %v", err)
	}
}

func TestOpenResolveReportsMissingTenant(t *testing.T) {
	_, err := ResolveOpen(openStore{}, OpenParams{
		Tenant:      "dog",
		Environment: "me",
	})
	if !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "no such tenant exists") {
		t.Fatalf("expected missing tenant message, got %q", got)
	}
}

func TestOpenResolveRequiresKubernetesContextAssociation(t *testing.T) {
	repoPath := t.TempDir()
	store := openStore{
		tenantConfigs: map[string]TenantConfig{
			"tenant-a": {
				Name:               "tenant-a",
				ProjectRoot:        repoPath,
				DefaultEnvironment: "dev",
			},
		},
		envConfigs: map[string]EnvConfig{
			"tenant-a/dev": {
				Name:     "dev",
				RepoPath: repoPath,
			},
		},
	}

	_, err := ResolveOpen(store, OpenParams{
		Tenant:      "tenant-a",
		Environment: "dev",
	})
	if !errors.Is(err, ErrKubernetesContextNotConfigured) {
		t.Fatalf("expected ErrKubernetesContextNotConfigured, got %v", err)
	}
}

func TestResolveEffectiveKubernetesContextFallsBackToCurrentContextForLocalEnvironment(t *testing.T) {
	got := resolveEffectiveKubernetesContext(
		DefaultEnvironment,
		"rancher-desktop",
		func() ([]string, error) {
			return []string{"docker-desktop"}, nil
		},
		func() (string, error) {
			return "docker-desktop", nil
		},
	)
	if got != "docker-desktop" {
		t.Fatalf("expected current context fallback, got %q", got)
	}
}

func TestResolveEffectiveKubernetesContextKeepsConfiguredContextOutsideLocalEnvironment(t *testing.T) {
	listCalled := false
	currentCalled := false

	got := resolveEffectiveKubernetesContext(
		"dev",
		"cluster-dev",
		func() ([]string, error) {
			listCalled = true
			return []string{"other-context"}, nil
		},
		func() (string, error) {
			currentCalled = true
			return "other-context", nil
		},
	)
	if got != "cluster-dev" {
		t.Fatalf("expected configured context to be preserved, got %q", got)
	}
	if listCalled || currentCalled {
		t.Fatalf("did not expect kubectl lookup for non-local environment")
	}
}

func TestOpenResolveUsesEffectiveKubernetesContextFromStore(t *testing.T) {
	repoPath := t.TempDir()
	store := openStore{
		tenantConfigs: map[string]TenantConfig{
			"tenant-a": {
				Name:               "tenant-a",
				ProjectRoot:        repoPath,
				DefaultEnvironment: DefaultEnvironment,
			},
		},
		envConfigs: map[string]EnvConfig{
			"tenant-a/local": {
				Name:              DefaultEnvironment,
				RepoPath:          repoPath,
				KubernetesContext: "rancher-desktop",
			},
		},
		resolveEffectiveKubernetesContext: func(environment, configured string) string {
			if environment != DefaultEnvironment || configured != "rancher-desktop" {
				t.Fatalf("unexpected resolver inputs: environment=%q configured=%q", environment, configured)
			}
			return "docker-desktop"
		},
	}

	result, err := ResolveOpen(store, OpenParams{
		Tenant:      "tenant-a",
		Environment: DefaultEnvironment,
	})
	if err != nil {
		t.Fatalf("ResolveOpen failed: %v", err)
	}
	if result.EnvConfig.KubernetesContext != "docker-desktop" {
		t.Fatalf("expected effective context override, got %+v", result.EnvConfig)
	}
}

func TestOpenRunLaunchesShell(t *testing.T) {
	repoPath := t.TempDir()
	store := openStore{
		tenantConfigs: map[string]TenantConfig{
			"tenant-a": {
				Name:               "tenant-a",
				ProjectRoot:        repoPath,
				DefaultEnvironment: "dev",
			},
		},
		envConfigs: map[string]EnvConfig{
			"tenant-a/dev": {
				Name:              "dev",
				RepoPath:          repoPath,
				KubernetesContext: "cluster-dev",
			},
		},
	}

	result, err := ResolveOpen(store, OpenParams{Tenant: "tenant-a", Environment: "dev"})
	if err != nil {
		t.Fatalf("ResolveOpen failed: %v", err)
	}
	launched := ShellLaunchParamsFromResult(result)
	if launched.Dir != repoPath || launched.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if launched.Tenant != "tenant-a" || launched.Environment != "dev" {
		t.Fatalf("unexpected shell launch request identity: %+v", launched)
	}
	if launched.Namespace != KubernetesNamespaceName("tenant-a", "dev") || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected remote shell target: %+v", launched)
	}
}

func TestRemoteShellScriptSeedsConfigsAndCloneCommand(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	repoDir := createGitRepoWithRemote(t, "git@github.com:sophium/erun.git")

	script, err := buildRemoteShellScript(ShellLaunchParams{
		Dir:               repoDir,
		Tenant:            "tenant-a",
		Environment:       "local",
		Title:             "tenant-a-local",
		KubernetesContext: "in-cluster",
	}, false)
	requireNoError(t, err, "buildRemoteShellScript failed")

	remoteWorkdir := "/home/erun/git/erun"

	requireRemoteShellScriptPatterns(t, script, remoteWorkdir)
	requireCondition(t, !strings.Contains(script, repoDir), "did not expect host repo path %q in remote bootstrap script, got:\n%s", repoDir, script)
	requireCondition(t, !strings.Contains(script, "/etc/bash.bashrc"), "did not expect remote rcfile to source system bashrc twice, got:\n%s", script)
	requireRemoteShellSeededConfigs(t, script, remoteWorkdir)
}

func requireRemoteShellScriptPatterns(t *testing.T, script, remoteWorkdir string) {
	t.Helper()
	for _, pattern := range []string{
		"rm -f \"$HOME/.ssh/known_hosts\" \"$HOME/.ssh/keys\" \"$HOME/.ssh/config\"",
		"old_umask=\"$(umask)\"",
		"umask 077",
		"umask \"$old_umask\"",
		"chmod 600 \"$HOME/.ssh/known_hosts\" \"$HOME/.ssh/keys\" \"$HOME/.ssh/config\"",
		"mkdir -p '/home/erun/git/erun'",
		"cd '/home/erun/git/erun'",
		"config_home=\"${XDG_CONFIG_HOME:-$HOME/.config}\"",
		"$config_home/erun/config.yaml",
		"$config_home/erun/tenant-a/config.yaml",
		"$config_home/erun/tenant-a/local/config.yaml",
		"defaulttenant: tenant-a",
		"projectroot: " + remoteWorkdir,
		"defaultenvironment: local",
		"repopath: " + remoteWorkdir,
		"kubernetescontext: in-cluster",
		"cat > \"$HOME/.erun_bashrc\" <<'EOF'\nexport ERUN_SHELL_HOST='tenant-a-local'",
		"if [ \"${1:-}\" = \"deploy\" ] && [ \"$#\" -eq 1 ] && [ -n \"${ERUN_SHELL_REQUEST_FILE:-}\" ]; then",
		": > \"$ERUN_SHELL_REQUEST_FILE\"",
		"exit 0",
		"printf '\\033]0;'tenant-a-local'\\007'",
		"request_file=\"$HOME/.erun-shell-request\"",
		"export ERUN_SHELL_REQUEST_FILE=\"$request_file\"",
		"IdentityFile ~/.ssh/keys",
		"if command -v git >/dev/null 2>&1; then if [ ! -d .git ]; then git clone git@github.com:'sophium'/'erun'.git .; fi; git config --global --add safe.directory '*'; fi",
		"/bin/bash --rcfile \"$HOME/.erun_bashrc\" -i || shell_status=$?",
		fmt.Sprintf("if [ -e \"$request_file\" ]; then rm -f \"$request_file\"; exit %d; fi", remoteShellReattachDeployExitCode),
		"exit \"$shell_status\"",
	} {
		requireStringContains(t, script, pattern, "expected script pattern")
	}
}

func requireRemoteShellSeededConfigs(t *testing.T, script, remoteWorkdir string) {
	t.Helper()
	toolConfigBody := extractHeredoc(t, script, `cat > "$config_home/erun/config.yaml" <<'EOF'`)
	var toolConfig ERunConfig
	requireNoError(t, yaml.Unmarshal([]byte(toolConfigBody), &toolConfig), "expected tool config heredoc to be valid yaml")
	requireEqual(t, toolConfig.DefaultTenant, "tenant-a", "tool config default tenant")

	tenantConfigBody := extractHeredoc(t, script, `cat > "$config_home/erun/tenant-a/config.yaml" <<'EOF'`)
	var tenantConfig TenantConfig
	requireNoError(t, yaml.Unmarshal([]byte(tenantConfigBody), &tenantConfig), "expected tenant config heredoc to be valid yaml")
	requireCondition(t, tenantConfig.ProjectRoot == remoteWorkdir && tenantConfig.DefaultEnvironment == "local", "unexpected tenant config: %+v", tenantConfig)

	envConfigBody := extractHeredoc(t, script, `cat > "$config_home/erun/tenant-a/local/config.yaml" <<'EOF'`)
	var envConfig EnvConfig
	requireNoError(t, yaml.Unmarshal([]byte(envConfigBody), &envConfig), "expected env config heredoc to be valid yaml")
	requireCondition(t, envConfig.RepoPath == remoteWorkdir && envConfig.KubernetesContext == "in-cluster", "unexpected env config: %+v", envConfig)
}

func TestRemoteShellScriptUsesXDGConfigHomeWhenPresent(t *testing.T) {
	script, err := buildRemoteShellScript(ShellLaunchParams{
		Dir:               "/Users/test/git/erun",
		Tenant:            "tenant-a",
		Environment:       "local",
		Title:             "tenant-a-local",
		KubernetesContext: "in-cluster",
	}, true)
	if err != nil {
		t.Fatalf("buildRemoteShellScript failed: %v", err)
	}

	for _, pattern := range []string{
		`config_home="${XDG_CONFIG_HOME:-$HOME/.config}"`,
		`mkdir -p "$config_home/erun"`,
		`cat > "$config_home/erun/config.yaml" <<'EOF'`,
	} {
		if !strings.Contains(script, pattern) {
			t.Fatalf("expected script to contain %q, got:\n%s", pattern, script)
		}
	}
}

func TestRemoteShellScriptUsesRepoBasenameForRemoteWorkdir(t *testing.T) {
	script, err := buildRemoteShellScript(ShellLaunchParams{
		Dir:               "/Users/test/git/frs",
		Tenant:            "frs",
		Environment:       "local",
		Title:             "frs-local",
		KubernetesContext: "cluster-local",
	}, true)
	if err != nil {
		t.Fatalf("buildRemoteShellScript failed: %v", err)
	}

	for _, pattern := range []string{
		"mkdir -p '/home/erun/git/frs'",
		"cd '/home/erun/git/frs'",
		"projectroot: /home/erun/git/frs",
		"repopath: /home/erun/git/frs",
	} {
		if !strings.Contains(script, pattern) {
			t.Fatalf("expected script to contain %q, got:\n%s", pattern, script)
		}
	}
}

func TestRemoteShellScriptMarksRemoteConfigsAndSkipsHostGitBootstrap(t *testing.T) {
	script, err := buildRemoteShellScript(ShellLaunchParams{
		Dir:               "/home/erun/git/erun",
		Tenant:            "erun",
		Environment:       "remote",
		Title:             "erun-remote",
		KubernetesContext: "in-cluster",
		RemoteRepo:        true,
	}, true)
	if err != nil {
		t.Fatalf("buildRemoteShellScript failed: %v", err)
	}

	if strings.Contains(script, "git clone git@") {
		t.Fatalf("did not expect host git bootstrap in remote repo shell script, got:\n%s", script)
	}

	tenantConfigBody := extractHeredoc(t, script, `cat > "$config_home/erun/erun/config.yaml" <<'EOF'`)
	var tenantConfig TenantConfig
	if err := yaml.Unmarshal([]byte(tenantConfigBody), &tenantConfig); err != nil {
		t.Fatalf("expected tenant config heredoc to be valid yaml, got %v\n%s", err, tenantConfigBody)
	}
	if tenantConfig.Remote {
		t.Fatalf("did not expect tenant config to be marked remote, got %+v", tenantConfig)
	}

	envConfigBody := extractHeredoc(t, script, `cat > "$config_home/erun/erun/remote/config.yaml" <<'EOF'`)
	var envConfig EnvConfig
	if err := yaml.Unmarshal([]byte(envConfigBody), &envConfig); err != nil {
		t.Fatalf("expected env config heredoc to be valid yaml, got %v\n%s", err, envConfigBody)
	}
	if !envConfig.Remote {
		t.Fatalf("expected remote env config, got %+v", envConfig)
	}
}

func TestRemoteShellScriptUsesOnlySSHCredentialsRelevantToRepoRemote(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	repoDir := createGitRepoWithRemote(t, "git@github.com:sophium/erun.git")

	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o755); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host github.com\n  IdentityFile ~/.ssh/id_ed25519\n\nHost other.example\n  IdentityFile ~/.ssh/id_other\n"), 0o600); err != nil {
		t.Fatalf("write ssh config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatalf("write ssh key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_other"), []byte("OTHER PRIVATE KEY"), 0o600); err != nil {
		t.Fatalf("write unrelated ssh key: %v", err)
	}

	script, err := buildRemoteShellScript(ShellLaunchParams{
		Dir:               repoDir,
		Tenant:            "tenant-a",
		Environment:       "local",
		Title:             "tenant-a-local",
		KubernetesContext: "in-cluster",
	}, false)
	if err != nil {
		t.Fatalf("buildRemoteShellScript failed: %v", err)
	}

	if !strings.Contains(script, "PRIVATE KEY") {
		t.Fatalf("expected matching private key in script, got:\n%s", script)
	}
	if strings.Contains(script, "OTHER PRIVATE KEY") {
		t.Fatalf("did not expect unrelated private key in script, got:\n%s", script)
	}
}

func TestRemoteShellScriptLoadsSSHConfigFromUserHomeDirWhenHOMEIsUnset(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", "")
	previousUserHomeDir := openUserHomeDir
	openUserHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() {
		openUserHomeDir = previousUserHomeDir
	})

	repoDir := createGitRepoWithRemote(t, "git@github.com:sophium/erun.git")
	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o755); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host github.com\n  IdentityFile ~/.ssh/id_ed25519\n"), 0o600); err != nil {
		t.Fatalf("write ssh config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatalf("write ssh key: %v", err)
	}

	script, err := buildRemoteShellScript(ShellLaunchParams{
		Dir:               repoDir,
		Tenant:            "tenant-a",
		Environment:       "local",
		Title:             "tenant-a-local",
		KubernetesContext: "in-cluster",
	}, false)
	if err != nil {
		t.Fatalf("buildRemoteShellScript failed: %v", err)
	}

	if !strings.Contains(script, "PRIVATE KEY") {
		t.Fatalf("expected private key to be loaded from user home dir, got:\n%s", script)
	}
}

func TestKubectlDeploymentWaitArgs(t *testing.T) {
	args := kubectlDeploymentWaitArgs(ShellLaunchParams{
		Tenant:            "tenant-a",
		Namespace:         "tenant-a-local",
		KubernetesContext: "cluster-local",
	})

	expected := []string{
		"--context", "cluster-local",
		"--namespace", "tenant-a-local",
		"wait",
		"--for=condition=Available",
		"--timeout", defaultShellLaunchWaitTimeout,
		"deployment/tenant-a-devops",
	}
	if strings.Join(args, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("unexpected wait args:\nwant: %#v\ngot:  %#v", expected, args)
	}
}

func TestShellDeploymentFailureDiagnosticIncludesPodExitDetails(t *testing.T) {
	req := ShellLaunchParams{
		Tenant:            "tenant-a",
		Environment:       "local",
		Namespace:         "tenant-a-local",
		KubernetesContext: "cluster-local",
	}
	runner := func(args []string, stdout, stderr io.Writer) error {
		command := strings.Join(args, " ")
		switch {
		case strings.Contains(command, " get pods "):
			_, _ = io.WriteString(stdout, `{
				"items": [{
					"metadata": {"name": "tenant-a-devops-abc123"},
					"status": {
						"phase": "Running",
						"conditions": [{
							"type": "Ready",
							"status": "False",
							"reason": "ContainersNotReady",
							"message": "containers with unready status: [erun-devops]"
						}],
						"containerStatuses": [{
							"name": "erun-devops",
							"ready": false,
							"restartCount": 1,
							"state": {"running": {"startedAt": "2026-04-30T20:33:30Z"}},
							"lastState": {"terminated": {
								"exitCode": 137,
								"reason": "OOMKilled",
								"message": "Container was killed because it used too much memory",
								"startedAt": "2026-04-30T20:33:28Z",
								"finishedAt": "2026-04-30T20:34:00Z"
							}}
						}]
					}
				}]
			}`)
		case strings.Contains(command, " get events "):
			_, _ = io.WriteString(stdout, `{
				"items": [{
					"type": "Normal",
					"reason": "Pulled",
					"message": "Successfully pulled image",
					"lastTimestamp": "2026-04-30T20:33:29Z"
				}, {
					"type": "Warning",
					"reason": "OOMKilling",
					"message": "Memory cgroup out of memory: Killed process 123",
					"count": 2,
					"lastTimestamp": "2026-04-30T20:34:00Z",
					"involvedObject": {"name": "tenant-a-devops-abc123"}
				}]
			}`)
		default:
			t.Fatalf("unexpected kubectl args: %#v", args)
		}
		return nil
	}

	err := enrichShellDeploymentError(req, errors.New("exit status 137"), runner)
	if err == nil {
		t.Fatal("expected enriched error")
	}
	got := err.Error()
	for _, want := range []string{
		"exit status 137",
		"Runtime pod diagnostics:",
		"Pod tenant-a-devops-abc123: phase=Running",
		"Ready=False (ContainersNotReady: containers with unready status: [erun-devops])",
		"Container erun-devops: ready=false restartCount=1",
		"lastState=terminated (exitCode=137, reason=OOMKilled, message=Container was killed because it used too much memory",
		"Warning OOMKilling: Memory cgroup out of memory: Killed process 123 (x2)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected diagnostic to contain %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Successfully pulled image") {
		t.Fatalf("expected normal events to be omitted, got:\n%s", got)
	}
}

func TestPreviewShellLaunchRedactsHostCredentialContents(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	repoDir := createGitRepoWithRemote(t, "git@github.com:sophium/erun.git")

	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o755); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host github.com\n  IdentityFile ~/.ssh/id_ed25519\n"), 0o600); err != nil {
		t.Fatalf("write ssh config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatalf("write ssh key: %v", err)
	}

	preview, err := PreviewShellLaunch(ShellLaunchParams{
		Dir:               repoDir,
		Tenant:            "tenant-a",
		Environment:       "local",
		Title:             "tenant-a-local",
		Namespace:         "tenant-a-local",
		KubernetesContext: "cluster-local",
	})
	if err != nil {
		t.Fatalf("PreviewShellLaunch failed: %v", err)
	}

	if !strings.Contains(strings.Join(preview.WaitArgs, " "), "wait --for=condition=Available --timeout 2m0s deployment/tenant-a-devops") {
		t.Fatalf("unexpected wait args: %#v", preview.WaitArgs)
	}
	if !strings.Contains(strings.Join(preview.ExecArgs, " "), "exec -it deployment/tenant-a-devops -- /bin/sh -lc") {
		t.Fatalf("unexpected exec args: %#v", preview.ExecArgs)
	}
	if strings.Contains(preview.Script, "PRIVATE KEY") {
		t.Fatalf("did not expect preview script to contain secret contents, got:\n%s", preview.Script)
	}
	if !strings.Contains(preview.Script, "<redacted>") {
		t.Fatalf("expected preview script to include redaction marker, got:\n%s", preview.Script)
	}
}

func createGitRepoWithRemote(t *testing.T, remoteURL string) string {
	t.Helper()

	repoDir := filepath.Join(t.TempDir(), "erun")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo dir: %v", err)
	}
	if output, err := exec.Command("git", "init", repoDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	if output, err := exec.Command("git", "-C", repoDir, "remote", "add", "origin", remoteURL).CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, output)
	}
	return repoDir
}

func extractHeredoc(t *testing.T, script, header string) string {
	t.Helper()

	start := strings.Index(script, header)
	if start < 0 {
		t.Fatalf("expected script to contain heredoc header %q, got:\n%s", header, script)
	}
	body := script[start+len(header):]
	body = strings.TrimPrefix(body, "\n")
	end := strings.Index(body, "\nEOF")
	if end < 0 {
		t.Fatalf("expected heredoc body for %q to terminate with EOF, got:\n%s", header, script)
	}
	return body[:end]
}

type openStore struct {
	toolConfig                        ERunConfig
	loadERunErr                       error
	tenantConfigs                     map[string]TenantConfig
	envConfigs                        map[string]EnvConfig
	resolveEffectiveKubernetesContext func(environment, configured string) string
	resolveDeployKubernetesContext    func(environment, configured string) string
}

func (s openStore) LoadERunConfig() (ERunConfig, string, error) {
	if s.loadERunErr != nil {
		return ERunConfig{}, "", s.loadERunErr
	}
	return s.toolConfig, "", nil
}

func (s openStore) LoadTenantConfig(tenant string) (TenantConfig, string, error) {
	config, ok := s.tenantConfigs[tenant]
	if !ok {
		return TenantConfig{}, "", ErrNotInitialized
	}
	return config, "", nil
}

func (s openStore) ListTenantConfigs() ([]TenantConfig, error) {
	tenants := make([]TenantConfig, 0, len(s.tenantConfigs))
	for _, config := range s.tenantConfigs {
		tenants = append(tenants, config)
	}
	return tenants, nil
}

func (s openStore) ResolveEffectiveKubernetesContext(environment, configured string) string {
	if s.resolveEffectiveKubernetesContext == nil {
		return configured
	}
	return s.resolveEffectiveKubernetesContext(environment, configured)
}

func (s openStore) ResolveDeployKubernetesContext(environment, configured string) string {
	if s.resolveDeployKubernetesContext == nil {
		return configured
	}
	return s.resolveDeployKubernetesContext(environment, configured)
}

func (s openStore) LoadEnvConfig(tenant, environment string) (EnvConfig, string, error) {
	config, ok := s.envConfigs[tenant+"/"+environment]
	if !ok {
		return EnvConfig{}, "", ErrNotInitialized
	}
	return config, "", nil
}

func (s openStore) ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	envs := make([]EnvConfig, 0, len(s.envConfigs))
	for key, config := range s.envConfigs {
		if !strings.HasPrefix(key, tenant+"/") {
			continue
		}
		envs = append(envs, config)
	}
	return envs, nil
}
