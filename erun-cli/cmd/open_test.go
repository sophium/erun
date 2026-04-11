package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

func TestOpenCommandLaunchesShell(t *testing.T) {
	repoPath := t.TempDir()
	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{repoPath: repoPath},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "tenant-a", "dev"})

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

func TestOpenCommandLaunchesLocalRuntimeForLocalEnvironment(t *testing.T) {
	repoPath := t.TempDir()
	componentDir := filepath.Join(repoPath, "tenant-a-devops", "docker", "tenant-a-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	for _, path := range []string{
		filepath.Join(repoPath, "tenant-a-devops", "VERSION"),
		filepath.Join(componentDir, "VERSION"),
	} {
		if err := os.WriteFile(path, []byte("1.1.0\n"), 0o644); err != nil {
			t.Fatalf("write VERSION: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	var builtTag string
	var ranArgs []string
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   repoPath,
			toolConfig: common.ERunConfig{DefaultTenant: "tenant-a"},
		},
		BuildDockerImage: func(dir, dockerfilePath, tag string, stdout, stderr io.Writer) error {
			builtTag = tag
			if dir != repoPath {
				t.Fatalf("unexpected build dir: %q", dir)
			}
			if dockerfilePath != filepath.Join(componentDir, "Dockerfile") {
				t.Fatalf("unexpected dockerfile path: %q", dockerfilePath)
			}
			return nil
		},
		OpenDockerContainer: func(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
			ranArgs = append([]string{}, args...)
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect kubernetes shell launch: %+v", req)
			return nil
		},
		Now: func() time.Time {
			return time.Date(2026, time.April, 11, 10, 9, 8, 0, time.UTC)
		},
	})
	cmd.SetArgs([]string{"open"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if builtTag != "erunpaas/tenant-a-devops:1.1.0" {
		t.Fatalf("unexpected build tag: %q", builtTag)
	}
	runCommand := strings.Join(ranArgs, " ")
	for _, want := range []string{
		"run --rm -it",
		"ERUN_REPO_PATH=/home/erun/git/" + filepath.Base(repoPath),
		"ERUN_KUBERNETES_CONTEXT=cluster-dev",
		"ERUN_SHELL_HOST=tenant-a-local",
		"erunpaas/tenant-a-devops:1.1.0",
		"shell",
	} {
		if !strings.Contains(runCommand, want) {
			t.Fatalf("expected local runtime args to contain %q, got %q", want, runCommand)
		}
	}
}

func TestOpenCommandNoShellConfiguresLocalKubeconfig(t *testing.T) {
	repoPath := t.TempDir()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{repoPath: repoPath},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			t.Fatalf("did not expect deployment check for --no-shell: %+v", req)
			return false, nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect remote shell launch for --no-shell: %+v", req)
			return nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"open", "tenant-a", "dev", "--no-shell"})

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
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{repoPath: repoPath},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			if req.Name != "tenant-a-devops" || req.Namespace != "tenant-a-dev" || req.KubernetesContext != "cluster-dev" {
				t.Fatalf("unexpected deployment check: %+v", req)
			}
			return true, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("did not expect runtime deployment during dry-run: %+v", req)
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
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
		"kubectl --context cluster-dev --namespace tenant-a-dev wait --for=condition=Available --timeout 2m0s deployment/tenant-a-devops",
		"kubectl --context cluster-dev --namespace tenant-a-dev exec -it -c erun-devops deployment/tenant-a-devops -- /bin/sh -lc '<bootstrap-script>'",
		"bootstrap-script:",
		"  set -eu",
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
	if err := os.WriteFile(filepath.Join(chartPath, "values.dev.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.dev.yaml: %v", err)
	}
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
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   projectRoot,
			toolConfig: common.ERunConfig{DefaultTenant: "tenant-a"},
		},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			return false, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("did not expect runtime deployment during dry-run: %+v", req)
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect remote shell launch during dry-run: %+v", req)
			return nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				Dir:           chartPath,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "open", "tenant-a", "dev", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	for _, want := range []string{
		"helm upgrade --install --wait --wait-for-jobs --timeout 2m0s --namespace tenant-a-dev --kube-context cluster-dev -f " + filepath.Join(chartPath, "values.dev.yaml"),
		"kubectl --context cluster-dev --namespace tenant-a-dev wait --for=condition=Available --timeout 2m0s deployment/tenant-a-devops",
		"kubectl --context cluster-dev --namespace tenant-a-dev exec -it -c erun-devops deployment/tenant-a-devops -- /bin/sh -lc '<bootstrap-script>'",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
}

func TestOpenCommandDryRunFallsBackToDefaultRuntimeChartWhenTenantRepoHasNoDevopsChart(t *testing.T) {
	repoPath := t.TempDir()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   repoPath,
			toolConfig: common.ERunConfig{DefaultTenant: "frs"},
		},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			return false, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("did not expect runtime deployment during dry-run: %+v", req)
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect remote shell launch during dry-run: %+v", req)
			return nil
		},
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"open", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	if strings.Contains(output, "docker build -t") || strings.Contains(output, "docker push ") {
		t.Fatalf("did not expect local build or push for default runtime chart, got %q", output)
	}
	for _, want := range []string{
		"helm upgrade --install --wait --wait-for-jobs --timeout 2m0s --namespace frs-local --kube-context cluster-dev",
		"kubectl --context cluster-dev --namespace frs-local wait --for=condition=Available --timeout 2m0s deployment/frs-devops",
		"kubectl --context cluster-dev --namespace frs-local exec -it -c erun-devops deployment/frs-devops -- /bin/sh -lc '<bootstrap-script>'",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
}

func TestOpenCommandLaunchesShellWithDefaults(t *testing.T) {
	repoPath := t.TempDir()
	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   repoPath,
			toolConfig: common.ERunConfig{DefaultTenant: "tenant-a"},
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"open"})

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

func TestOpenCommandDryRunPrintsLocalRuntimeTraceWhenTenantDevopsModuleExists(t *testing.T) {
	repoPath := t.TempDir()
	componentDir := filepath.Join(repoPath, "tenant-a-devops", "docker", "tenant-a-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	for _, path := range []string{
		filepath.Join(repoPath, "tenant-a-devops", "VERSION"),
		filepath.Join(componentDir, "VERSION"),
	} {
		if err := os.WriteFile(path, []byte("1.1.0\n"), 0o644); err != nil {
			t.Fatalf("write VERSION: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   repoPath,
			toolConfig: common.ERunConfig{DefaultTenant: "tenant-a"},
		},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			t.Fatalf("did not expect kubernetes deployment check: %+v", req)
			return false, nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("did not expect kubernetes shell launch: %+v", req)
			return nil
		},
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"open", "--dry-run", "-v"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	for _, want := range []string{
		"docker build -t erunpaas/tenant-a-devops:1.1.0",
		"docker run --rm -it",
		"ERUN_KUBERNETES_CONTEXT=cluster-dev",
		"ERUN_SHELL_HOST=tenant-a-local",
		"erunpaas/tenant-a-devops:1.1.0 shell",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run trace to contain %q, got %q", want, output)
		}
	}
}

func TestOpenCommandLaunchesShellWithDefaultTenantAndRequestedEnvironment(t *testing.T) {
	repoPath := t.TempDir()
	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath:   repoPath,
			toolConfig: common.ERunConfig{DefaultTenant: "tenant-a"},
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "dev"})

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
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "dev",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:     "dev",
		RepoPath: projectRoot,
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		Store: common.ConfigStore{},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			t.Fatalf("unexpected prompt: %+v", prompt)
			return "", nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			if fmt.Sprint(prompt.Label) != fmt.Sprintf("Kubernetes context for environment %q in tenant %q", "dev", "tenant-a") {
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
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("unexpected shell launch: %+v", req)
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	envConfig, _, err := common.LoadEnvConfig("tenant-a", "dev")
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
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "dev",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		Store: common.ConfigStore{},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			if fmt.Sprint(prompt.Label) == fmt.Sprintf("Container registry for environment %q in tenant %q", "dev", "tenant-a") {
				return "", nil
			}
			return "y", nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			if fmt.Sprint(prompt.Label) != fmt.Sprintf("Kubernetes context for environment %q in tenant %q", "dev", "tenant-a") {
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
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("unexpected shell launch: %+v", req)
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	envConfig, _, err := common.LoadEnvConfig("tenant-a", "dev")
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

	launched := common.ShellLaunchParams{}
	deployed := common.HelmDeployParams{}
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{repoPath: repoPath},
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			if req.Name != "tenant-a-devops" || req.Namespace != "tenant-a-dev" || req.KubernetesContext != "cluster-dev" {
				t.Fatalf("unexpected deployment check: %+v", req)
			}
			return false, nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			deployed = req
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"open", "tenant-a", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if deployed.ReleaseName != "tenant-a-devops" || deployed.ChartPath != chartPath {
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
	toolConfig common.ERunConfig
}

func (s openCommandStore) LoadERunConfig() (common.ERunConfig, string, error) {
	return s.toolConfig, "", nil
}

func (openCommandStore) SaveERunConfig(common.ERunConfig) error {
	return nil
}

func (openCommandStore) ListTenantConfigs() ([]common.TenantConfig, error) {
	return nil, nil
}

func (s openCommandStore) LoadTenantConfig(tenant string) (common.TenantConfig, string, error) {
	return common.TenantConfig{
		Name:               tenant,
		ProjectRoot:        s.repoPath,
		DefaultEnvironment: "local",
	}, "", nil
}

func (openCommandStore) SaveTenantConfig(common.TenantConfig) error {
	return nil
}

func (s openCommandStore) LoadEnvConfig(tenant, env string) (common.EnvConfig, string, error) {
	return common.EnvConfig{
		Name:              env,
		RepoPath:          s.repoPath,
		KubernetesContext: "cluster-dev",
	}, "", nil
}

func (openCommandStore) SaveEnvConfig(string, common.EnvConfig) error {
	return nil
}
