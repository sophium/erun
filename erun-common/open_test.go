package eruncommon

import (
	"errors"
	"fmt"
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
}

func TestOpenResolveAllowsRemoteRepoPathWithoutLocalCheckout(t *testing.T) {
	store := openStore{
		toolConfig: ERunConfig{DefaultTenant: "frs"},
		tenantConfigs: map[string]TenantConfig{
			"frs": {
				Name:               "frs",
				ProjectRoot:        "/home/erun/git/frs",
				DefaultEnvironment: "dev",
				Remote:             true,
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

func TestOpenResolveUsesCurrentDirectoryTenantBeforeDefault(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("return to original dir: %v", err)
		}
	})

	repoRoot := filepath.Join(t.TempDir(), "tenant-a")
	subDir := filepath.Join(repoRoot, "nested")
	defaultRepo := filepath.Join(t.TempDir(), "tenant-b")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}
	if err := os.MkdirAll(defaultRepo, 0o755); err != nil {
		t.Fatalf("mkdir default repo: %v", err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

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
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result.Tenant != "tenant-a" || result.Environment != "dev" {
		t.Fatalf("expected current directory tenant to win, got %+v", result)
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
	if err != nil {
		t.Fatalf("buildRemoteShellScript failed: %v", err)
	}

	remoteWorkdir := "/home/erun/git/erun"

	for _, pattern := range []string{
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
		if !strings.Contains(script, pattern) {
			t.Fatalf("expected script to contain %q, got:\n%s", pattern, script)
		}
	}
	if strings.Contains(script, repoDir) {
		t.Fatalf("did not expect host repo path %q in remote bootstrap script, got:\n%s", repoDir, script)
	}

	toolConfigBody := extractHeredoc(t, script, `cat > "$config_home/erun/config.yaml" <<'EOF'`)
	var toolConfig ERunConfig
	if err := yaml.Unmarshal([]byte(toolConfigBody), &toolConfig); err != nil {
		t.Fatalf("expected tool config heredoc to be valid yaml, got %v\n%s", err, toolConfigBody)
	}
	if toolConfig.DefaultTenant != "tenant-a" {
		t.Fatalf("unexpected tool config: %+v", toolConfig)
	}

	tenantConfigBody := extractHeredoc(t, script, `cat > "$config_home/erun/tenant-a/config.yaml" <<'EOF'`)
	var tenantConfig TenantConfig
	if err := yaml.Unmarshal([]byte(tenantConfigBody), &tenantConfig); err != nil {
		t.Fatalf("expected tenant config heredoc to be valid yaml, got %v\n%s", err, tenantConfigBody)
	}
	if tenantConfig.ProjectRoot != remoteWorkdir || tenantConfig.DefaultEnvironment != "local" {
		t.Fatalf("unexpected tenant config: %+v", tenantConfig)
	}

	envConfigBody := extractHeredoc(t, script, `cat > "$config_home/erun/tenant-a/local/config.yaml" <<'EOF'`)
	var envConfig EnvConfig
	if err := yaml.Unmarshal([]byte(envConfigBody), &envConfig); err != nil {
		t.Fatalf("expected env config heredoc to be valid yaml, got %v\n%s", err, envConfigBody)
	}
	if envConfig.RepoPath != remoteWorkdir || envConfig.KubernetesContext != "in-cluster" {
		t.Fatalf("unexpected env config: %+v", envConfig)
	}
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
	if !tenantConfig.Remote {
		t.Fatalf("expected remote tenant config, got %+v", tenantConfig)
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
	if !strings.Contains(strings.Join(preview.ExecArgs, " "), "exec -it -c tenant-a-devops deployment/tenant-a-devops -- /bin/sh -lc") {
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
