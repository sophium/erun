package opener

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
)

func TestResolveUsesDefaultTenantAndEnvironment(t *testing.T) {
	repoPath := t.TempDir()
	service := Service{
		Store: openerStore{
			toolConfig: internal.ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]internal.TenantConfig{
				"tenant-a": {
					Name:               "tenant-a",
					ProjectRoot:        filepath.Join(t.TempDir(), "fallback"),
					DefaultEnvironment: "dev",
				},
			},
			envConfigs: map[string]internal.EnvConfig{
				"tenant-a/dev": {
					Name:              "dev",
					RepoPath:          repoPath,
					KubernetesContext: "cluster-dev",
				},
			},
		},
	}

	result, err := service.Resolve(Request{
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

func TestResolveFallsBackToTenantProjectRoot(t *testing.T) {
	repoPath := t.TempDir()
	service := Service{
		Store: openerStore{
			tenantConfigs: map[string]internal.TenantConfig{
				"tenant-a": {
					Name:               "tenant-a",
					ProjectRoot:        repoPath,
					DefaultEnvironment: "dev",
				},
			},
			envConfigs: map[string]internal.EnvConfig{
				"tenant-a/dev": {Name: "dev", KubernetesContext: "cluster-dev"},
			},
		},
	}

	result, err := service.Resolve(Request{
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

func TestResolveRequiresDefaultTenant(t *testing.T) {
	service := Service{Store: openerStore{loadERunErr: internal.ErrNotInitialized}}

	if _, err := service.Resolve(Request{UseDefaultTenant: true}); !errors.Is(err, ErrDefaultTenantNotConfigured) {
		t.Fatalf("expected ErrDefaultTenantNotConfigured, got %v", err)
	}
}

func TestResolveRequiresDefaultEnvironment(t *testing.T) {
	service := Service{
		Store: openerStore{
			toolConfig: internal.ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]internal.TenantConfig{
				"tenant-a": {Name: "tenant-a"},
			},
		},
	}

	if _, err := service.Resolve(Request{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	}); !errors.Is(err, ErrDefaultEnvironmentNotConfigured) {
		t.Fatalf("expected ErrDefaultEnvironmentNotConfigured, got %v", err)
	}
}

func TestResolveReportsMissingTenant(t *testing.T) {
	service := Service{Store: openerStore{}}

	_, err := service.Resolve(Request{
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

func TestResolveRequiresKubernetesContextAssociation(t *testing.T) {
	repoPath := t.TempDir()
	service := Service{
		Store: openerStore{
			tenantConfigs: map[string]internal.TenantConfig{
				"tenant-a": {
					Name:               "tenant-a",
					ProjectRoot:        repoPath,
					DefaultEnvironment: "dev",
				},
			},
			envConfigs: map[string]internal.EnvConfig{
				"tenant-a/dev": {
					Name:     "dev",
					RepoPath: repoPath,
				},
			},
		},
	}

	_, err := service.Resolve(Request{
		Tenant:      "tenant-a",
		Environment: "dev",
	})
	if !errors.Is(err, ErrKubernetesContextNotConfigured) {
		t.Fatalf("expected ErrKubernetesContextNotConfigured, got %v", err)
	}
}

func TestRunLaunchesShell(t *testing.T) {
	repoPath := t.TempDir()
	launched := ShellLaunchRequest{}
	service := Service{
		Store: openerStore{
			tenantConfigs: map[string]internal.TenantConfig{
				"tenant-a": {
					Name:               "tenant-a",
					ProjectRoot:        repoPath,
					DefaultEnvironment: "dev",
				},
			},
			envConfigs: map[string]internal.EnvConfig{
				"tenant-a/dev": {
					Name:              "dev",
					RepoPath:          repoPath,
					KubernetesContext: "cluster-dev",
				},
			},
		},
		LaunchShell: func(req ShellLaunchRequest) error {
			launched = req
			return nil
		},
	}

	if _, err := service.Run(Request{Tenant: "tenant-a", Environment: "dev"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if launched.Dir != repoPath || launched.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if launched.Tenant != "tenant-a" || launched.Environment != "dev" {
		t.Fatalf("unexpected shell launch request identity: %+v", launched)
	}
	if launched.Namespace != bootstrap.KubernetesNamespaceName("tenant-a", "dev") || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected remote shell target: %+v", launched)
	}
}

func TestRemoteShellScriptSeedsERunConfig(t *testing.T) {
	repoDir := createGitRepoWithRemote(t, "git@github.com:sophium/erun.git")
	script, err := remoteShellScript(ShellLaunchRequest{
		Dir:         repoDir,
		Tenant:      "tenant-a",
		Environment: "local",
		Title:       "tenant-a-local",
	})
	if err != nil {
		t.Fatalf("remoteShellScript failed: %v", err)
	}

	for _, pattern := range []string{
		"export ERUN_SHELL_HOST='tenant-a-local'",
		"/home/erun/.config/erun/config.yaml",
		"/home/erun/.config/erun/tenant-a/config.yaml",
		"/home/erun/.config/erun/tenant-a/local/config.yaml",
		"defaulttenant: tenant-a",
		"defaultenvironment: local",
		"kubernetescontext: in-cluster",
		"cd '/home/erun/git/erun'",
	} {
		if !strings.Contains(script, pattern) {
			t.Fatalf("expected script to contain %q, got:\n%s", pattern, script)
		}
	}
}

func TestRemoteShellScriptSeedsOnlySSHCredentialsRelevantToRepoRemote(t *testing.T) {
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

	script, err := remoteShellScript(ShellLaunchRequest{
		Dir:         repoDir,
		Tenant:      "tenant-a",
		Environment: "local",
		Title:       "tenant-a-local",
	})
	if err != nil {
		t.Fatalf("remoteShellScript failed: %v", err)
	}

	for _, pattern := range []string{
		"/home/erun/.ssh/config",
		"/home/erun/.ssh/id_ed25519",
		"chmod 0600 '/home/erun/.ssh/config'",
		"chmod 0600 '/home/erun/.ssh/id_ed25519'",
		"chmod 700 '/home/erun/.ssh'",
	} {
		if !strings.Contains(script, pattern) {
			t.Fatalf("expected script to contain %q, got:\n%s", pattern, script)
		}
	}
	for _, forbidden := range []string{
		"/home/erun/.ssh/id_other",
		"IdentityFile ~/.ssh/id_other",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("did not expect script to contain %q, got:\n%s", forbidden, script)
		}
	}
}

func TestRemoteShellScriptDoesNotCopyHostGitConfig(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	repoDir := createGitRepoWithRemote(t, "git@github.com:sophium/erun.git")

	if err := os.WriteFile(filepath.Join(homeDir, ".gitconfig"), []byte("[diff]\n\texternal = difft\n"), 0o644); err != nil {
		t.Fatalf("write .gitconfig: %v", err)
	}

	script, err := remoteShellScript(ShellLaunchRequest{
		Dir:         repoDir,
		Tenant:      "tenant-a",
		Environment: "local",
		Title:       "tenant-a-local",
	})
	if err != nil {
		t.Fatalf("remoteShellScript failed: %v", err)
	}

	for _, forbidden := range []string{
		"/home/erun/.gitconfig",
		"difft",
		"/home/erun/.config/git/config",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("did not expect script to contain %q, got:\n%s", forbidden, script)
		}
	}
}

func TestRemoteShellScriptCopiesOnlyHTTPSCredentialsRelevantToRepoRemote(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	repoDir := createGitRepoWithRemote(t, "https://github.com/sophium/erun.git")

	if err := os.WriteFile(filepath.Join(homeDir, ".git-credentials"), []byte("https://user:token@github.com\nhttps://user:token@example.com\n"), 0o600); err != nil {
		t.Fatalf("write .git-credentials: %v", err)
	}

	script, err := remoteShellScript(ShellLaunchRequest{
		Dir:         repoDir,
		Tenant:      "tenant-a",
		Environment: "local",
		Title:       "tenant-a-local",
	})
	if err != nil {
		t.Fatalf("remoteShellScript failed: %v", err)
	}

	for _, pattern := range []string{
		"/home/erun/.git-credentials",
		"chmod 0600 '/home/erun/.git-credentials'",
		"git config --global credential.helper 'store --file /home/erun/.git-credentials'",
	} {
		if !strings.Contains(script, pattern) {
			t.Fatalf("expected script to contain %q, got:\n%s", pattern, script)
		}
	}
	if strings.Contains(script, "example.com") {
		t.Fatalf("did not expect unrelated HTTPS credentials in script, got:\n%s", script)
	}
}

func TestKubectlDeploymentWaitArgs(t *testing.T) {
	args := kubectlDeploymentWaitArgs(ShellLaunchRequest{
		Namespace:         "tenant-a-local",
		KubernetesContext: "cluster-local",
	})

	expected := []string{
		"--context", "cluster-local",
		"--namespace", "tenant-a-local",
		"wait",
		"--for=condition=Available",
		"--timeout", defaultShellLaunchWaitTimeout,
		"deployment/erun-devops",
	}
	if strings.Join(args, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("unexpected wait args:\nwant: %#v\ngot:  %#v", expected, args)
	}
}

func TestKubectlExecArgsInteractive(t *testing.T) {
	args := kubectlExecArgs(ShellLaunchRequest{
		Namespace:         "tenant-a-local",
		KubernetesContext: "cluster-local",
	}, "echo hi")
	if !strings.Contains(strings.Join(args, " "), " exec -it deployment/erun-devops -- /bin/sh -lc echo hi") {
		t.Fatalf("unexpected interactive exec args %#v", args)
	}
}

func TestPreviewShellLaunchRedactsHostCredentialContents(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	repoDir := createGitRepoWithRemote(t, "https://github.com/sophium/erun.git")

	if err := os.WriteFile(filepath.Join(homeDir, ".git-credentials"), []byte("https://user:token@github.com\n"), 0o600); err != nil {
		t.Fatalf("write .git-credentials: %v", err)
	}

	preview, err := PreviewShellLaunch(ShellLaunchRequest{
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

	if !strings.Contains(strings.Join(preview.WaitArgs, " "), "wait --for=condition=Available --timeout 2m0s deployment/erun-devops") {
		t.Fatalf("unexpected wait args: %#v", preview.WaitArgs)
	}
	if !strings.Contains(strings.Join(preview.ExecArgs, " "), "exec -it deployment/erun-devops -- /bin/sh -lc") {
		t.Fatalf("unexpected exec args: %#v", preview.ExecArgs)
	}
	if strings.Contains(preview.Script, "https://user:token@github.com") {
		t.Fatalf("did not expect preview script to contain secret contents, got:\n%s", preview.Script)
	}
	if !strings.Contains(preview.Script, "# redacted preview of") {
		t.Fatalf("expected preview script to include redaction marker, got:\n%s", preview.Script)
	}
}

func createGitRepoWithRemote(t *testing.T, remoteURL string) string {
	t.Helper()

	rootDir := t.TempDir()
	repoDir := filepath.Join(rootDir, "erun")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo dir: %v", err)
	}
	gitDir := filepath.Join(repoDir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	config := "[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tlogallrefupdates = true\n[remote \"origin\"]\n\turl = " + remoteURL + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(config), 0o644); err != nil {
		t.Fatalf("write .git/config: %v", err)
	}
	return repoDir
}

type openerStore struct {
	toolConfig    internal.ERunConfig
	loadERunErr   error
	tenantConfigs map[string]internal.TenantConfig
	envConfigs    map[string]internal.EnvConfig
}

func (s openerStore) LoadERunConfig() (internal.ERunConfig, string, error) {
	if s.loadERunErr != nil {
		return internal.ERunConfig{}, "", s.loadERunErr
	}
	return s.toolConfig, "", nil
}

func (s openerStore) LoadTenantConfig(tenant string) (internal.TenantConfig, string, error) {
	config, ok := s.tenantConfigs[tenant]
	if !ok {
		return internal.TenantConfig{}, "", internal.ErrNotInitialized
	}
	return config, "", nil
}

func (s openerStore) LoadEnvConfig(tenant, environment string) (internal.EnvConfig, string, error) {
	config, ok := s.envConfigs[tenant+"/"+environment]
	if !ok {
		return internal.EnvConfig{}, "", internal.ErrNotInitialized
	}
	return config, "", nil
}
