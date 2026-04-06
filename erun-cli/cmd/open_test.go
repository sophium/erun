package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/sophium/erun/internal/opener"
)

func TestOpenCommandLaunchesShell(t *testing.T) {
	repoPath := t.TempDir()
	launched := opener.ShellLaunchRequest{}
	cmd := NewOpenCmd(Dependencies{
		Store: openCommandStore{repoPath: repoPath},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
			launched = req
			return nil
		},
	}, nil)
	cmd.SetArgs([]string{"tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if launched.Dir != repoPath || launched.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if launched.Namespace != "tenant-a-dev" || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected remote shell target: %+v", launched)
	}
}

func TestOpenCommandNoShellConfiguresLocalKubeconfig(t *testing.T) {
	repoPath := t.TempDir()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := NewOpenCmd(Dependencies{
		Store: openCommandStore{repoPath: repoPath},
		CheckKubernetesDeployment: func(req KubernetesDeploymentCheckRequest) (bool, error) {
			t.Fatalf("did not expect deployment check for --no-shell: %+v", req)
			return false, nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
			t.Fatalf("did not expect remote shell launch for --no-shell: %+v", req)
			return nil
		},
	}, nil)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"tenant-a", "dev", "--no-shell"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	expected := "kubectl config use-context 'cluster-dev' >/dev/null &&\n" +
		"kubectl config set-context --current --namespace='tenant-a-dev' >/dev/null &&\n" +
		"cd '" + repoPath + "'\n"
	if stdout.String() != expected {
		t.Fatalf("unexpected no-shell output:\nwant:\n%s\ngot:\n%s", expected, stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("did not expect stderr output in buffered mode, got %q", stderr.String())
	}
}

func TestOpenCommandDryRunPrintsResolvedOpenTraceWithoutLaunchingShell(t *testing.T) {
	repoPath := t.TempDir()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := NewRootCmd(Dependencies{
		Store: openCommandStore{repoPath: repoPath},
		CheckKubernetesDeployment: func(req KubernetesDeploymentCheckRequest) (bool, error) {
			if req.Name != devopsComponentName || req.Namespace != "tenant-a-dev" || req.KubernetesContext != "cluster-dev" {
				t.Fatalf("unexpected deployment check: %+v", req)
			}
			return true, nil
		},
		DeployHelmChart: func(req HelmDeployRequest) error {
			t.Fatalf("did not expect runtime deployment during dry-run: %+v", req)
			return nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
			t.Fatalf("did not expect remote shell launch during dry-run: %+v", req)
			return nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "open", "tenant-a", "dev", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stdout.String(); got != "" {
		t.Fatalf("did not expect stdout output during dry-run, got %q", got)
	}
	output := stderr.String()
	for _, want := range []string{
		"[dry-run] decision: resolved tenant=tenant-a",
		"[dry-run] decision: resolved environment=dev",
		"[dry-run] decision: resolved namespace=tenant-a-dev",
		"[dry-run] decision: resolved kubernetes context=cluster-dev",
		"[dry-run] kubectl --context cluster-dev --namespace tenant-a-dev wait --for=condition=Available --timeout 2m0s deployment/erun-devops",
		"[dry-run] kubectl --context cluster-dev --namespace tenant-a-dev exec -it deployment/erun-devops -- /bin/sh -lc '<bootstrap-script>'",
		"[dry-run] bootstrap-script:",
		"[dry-run]   set -eu",
		"[dry-run] decision: the remote shell bootstrap preview redacts host credential file contents while preserving the command shape",
		"[dry-run] decision: the devops runtime is already deployed in the target namespace",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run trace to contain %q, got %q", want, output)
		}
	}
}

func TestOpenCommandDryRunPrintsDeployPlanWhenDevopsRuntimeIsMissing(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := NewRootCmd(Dependencies{
		Store: openCommandStore{
			repoPath:   projectRoot,
			toolConfig: internal.ERunConfig{DefaultTenant: "tenant-a"},
		},
		CheckKubernetesDeployment: func(req KubernetesDeploymentCheckRequest) (bool, error) {
			return false, nil
		},
		DeployHelmChart: func(req HelmDeployRequest) error {
			t.Fatalf("did not expect runtime deployment during dry-run: %+v", req)
			return nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
			t.Fatalf("did not expect remote shell launch during dry-run: %+v", req)
			return nil
		},
		ResolveKubernetesDeployContext: func() (KubernetesDeployContext, error) {
			return KubernetesDeployContext{
				Dir:           chartPath,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "open", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	for _, want := range []string{
		"[dry-run] decision: the devops runtime is missing and will be deployed before opening the shell",
		"[dry-run] docker build -t erunpaas/erun-devops:1.1.0",
		"[dry-run] docker push erunpaas/erun-devops:1.1.0",
		"[dry-run] helm upgrade --install --wait --wait-for-jobs --timeout 2m0s --namespace tenant-a-local --kube-context cluster-dev -f " + filepath.Join(chartPath, "values.local.yaml"),
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
}

func TestOpenCommandLaunchesShellWithDefaults(t *testing.T) {
	repoPath := t.TempDir()
	launched := opener.ShellLaunchRequest{}
	cmd := NewOpenCmd(Dependencies{
		Store: openCommandStore{
			repoPath:   repoPath,
			toolConfig: internal.ERunConfig{DefaultTenant: "tenant-a"},
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
			launched = req
			return nil
		},
	}, nil)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if launched.Dir != repoPath || launched.Title != "tenant-a-local" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if launched.Namespace != "tenant-a-local" || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected remote shell target: %+v", launched)
	}
}

func TestOpenCommandLaunchesShellWithDefaultTenantAndRequestedEnvironment(t *testing.T) {
	repoPath := t.TempDir()
	launched := opener.ShellLaunchRequest{}
	cmd := NewOpenCmd(Dependencies{
		Store: openCommandStore{
			repoPath:   repoPath,
			toolConfig: internal.ERunConfig{DefaultTenant: "tenant-a"},
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
			launched = req
			return nil
		},
	}, nil)
	cmd.SetArgs([]string{"dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if launched.Dir != repoPath || launched.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if launched.Namespace != "tenant-a-dev" || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected remote shell target: %+v", launched)
	}
}

func TestOpenCommandRunsInitWhenKubernetesContextIsMissing(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "dev",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{
		Name:     "dev",
		RepoPath: projectRoot,
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	cmd := NewOpenCmd(Dependencies{
		Store: bootstrap.ConfigStore{},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			t.Fatalf("unexpected prompt: %+v", prompt)
			return "", nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			if fmt.Sprint(prompt.Label) != bootstrap.KubernetesContextLabel("tenant-a", "dev") {
				t.Fatalf("unexpected select prompt: %+v", prompt)
			}
			return 0, "cluster-dev", nil
		},
		ListKubernetesContexts: func() ([]string, error) {
			return []string{"cluster-dev", "cluster-prod"}, nil
		},
		EnsureKubernetesNamespace: func(contextName, namespace string) error {
			if contextName != "cluster-dev" || namespace != "tenant-a-dev" {
				t.Fatalf("unexpected namespace ensure request: context=%q namespace=%q", contextName, namespace)
			}
			return nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
			t.Fatalf("unexpected shell launch: %+v", req)
			return nil
		},
	}, nil)
	cmd.SetArgs([]string{"tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	envConfig, _, err := internal.LoadEnvConfig("tenant-a", "dev")
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if envConfig.KubernetesContext != "cluster-dev" {
		t.Fatalf("expected kubernetes context to be saved, got %+v", envConfig)
	}
}

func TestOpenCommandRunsInitWhenEnvironmentIsMissing(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "dev",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}

	cmd := NewOpenCmd(Dependencies{
		Store: bootstrap.ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			if fmt.Sprint(prompt.Label) == bootstrap.ContainerRegistryLabel("tenant-a", "dev") {
				return "", nil
			}
			return "y", nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			if fmt.Sprint(prompt.Label) != bootstrap.KubernetesContextLabel("tenant-a", "dev") {
				t.Fatalf("unexpected select prompt: %+v", prompt)
			}
			return 0, "cluster-dev", nil
		},
		ListKubernetesContexts: func() ([]string, error) {
			return []string{"cluster-dev"}, nil
		},
		EnsureKubernetesNamespace: func(contextName, namespace string) error {
			if contextName != "cluster-dev" || namespace != "tenant-a-dev" {
				t.Fatalf("unexpected namespace ensure request: context=%q namespace=%q", contextName, namespace)
			}
			return nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
			t.Fatalf("unexpected shell launch: %+v", req)
			return nil
		},
	}, nil)
	cmd.SetArgs([]string{"tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	envConfig, _, err := internal.LoadEnvConfig("tenant-a", "dev")
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if envConfig.KubernetesContext != "cluster-dev" {
		t.Fatalf("expected environment to be initialized, got %+v", envConfig)
	}
}

func TestOpenCommandDeploysDevopsWhenMissing(t *testing.T) {
	repoPath := t.TempDir()
	chartPath := filepath.Join(repoPath, "erun-devops", "k8s", "erun-devops")
	if err := os.MkdirAll(chartPath, 0o755); err != nil {
		t.Fatalf("mkdir chart path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: erun-devops\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "values.dev.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.dev.yaml: %v", err)
	}

	launched := opener.ShellLaunchRequest{}
	deployed := HelmDeployRequest{}
	cmd := NewOpenCmd(Dependencies{
		Store: openCommandStore{repoPath: repoPath},
		CheckKubernetesDeployment: func(req KubernetesDeploymentCheckRequest) (bool, error) {
			if req.Name != "erun-devops" || req.Namespace != "tenant-a-dev" || req.KubernetesContext != "cluster-dev" {
				t.Fatalf("unexpected deployment check: %+v", req)
			}
			return false, nil
		},
		DeployHelmChart: func(req HelmDeployRequest) error {
			deployed = req
			return nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
			launched = req
			return nil
		},
	}, nil)
	cmd.SetArgs([]string{"tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if deployed.ReleaseName != "erun-devops" || deployed.ChartPath != chartPath {
		t.Fatalf("unexpected deploy request: %+v", deployed)
	}
	if deployed.ValuesFilePath != filepath.Join(chartPath, "values.dev.yaml") {
		t.Fatalf("unexpected values path: %+v", deployed)
	}
	if deployed.Namespace != "tenant-a-dev" || deployed.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected deployment target: %+v", deployed)
	}
	if launched.Namespace != "tenant-a-dev" || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
}

type openCommandStore struct {
	repoPath   string
	toolConfig internal.ERunConfig
}

func (s openCommandStore) LoadERunConfig() (internal.ERunConfig, string, error) {
	return s.toolConfig, "", nil
}

func (openCommandStore) SaveERunConfig(internal.ERunConfig) error {
	return nil
}

func (openCommandStore) ListTenantConfigs() ([]internal.TenantConfig, error) {
	return nil, nil
}

func (s openCommandStore) LoadTenantConfig(tenant string) (internal.TenantConfig, string, error) {
	return internal.TenantConfig{
		Name:               tenant,
		ProjectRoot:        s.repoPath,
		DefaultEnvironment: "local",
	}, "", nil
}

func (openCommandStore) SaveTenantConfig(internal.TenantConfig) error {
	return nil
}

func (s openCommandStore) LoadEnvConfig(tenant, env string) (internal.EnvConfig, string, error) {
	return internal.EnvConfig{
		Name:              env,
		RepoPath:          s.repoPath,
		KubernetesContext: "cluster-dev",
	}, "", nil
}

func (openCommandStore) SaveEnvConfig(string, internal.EnvConfig) error {
	return nil
}
