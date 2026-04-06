package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/opener"
	"gopkg.in/yaml.v3"
)

func TestNewRootCmdRegistersDevopsK8sDeployCommand(t *testing.T) {
	cmd := NewRootCmd(Dependencies{
		ResolveKubernetesDeployContext: func() (KubernetesDeployContext, error) {
			return KubernetesDeployContext{Dir: t.TempDir()}, nil
		},
	})

	found, _, err := cmd.Find([]string{"devops", "k8s", "deploy"})
	if err != nil {
		t.Fatalf("Find(devops k8s deploy) failed: %v", err)
	}
	if found == nil || found.Name() != "deploy" || found.Parent() == nil || found.Parent().Name() != "k8s" {
		t.Fatalf("expected devops k8s deploy command to be registered, got %+v", found)
	}
}

func TestNewRootCmdRegistersDeployShorthandWhenKubernetesDeployContextPresent(t *testing.T) {
	cmd := NewRootCmd(Dependencies{
		ResolveKubernetesDeployContext: func() (KubernetesDeployContext, error) {
			return KubernetesDeployContext{
				ComponentName: "erun-devops",
				ChartPath:     filepath.Join(t.TempDir(), "erun-devops", "k8s", "erun-devops"),
			}, nil
		},
	})

	if !hasSubcommand(cmd, "deploy") {
		t.Fatal("expected deploy shorthand command to be registered")
	}
}

func TestNewRootCmdOmitsDeployShorthandWhenKubernetesDeployContextAbsent(t *testing.T) {
	cmd := NewRootCmd(Dependencies{
		ResolveKubernetesDeployContext: func() (KubernetesDeployContext, error) {
			return KubernetesDeployContext{Dir: t.TempDir()}, nil
		},
	})

	if hasSubcommand(cmd, "deploy") {
		t.Fatal("did not expect deploy shorthand command to be registered")
	}
}

func TestDevopsK8sDeployBuildsAndDeploysSameExactVersionFromCurrentBuildDirectoryForLocalEnvironment(t *testing.T) {
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

	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	var built DockerBuildRequest
	var pushed DockerPushRequest
	var received HelmDeployRequest
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			built = req
			return nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			pushed = req
			return nil
		},
		DeployHelmChart: func(req HelmDeployRequest) error {
			received = req
			return nil
		},
		Now: func() time.Time {
			return time.Date(2026, time.April, 6, 12, 30, 0, 0, time.UTC)
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"devops", "k8s", "deploy", "erun-devops"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if received.ReleaseName != "erun-devops" {
		t.Fatalf("unexpected release name: %+v", received)
	}
	if received.ChartPath != chartPath {
		t.Fatalf("unexpected chart path: %+v", received)
	}
	if received.ValuesFilePath != filepath.Join(chartPath, "values.local.yaml") {
		t.Fatalf("unexpected values file path: %+v", received)
	}
	if received.Namespace != "tenant-a-local" {
		t.Fatalf("unexpected namespace: %+v", received)
	}
	if received.KubernetesContext != "cluster-local" {
		t.Fatalf("unexpected kubernetes context: %+v", received)
	}
	if received.WorktreeHostPath != projectRoot {
		t.Fatalf("unexpected worktree values: %+v", received)
	}
	if received.Timeout != defaultHelmDeploymentTimeout {
		t.Fatalf("unexpected timeout: %+v", received)
	}
	if built.Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected build request: %+v", built)
	}
	if pushed.Tag != built.Tag {
		t.Fatalf("expected push to use built tag, got build=%+v push=%+v", built, pushed)
	}
	if received.Version != "1.1.0" {
		t.Fatalf("unexpected chart version override: %+v", received)
	}
	if received.Stdout != stdout || received.Stderr != stderr {
		t.Fatalf("unexpected output writers: %+v", received)
	}
}

func TestRootDeployShorthandUsesCurrentComponentContext(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	fixedNow := time.Date(2026, time.April, 6, 13, 16, 30, 0, time.UTC)
	var built DockerBuildRequest
	var pushed DockerPushRequest
	var received HelmDeployRequest
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (KubernetesDeployContext, error) {
			return KubernetesDeployContext{
				Dir:           workdir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			built = req
			return nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			pushed = req
			return nil
		},
		DeployHelmChart: func(req HelmDeployRequest) error {
			if built.Tag == "" || pushed.Tag == "" {
				t.Fatal("expected build and push to run before deploy")
			}
			received = req
			return nil
		},
		Now: func() time.Time {
			return fixedNow
		},
	})
	cmd.SetArgs([]string{"deploy"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if built.Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected build request: %+v", built)
	}
	if pushed.Tag != built.Tag {
		t.Fatalf("expected push to use built tag, got build=%+v push=%+v", built, pushed)
	}
	if received.ReleaseName != "erun-devops" || received.ChartPath != chartPath {
		t.Fatalf("unexpected deploy request: %+v", received)
	}
	if received.Version != "1.1.0" {
		t.Fatalf("unexpected chart version override: %+v", received)
	}
}

func TestRootDeployShorthandDryRunPrintsBuildAndDeployCommandsWithoutExecuting(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (KubernetesDeployContext, error) {
			return KubernetesDeployContext{
				Dir:           workdir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			t.Fatalf("unexpected build request during dry-run: %+v", req)
			return nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			t.Fatalf("unexpected push request during dry-run: %+v", req)
			return nil
		},
		DeployHelmChart: func(req HelmDeployRequest) error {
			t.Fatalf("unexpected deploy request during dry-run: %+v", req)
			return nil
		},
		Now: func() time.Time {
			return time.Date(2026, time.April, 6, 13, 16, 30, 0, time.UTC)
		},
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"deploy", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	if !strings.Contains(output, "[dry-run] docker build -t erunpaas/erun-devops:1.1.0") {
		t.Fatalf("expected dry-run build trace, got %q", output)
	}
	if !strings.Contains(output, "[dry-run] docker push erunpaas/erun-devops:1.1.0") {
		t.Fatalf("expected dry-run push trace, got %q", output)
	}
	if !strings.Contains(output, "[dry-run] helm upgrade --install --wait --wait-for-jobs --timeout 2m0s --namespace tenant-a-local --kube-context cluster-local -f "+filepath.Join(chartPath, "values.local.yaml")) {
		t.Fatalf("expected dry-run deploy trace, got %q", output)
	}
	if strings.Contains(output, "decision:") || strings.Contains(output, "chart version override=") {
		t.Fatalf("did not expect decision notes without -v during dry-run, got %q", output)
	}
}

func TestRootDeployShorthandBuildsAndPushesLiteralChartImageDependencies(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	templatePath := filepath.Join(chartPath, "templates", "deployment.yaml")
	if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
		t.Fatalf("mkdir templates dir: %v", err)
	}
	if err := os.WriteFile(templatePath, []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: erun-devops\nspec:\n  template:\n    spec:\n      containers:\n        - image: erunpaas/erun-dind:28.1.1-dind\n          name: dind\n"), 0o644); err != nil {
		t.Fatalf("write deployment template: %v", err)
	}

	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write main Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write main VERSION: %v", err)
	}

	dindDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-dind")
	if err := os.MkdirAll(dindDir, 0o755); err != nil {
		t.Fatalf("mkdir dind dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dindDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write dind Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dindDir, "VERSION"), []byte("28.1.1-dind\n"), 0o644); err != nil {
		t.Fatalf("write dind VERSION: %v", err)
	}

	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	var builds []DockerBuildRequest
	var pushes []DockerPushRequest
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (KubernetesDeployContext, error) {
			return KubernetesDeployContext{
				Dir:           workdir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			builds = append(builds, req)
			return nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			pushes = append(pushes, req)
			return nil
		},
		DeployHelmChart: func(req HelmDeployRequest) error {
			return nil
		},
		Now: func() time.Time {
			return time.Date(2026, time.April, 6, 13, 16, 30, 0, time.UTC)
		},
	})
	cmd.SetArgs([]string{"deploy"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(builds) != 2 {
		t.Fatalf("expected 2 builds, got %+v", builds)
	}
	if len(pushes) != 2 {
		t.Fatalf("expected 2 pushes, got %+v", pushes)
	}
	if builds[0].Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected primary build: %+v", builds[0])
	}
	if builds[1].Tag != "erunpaas/erun-dind:28.1.1-dind" {
		t.Fatalf("unexpected dependency build: %+v", builds[1])
	}
	if pushes[0].Tag != builds[0].Tag || pushes[1].Tag != builds[1].Tag {
		t.Fatalf("expected pushes to match builds, got builds=%+v pushes=%+v", builds, pushes)
	}
}

func TestFindLiteralDockerImagesInChartReadsDashPrefixedImageEntries(t *testing.T) {
	chartPath := filepath.Join(t.TempDir(), "erun-devops")
	templatePath := filepath.Join(chartPath, "templates", "deployment.yaml")
	if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
		t.Fatalf("mkdir templates dir: %v", err)
	}
	if err := os.WriteFile(templatePath, []byte("apiVersion: apps/v1\nkind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n        - image: erunpaas/erun-dind:28.1.1-dind\n          name: dind\n        - image: erunpaas/erun-devops:{{ .Chart.AppVersion }}\n          name: erun-devops\n"), 0o644); err != nil {
		t.Fatalf("write deployment template: %v", err)
	}

	images, err := findLiteralDockerImagesInChart(chartPath)
	if err != nil {
		t.Fatalf("findLiteralDockerImagesInChart failed: %v", err)
	}

	if len(images) != 1 || images[0] != "erunpaas/erun-dind:28.1.1-dind" {
		t.Fatalf("unexpected images: %+v", images)
	}
}

func TestRootCommandTreatsDeployAsEnvironmentWhenDeployContextAbsent(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "tenant-a-deploy")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "deploy",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{
		Name:              "deploy",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-deploy",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := opener.ShellLaunchRequest{}
	cmd := NewRootCmd(Dependencies{
		ResolveKubernetesDeployContext: func() (KubernetesDeployContext, error) {
			return KubernetesDeployContext{Dir: t.TempDir()}, nil
		},
		DeployHelmChart: func(req HelmDeployRequest) error {
			t.Fatalf("unexpected deploy request: %+v", req)
			return nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"deploy"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if launched.Dir != projectRoot || launched.Title != "tenant-a-deploy" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
}

func TestDefaultKubernetesDeployContextResolverDetectsDockerComponentDir(t *testing.T) {
	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}

	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDir) })
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	result, err := defaultKubernetesDeployContextResolver()
	if err != nil {
		t.Fatalf("defaultKubernetesDeployContextResolver failed: %v", err)
	}

	resolvedChartPath, err := filepath.EvalSymlinks(chartPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(chartPath) failed: %v", err)
	}
	if result.ComponentName != "erun-devops" || result.ChartPath != resolvedChartPath {
		t.Fatalf("unexpected deploy context: %+v", result)
	}
}

func TestDefaultKubernetesDeployContextResolverDetectsK8sComponentDir(t *testing.T) {
	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")

	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDir) })
	if err := os.Chdir(chartPath); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	result, err := defaultKubernetesDeployContextResolver()
	if err != nil {
		t.Fatalf("defaultKubernetesDeployContextResolver failed: %v", err)
	}

	resolvedChartPath, err := filepath.EvalSymlinks(chartPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(chartPath) failed: %v", err)
	}
	if result.ComponentName != "erun-devops" || result.ChartPath != resolvedChartPath {
		t.Fatalf("unexpected deploy context: %+v", result)
	}
}

func TestPrepareHelmChartForDeployOverridesVersionAndAppVersion(t *testing.T) {
	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")

	preparedChartPath, cleanup, err := prepareHelmChartForDeploy(chartPath, "1.0.0-snapshot-20260406123000")
	if err != nil {
		t.Fatalf("prepareHelmChartForDeploy failed: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(preparedChartPath, "Chart.yaml"))
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var chart struct {
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal(data, &chart); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	if chart.Version != "1.0.0-snapshot-20260406123000" {
		t.Fatalf("unexpected chart version: %+v", chart)
	}
	if chart.AppVersion != "1.0.0-snapshot-20260406123000" {
		t.Fatalf("unexpected chart appVersion: %+v", chart)
	}
}

func createHelmChartFixture(t *testing.T, projectRoot, componentName string) string {
	t.Helper()

	chartPath := filepath.Join(projectRoot, "erun-devops", "k8s", componentName)
	if err := os.MkdirAll(chartPath, 0o755); err != nil {
		t.Fatalf("mkdir chart dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: "+componentName+"\nversion: 1.0.0\nappVersion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartPath, "values.local.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.local.yaml: %v", err)
	}
	return chartPath
}
