package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

type deployBuildCall struct {
	Dir            string
	DockerfilePath string
	Tag            string
	Platforms      []string
	Push           bool
	Stdout         io.Writer
	Stderr         io.Writer
}

type deployPushCall struct {
	Tag    string
	Stdout io.Writer
	Stderr io.Writer
}

func deployBuildCallFunc(run func(deployBuildCall) error) common.DockerImageBuilderFunc {
	return func(buildInput common.DockerBuildSpec, stdout, stderr io.Writer) error {
		return run(deployBuildCall{
			Dir:            buildInput.ContextDir,
			DockerfilePath: buildInput.DockerfilePath,
			Tag:            buildInput.Image.Tag,
			Platforms:      append([]string{}, buildInput.Platforms...),
			Push:           buildInput.Push,
			Stdout:         stdout,
			Stderr:         stderr,
		})
	}
}

func deployPushCallFunc(run func(deployPushCall) error) common.DockerImagePusherFunc {
	return func(tag string, stdout, stderr io.Writer) error {
		return run(deployPushCall{
			Tag:    tag,
			Stdout: stdout,
			Stderr: stderr,
		})
	}
}

func TestNewRootCmdRegistersDevopsK8sDeployCommand(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{Dir: t.TempDir()}, nil
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

func TestDeployHelpShowsTenantAndEnvironmentFlags(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				ComponentName: "erun-devops",
				ChartPath:     filepath.Join(t.TempDir(), "erun-devops", "k8s", "erun-devops"),
			}, nil
		},
	})
	stdout := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"deploy", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"--tenant string",
		"Deploy for a specific tenant",
		"--environment string",
		"Deploy for a specific environment; requires --tenant",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected deploy help to contain %q, got:\n%s", want, output)
		}
	}
	if strings.Contains(output, "--repo-path") {
		t.Fatalf("expected repo-path to remain hidden, got:\n%s", output)
	}
}

func TestNewRootCmdRegistersDeployShorthandWhenKubernetesDeployContextPresent(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				ComponentName: "erun-devops",
				ChartPath:     filepath.Join(t.TempDir(), "erun-devops", "k8s", "erun-devops"),
			}, nil
		},
	})

	if !hasSubcommand(cmd, "deploy") {
		t.Fatal("expected deploy shorthand command to be registered")
	}
}

func TestNewRootCmdRegistersDeployShorthandAtProjectRootWhenDevopsK8sScopePresent(t *testing.T) {
	projectRoot := t.TempDir()
	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops", "k8s", "tenant-a-devops")
	if err := os.MkdirAll(moduleRoot, 0o755); err != nil {
		t.Fatalf("mkdir chart dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "Chart.yaml"), []byte("apiVersion: v2\nname: tenant-a-devops\nversion: 1.0.0\nappVersion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "values.local.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.local.yaml: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{Dir: projectRoot}, nil
		},
	})

	if !hasSubcommand(cmd, "deploy") {
		t.Fatal("expected deploy shorthand command to be registered")
	}
}

func TestNewRootCmdOmitsDeployShorthandWhenKubernetesDeployContextAbsent(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{Dir: t.TempDir()}, nil
		},
	})

	if hasSubcommand(cmd, "deploy") {
		t.Fatal("did not expect deploy shorthand command to be registered")
	}
}

func TestDevopsK8sDeployBuildsAndDeploysSameExactVersionFromCurrentBuildDirectoryForLocalEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	stubKubectlContexts(t, []string{"cluster-local"}, "cluster-local")

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

	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	var built deployBuildCall
	var pushed deployPushCall
	var received common.HelmDeployParams
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		BuildDockerImage: deployBuildCallFunc(func(req deployBuildCall) error {
			built = req
			return nil
		}),
		PushDockerImage: deployPushCallFunc(func(req deployPushCall) error {
			pushed = req
			return nil
		}),
		DeployHelmChart: func(req common.HelmDeployParams) error {
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
	if received.Tenant != "tenant-a" || received.Environment != "local" {
		t.Fatalf("unexpected tenant/environment: %+v", received)
	}
	if received.Namespace != "tenant-a-local" {
		t.Fatalf("unexpected namespace: %+v", received)
	}
	if received.KubernetesContext != "cluster-local" {
		t.Fatalf("unexpected kubernetes context: %+v", received)
	}
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks(projectRoot) failed: %v", err)
	}
	if received.WorktreeHostPath != resolvedProjectRoot {
		t.Fatalf("unexpected worktree values: %+v", received)
	}
	if received.Timeout != common.DefaultHelmDeploymentTimeout {
		t.Fatalf("unexpected timeout: %+v", received)
	}
	if built.Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected build request: %+v", built)
	}
	if !built.Push || !reflect.DeepEqual(built.Platforms, []string{"linux/amd64", "linux/arm64"}) {
		t.Fatalf("expected multi-platform buildx push request, got %+v", built)
	}
	if pushed.Tag != "" {
		t.Fatalf("did not expect separate push for buildx deploy, got build=%+v push=%+v", built, pushed)
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

	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	fixedNow := time.Date(2026, time.April, 6, 13, 16, 30, 0, time.UTC)
	var built deployBuildCall
	var pushed deployPushCall
	var received common.HelmDeployParams
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				Dir:           workdir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: deployBuildCallFunc(func(req deployBuildCall) error {
			built = req
			return nil
		}),
		PushDockerImage: deployPushCallFunc(func(req deployPushCall) error {
			pushed = req
			return nil
		}),
		DeployHelmChart: func(req common.HelmDeployParams) error {
			if built.Tag == "" {
				t.Fatal("expected build to run before deploy")
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
	if !built.Push || !reflect.DeepEqual(built.Platforms, []string{"linux/amd64", "linux/arm64"}) {
		t.Fatalf("expected multi-platform buildx push request, got %+v", built)
	}
	if pushed.Tag != "" {
		t.Fatalf("did not expect separate push for buildx deploy, got build=%+v push=%+v", built, pushed)
	}
	if received.ReleaseName != "erun-devops" || received.ChartPath != chartPath {
		t.Fatalf("unexpected deploy request: %+v", received)
	}
	if received.Version != "1.1.0" {
		t.Fatalf("unexpected chart version override: %+v", received)
	}
}

func TestRootDeployShorthandAtProjectRootDeploysAllComponents(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	chartA := filepath.Join(moduleRoot, "k8s", "tenant-a-devops")
	chartB := filepath.Join(moduleRoot, "k8s", "erun-dind")
	for _, chartPath := range []string{chartA, chartB} {
		if err := os.MkdirAll(chartPath, 0o755); err != nil {
			t.Fatalf("mkdir chart dir: %v", err)
		}
		componentName := filepath.Base(chartPath)
		if err := os.WriteFile(filepath.Join(chartPath, "Chart.yaml"), []byte("apiVersion: v2\nname: "+componentName+"\nversion: 1.0.0\nappVersion: 1.0.0\n"), 0o644); err != nil {
			t.Fatalf("write Chart.yaml: %v", err)
		}
		if err := os.WriteFile(filepath.Join(chartPath, "values.local.yaml"), nil, 0o644); err != nil {
			t.Fatalf("write values.local.yaml: %v", err)
		}
	}

	componentA := filepath.Join(moduleRoot, "docker", "tenant-a-devops")
	componentB := filepath.Join(moduleRoot, "docker", "erun-dind")
	for _, componentDir := range []string{componentA, componentB} {
		if err := os.MkdirAll(componentDir, 0o755); err != nil {
			t.Fatalf("mkdir docker dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatalf("write Dockerfile: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentA, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write component A VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentB, "VERSION"), []byte("28.1.1\n"), 0o644); err != nil {
		t.Fatalf("write component B VERSION: %v", err)
	}

	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	var builds []deployBuildCall
	var pushes []deployPushCall
	var deploys []common.HelmDeployParams
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{Dir: projectRoot}, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
		BuildDockerImage: deployBuildCallFunc(func(req deployBuildCall) error {
			builds = append(builds, req)
			return nil
		}),
		PushDockerImage: deployPushCallFunc(func(req deployPushCall) error {
			pushes = append(pushes, req)
			return nil
		}),
		DeployHelmChart: func(req common.HelmDeployParams) error {
			deploys = append(deploys, req)
			return nil
		},
	})
	cmd.SetArgs([]string{"deploy"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(builds) != 2 || len(pushes) != 0 || len(deploys) != 2 {
		t.Fatalf("unexpected execution counts: builds=%+v pushes=%+v deploys=%+v", builds, pushes, deploys)
	}
	if builds[0].Tag != "erunpaas/erun-dind:28.1.1" || builds[1].Tag != "erunpaas/tenant-a-devops:1.1.0" {
		t.Fatalf("unexpected builds: %+v", builds)
	}
	for _, build := range builds {
		if !build.Push || !reflect.DeepEqual(build.Platforms, []string{"linux/amd64", "linux/arm64"}) {
			t.Fatalf("expected multi-platform buildx push request, got %+v", build)
		}
	}
	if deploys[0].ReleaseName != "erun-dind" || deploys[0].ChartPath != chartB {
		t.Fatalf("unexpected first deploy: %+v", deploys[0])
	}
	if deploys[1].ReleaseName != "tenant-a-devops" || deploys[1].ChartPath != chartA {
		t.Fatalf("unexpected second deploy: %+v", deploys[1])
	}
}

func TestDeployCommandHiddenTargetOverrideUsesProvidedTenantEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	if err := os.WriteFile(filepath.Join(chartPath, "values.dev.yaml"), []byte("image:\n  repository: erunpaas/erun-devops\n  tag: latest\n"), 0o644); err != nil {
		t.Fatalf("write values.dev.yaml: %v", err)
	}
	componentDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save local env config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "dev",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-dev",
	}); err != nil {
		t.Fatalf("save dev env config: %v", err)
	}

	var deployed common.HelmDeployParams
	cmd := newTestRootCmd(testRootDeps{
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				Dir:           componentDir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            componentDir,
				DockerfilePath: filepath.Join(componentDir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: deployBuildCallFunc(func(deployBuildCall) error {
			return nil
		}),
		PushDockerImage: deployPushCallFunc(func(deployPushCall) error {
			return nil
		}),
		DeployHelmChart: func(req common.HelmDeployParams) error {
			deployed = req
			return nil
		},
	})
	cmd.SetArgs([]string{"deploy", "--tenant", "tenant-a", "--environment", "dev", "--repo-path", projectRoot})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if deployed.Namespace != "tenant-a-dev" || deployed.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected deploy target: %+v", deployed)
	}
	if deployed.Tenant != "tenant-a" || deployed.Environment != "dev" {
		t.Fatalf("unexpected deploy values: %+v", deployed)
	}
	if deployed.ValuesFilePath != filepath.Join(chartPath, "values.dev.yaml") {
		t.Fatalf("unexpected values file: %+v", deployed)
	}
}

func TestDeployCommandVersionOverrideUsesProvidedVersion(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	componentDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	var deployed common.HelmDeployParams
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				Dir:           componentDir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            componentDir,
				DockerfilePath: filepath.Join(componentDir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: deployBuildCallFunc(func(req deployBuildCall) error {
			t.Fatalf("unexpected build request: %+v", req)
			return nil
		}),
		PushDockerImage: deployPushCallFunc(func(req deployPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
		DeployHelmChart: func(req common.HelmDeployParams) error {
			deployed = req
			return nil
		},
	})
	cmd.SetArgs([]string{"deploy", "--version", "1.0.7"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if deployed.Version != "1.0.7" {
		t.Fatalf("unexpected deploy version: %+v", deployed)
	}
}

func TestDeployCommandUsesConfiguredKubernetesContextForLocalEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	stubKubectlContexts(t, []string{"cluster-local", "cluster-selected"}, "cluster-selected")

	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	componentDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	var deployed common.HelmDeployParams
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				Dir:           componentDir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            componentDir,
				DockerfilePath: filepath.Join(componentDir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: deployBuildCallFunc(func(req deployBuildCall) error {
			t.Fatalf("unexpected build request: %+v", req)
			return nil
		}),
		PushDockerImage: deployPushCallFunc(func(req deployPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
		DeployHelmChart: func(req common.HelmDeployParams) error {
			deployed = req
			return nil
		},
	})
	cmd.SetArgs([]string{"deploy", "--version", "1.0.7"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if deployed.KubernetesContext != "cluster-local" {
		t.Fatalf("expected deploy to keep configured kubernetes context, got %+v", deployed)
	}
}

func TestDeployCommandEnsuresNamespaceBeforeDeploy(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	stubKubectlContexts(t, []string{"cluster-local"}, "cluster-local")

	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	componentDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir docker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	ensuredContext := ""
	ensuredNamespace := ""
	deployed := false
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				Dir:           componentDir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            componentDir,
				DockerfilePath: filepath.Join(componentDir, "Dockerfile"),
			}, nil
		},
		EnsureKubernetesNamespace: func(contextName, namespace string) error {
			ensuredContext = contextName
			ensuredNamespace = namespace
			return nil
		},
		BuildDockerImage: deployBuildCallFunc(func(req deployBuildCall) error {
			t.Fatalf("unexpected build request: %+v", req)
			return nil
		}),
		PushDockerImage: deployPushCallFunc(func(req deployPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
		DeployHelmChart: func(req common.HelmDeployParams) error {
			if ensuredContext == "" || ensuredNamespace == "" {
				t.Fatalf("expected namespace ensure before deploy, got context=%q namespace=%q", ensuredContext, ensuredNamespace)
			}
			deployed = true
			return nil
		},
	})
	cmd.SetArgs([]string{"deploy", "--version", "1.0.7"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if ensuredContext != "cluster-local" || ensuredNamespace != "tenant-a-local" {
		t.Fatalf("unexpected namespace ensure request: context=%q namespace=%q", ensuredContext, ensuredNamespace)
	}
	if !deployed {
		t.Fatal("expected deploy to run")
	}
}

func TestRootDeployShorthandDryRunPrintsBuildAndDeployCommandsWithoutExecuting(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	stubKubectlContexts(t, []string{"cluster-local"}, "cluster-local")

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

	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				Dir:           workdir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: deployBuildCallFunc(func(req deployBuildCall) error {
			t.Fatalf("unexpected build request during dry-run: %+v", req)
			return nil
		}),
		PushDockerImage: deployPushCallFunc(func(req deployPushCall) error {
			t.Fatalf("unexpected push request during dry-run: %+v", req)
			return nil
		}),
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("unexpected deploy request during dry-run: %+v", req)
			return nil
		},
		Now: func() time.Time {
			return time.Date(2026, time.April, 6, 13, 16, 30, 0, time.UTC)
		},
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"deploy", "--dry-run", "-v"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	if !strings.Contains(output, "docker buildx build --builder erun-multiarch") {
		t.Fatalf("expected dry-run build trace, got %q", output)
	}
	if !strings.Contains(output, "--platform 'linux/amd64,linux/arm64'") || !strings.Contains(output, "--push") {
		t.Fatalf("expected multi-platform dry-run build trace, got %q", output)
	}
	if strings.Contains(output, "docker push erunpaas/erun-devops:1.1.0") {
		t.Fatalf("did not expect separate dry-run push trace, got %q", output)
	}
	if !strings.Contains(output, "helm upgrade --install --wait --wait-for-jobs --timeout 2m0s --namespace tenant-a-local --kube-context cluster-local -f "+filepath.Join(chartPath, "values.local.yaml")+" --set-string tenant=tenant-a --set-string environment=local") {
		t.Fatalf("expected dry-run deploy trace, got %q", output)
	}
	if strings.Contains(output, "decision:") || strings.Contains(output, "chart version override=") {
		t.Fatalf("did not expect decision notes during dry-run, got %q", output)
	}
}

func TestRootDeployShorthandUsesPersistedSnapshotPreferenceForLocalEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)
	stubKubectlContexts(t, []string{"cluster-local"}, "cluster-local")

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

	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	snapshot := false
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
		Snapshot:          &snapshot,
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				Dir:           workdir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: deployBuildCallFunc(func(req deployBuildCall) error {
			t.Fatalf("unexpected build request during dry-run: %+v", req)
			return nil
		}),
		PushDockerImage: deployPushCallFunc(func(req deployPushCall) error {
			t.Fatalf("unexpected push request during dry-run: %+v", req)
			return nil
		}),
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("unexpected deploy request during dry-run: %+v", req)
			return nil
		},
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"deploy", "--dry-run", "-v"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	if strings.Contains(output, "docker build -t") || strings.Contains(output, "docker push ") {
		t.Fatalf("did not expect dry-run snapshot build or push, got %q", output)
	}
	if !strings.Contains(output, "helm upgrade --install --wait --wait-for-jobs --timeout 2m0s --namespace tenant-a-local --kube-context cluster-local -f "+filepath.Join(chartPath, "values.local.yaml")+" --set-string tenant=tenant-a --set-string environment=local") {
		t.Fatalf("expected dry-run deploy trace, got %q", output)
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

	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	var builds []deployBuildCall
	var pushes []deployPushCall
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				Dir:           workdir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: deployBuildCallFunc(func(req deployBuildCall) error {
			builds = append(builds, req)
			return nil
		}),
		PushDockerImage: deployPushCallFunc(func(req deployPushCall) error {
			pushes = append(pushes, req)
			return nil
		}),
		DeployHelmChart: func(req common.HelmDeployParams) error {
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
	if len(pushes) != 0 {
		t.Fatalf("expected no separate pushes, got %+v", pushes)
	}
	if builds[0].Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected primary build: %+v", builds[0])
	}
	if builds[1].Tag != "erunpaas/erun-dind:28.1.1-dind" {
		t.Fatalf("unexpected dependency build: %+v", builds[1])
	}
	for _, build := range builds {
		if !build.Push || !reflect.DeepEqual(build.Platforms, []string{"linux/amd64", "linux/arm64"}) {
			t.Fatalf("expected multi-platform buildx push request, got %+v", build)
		}
	}
}

func TestRootCommandTreatsDeployAsEnvironmentWhenDeployContextAbsent(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "tenant-a-deploy")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "deploy",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              "deploy",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-deploy",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{Dir: t.TempDir()}, nil
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			if !prompt.IsConfirm {
				t.Fatalf("expected confirm prompt, got %+v", prompt)
			}
			if prompt.Label != fmt.Sprintf("create tenant-a-devops chart in %s", projectRoot) {
				t.Fatalf("unexpected prompt label: %q", prompt.Label)
			}
			return "n", nil
		},
		DeployHelmChart: func(req common.HelmDeployParams) error {
			t.Fatalf("unexpected deploy request: %+v", req)
			return nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
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
