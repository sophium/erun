package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

func TestNewRootCmdRegistersCommands(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{})

	for _, name := range []string{"init", "open", "devops", "mcp", "app", "list", "release", "version"} {
		found, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find(%q) failed: %v", name, err)
		}
		if found == nil || found.Name() != name {
			t.Fatalf("expected command %q to be registered", name)
		}
	}
}

func TestResolveRuntimeDeploySpecForOpenFallsBackToCurrentBuildVersionForRemoteRepo(t *testing.T) {
	spec, err := resolveRuntimeDeploySpecForOpen(
		common.ConfigStore{},
		common.FindProjectRoot,
		common.ResolveDockerBuildContext,
		common.ResolveKubernetesDeployContext,
		nil,
		common.BuildInfo{Version: "1.0.31"},
		common.OpenResult{
			Tenant:      "erun",
			Environment: "remote",
			RepoPath:    "/home/erun/git/erun",
			TenantConfig: common.TenantConfig{
				Name:   "erun",
				Remote: true,
			},
			EnvConfig: common.EnvConfig{
				Name:              "remote",
				RepoPath:          "/home/erun/git/erun",
				KubernetesContext: "rancher-desktop",
				Remote:            true,
			},
		},
	)
	if err != nil {
		t.Fatalf("resolveRuntimeDeploySpecForOpen failed: %v", err)
	}
	if spec.Deploy.Version != "1.0.31" {
		t.Fatalf("expected remote default runtime deploy version fallback, got %+v", spec.Deploy)
	}
}

func TestRootCommandRunsInitWhenNoSubcommand(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	var promptLabels []string
	var selectLabels []string
	ensuredContext := ""
	ensuredNamespace := ""
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			label := fmt.Sprint(prompt.Label)
			promptLabels = append(promptLabels, label)
			if label == fmt.Sprintf("Container registry for environment %q in tenant %q", common.DefaultEnvironment, "tenant-a") {
				return "", nil
			}
			return "y", nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			selectLabels = append(selectLabels, fmt.Sprint(prompt.Label))
			return 0, "cluster-local", nil
		},
		ListKubernetesContexts: func() ([]string, error) {
			return []string{"cluster-local", "cluster-prod"}, nil
		},
		EnsureKubernetesNamespace: func(contextName, namespace string) error {
			ensuredContext = contextName
			ensuredNamespace = namespace
			return nil
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := buf.String(); got != "" {
		t.Fatalf("expected no command output, got %q", got)
	}

	erunConfig, _, err := common.LoadERunConfig()
	if err != nil {
		t.Fatalf("LoadERunConfig failed: %v", err)
	}
	if erunConfig.DefaultTenant != "tenant-a" {
		t.Fatalf("unexpected default tenant: %+v", erunConfig)
	}

	tenantConfig, _, err := common.LoadTenantConfig("tenant-a")
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}
	if tenantConfig.ProjectRoot != projectRoot || tenantConfig.DefaultEnvironment != common.DefaultEnvironment {
		t.Fatalf("unexpected tenant config: %+v", tenantConfig)
	}

	envConfig, _, err := common.LoadEnvConfig("tenant-a", common.DefaultEnvironment)
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if envConfig.Name != common.DefaultEnvironment || envConfig.RepoPath != projectRoot {
		t.Fatalf("unexpected env config: %+v", envConfig)
	}
	if envConfig.KubernetesContext != "cluster-local" {
		t.Fatalf("unexpected kubernetes context: %+v", envConfig)
	}

	projectConfig, _, err := common.LoadProjectConfig(projectRoot)
	if err != nil {
		t.Fatalf("LoadProjectConfig failed: %v", err)
	}
	if got := projectConfig.ContainerRegistryForEnvironment(common.DefaultEnvironment); got != common.DefaultContainerRegistry {
		t.Fatalf("unexpected project config: %+v", projectConfig)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "tenant-a-devops", "docker", "tenant-a-devops", "Dockerfile")); err != nil {
		t.Fatalf("expected tenant devops module to be created: %v", err)
	}

	wantPromptLabels := []string{
		fmt.Sprintf("Initialize tenant %q (path: %s) as the default tenant?", "tenant-a", projectRoot),
		fmt.Sprintf("Initialize default environment %q for tenant %q?", common.DefaultEnvironment, "tenant-a"),
		fmt.Sprintf("Container registry for environment %q in tenant %q", common.DefaultEnvironment, "tenant-a"),
	}
	if len(promptLabels) != len(wantPromptLabels) {
		t.Fatalf("unexpected prompts: %+v", promptLabels)
	}
	for i := range wantPromptLabels {
		if promptLabels[i] != wantPromptLabels[i] {
			t.Fatalf("unexpected prompt %d: got %q want %q", i, promptLabels[i], wantPromptLabels[i])
		}
	}
	if len(selectLabels) != 1 || selectLabels[0] != fmt.Sprintf("Kubernetes context for environment %q in tenant %q", common.DefaultEnvironment, "tenant-a") {
		t.Fatalf("unexpected select prompts: %+v", selectLabels)
	}
	if ensuredContext != "cluster-local" || ensuredNamespace != "tenant-a-local" {
		t.Fatalf("unexpected namespace ensure request: context=%q namespace=%q", ensuredContext, ensuredNamespace)
	}
}

func TestRootCommandRunsOpenWithDefaults(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
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
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "dev", RepoPath: projectRoot, KubernetesContext: "cluster-dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			t.Fatal("unexpected project detection")
			return "", "", nil
		},
		PromptRunner: func(promptui.Prompt) (string, error) {
			t.Fatal("unexpected prompt")
			return "", nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if launched.Dir != projectRoot || launched.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if launched.Namespace != "tenant-a-dev" || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected remote shell target: %+v", launched)
	}
}

func TestRootCommandRunsOpenWithDefaultTenantAndRequestedEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "tenant-a-dev")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "dev", RepoPath: projectRoot, KubernetesContext: "cluster-dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			t.Fatal("unexpected project detection")
			return "", "", nil
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			t.Fatalf("unexpected confirmation: %+v", prompt)
			return "", nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if launched.Dir != projectRoot || launched.Title != "tenant-a-dev" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if launched.Namespace != "tenant-a-dev" || launched.KubernetesContext != "cluster-dev" {
		t.Fatalf("unexpected remote shell target: %+v", launched)
	}
}

func TestRootCommandRunsOpenWithExplicitTenantAndEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "dog-me")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "dog",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("dog", common.EnvConfig{Name: "me", RepoPath: projectRoot, KubernetesContext: "cluster-me"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"dog", "me"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if launched.Dir != projectRoot || launched.Title != "dog-me" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
	if launched.Namespace != "dog-me" || launched.KubernetesContext != "cluster-me" {
		t.Fatalf("unexpected remote shell target: %+v", launched)
	}
}

func TestRootCommandRunsInitWhenOpenEnvironmentLacksKubernetesContext(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "tenant-a-dev")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
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
		FindProjectRoot: func() (string, string, error) {
			t.Fatal("unexpected project detection")
			return "", "", nil
		},
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
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	envConfig, _, err := common.LoadEnvConfig("tenant-a", "dev")
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if envConfig.KubernetesContext != "cluster-dev" {
		t.Fatalf("expected kubernetes context to be stored, got %+v", envConfig)
	}
}

func TestRootCommandExplicitTenantFailsWhenMissing(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	cmd := newTestRootCmd(testRootDeps{})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"dog", "me"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing tenant error")
	}
	if !errors.Is(err, common.ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
	if got := err.Error(); !bytes.Contains([]byte(got), []byte("no such tenant exists")) {
		t.Fatalf("expected missing tenant message, got %q", got)
	}
}

func TestInitCommandPreservesExistingTenantDefaultEnvironmentWhenFlagOmitted(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "prod",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "prod", RepoPath: projectRoot, KubernetesContext: "cluster-prod"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		PromptRunner: func(promptui.Prompt) (string, error) {
			t.Fatal("unexpected prompt")
			return "", nil
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"init", "-y"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := buf.String(); got != "" {
		t.Fatalf("expected no command output, got %q", got)
	}

	tenantConfig, _, err := common.LoadTenantConfig("tenant-a")
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}
	if tenantConfig.DefaultEnvironment != "prod" {
		t.Fatalf("expected tenant default environment to remain prod, got %+v", tenantConfig)
	}

	envConfig, _, err := common.LoadEnvConfig("tenant-a", "prod")
	if err != nil {
		t.Fatalf("LoadEnvConfig(prod) failed: %v", err)
	}
	if envConfig.Name != "prod" {
		t.Fatalf("unexpected env config: %+v", envConfig)
	}

	if _, _, err := common.LoadEnvConfig("tenant-a", common.DefaultEnvironment); !errors.Is(err, common.ErrNotInitialized) {
		t.Fatalf("expected no implicit %q env config, got %v", common.DefaultEnvironment, err)
	}
}

func TestInitCommandDecliningDefaultTenantStillInitializes(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	promptLabels := make([]string, 0, 3)
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "petios", projectRoot, nil
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			label := fmt.Sprint(prompt.Label)
			promptLabels = append(promptLabels, label)
			if prompt.IsConfirm && label == fmt.Sprintf("Initialize tenant %q (path: %s) as the default tenant?", "petios", projectRoot) {
				return "n", nil
			}
			if prompt.IsConfirm {
				return "y", nil
			}
			return "", nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			if fmt.Sprint(prompt.Label) != fmt.Sprintf("Kubernetes context for environment %q in tenant %q", "review", "petios") {
				t.Fatalf("unexpected select prompt: %+v", prompt)
			}
			return 0, "cluster-review", nil
		},
		ListKubernetesContexts: func() ([]string, error) {
			return []string{"cluster-review"}, nil
		},
		EnsureKubernetesNamespace: func(contextName, namespace string) error {
			if contextName != "cluster-review" || namespace != "petios-review" {
				t.Fatalf("unexpected namespace ensure request: context=%q namespace=%q", contextName, namespace)
			}
			return nil
		},
	})
	cmd.SetArgs([]string{"init", "--tenant", "petios", "--environment", "review"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if _, _, err := common.LoadERunConfig(); !errors.Is(err, common.ErrNotInitialized) {
		t.Fatalf("expected erun config to remain absent, got %v", err)
	}
	tenantConfig, _, err := common.LoadTenantConfig("petios")
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}
	if tenantConfig.ProjectRoot != projectRoot || tenantConfig.DefaultEnvironment != "review" {
		t.Fatalf("unexpected tenant config: %+v", tenantConfig)
	}
	envConfig, _, err := common.LoadEnvConfig("petios", "review")
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if envConfig.KubernetesContext != "cluster-review" || envConfig.RepoPath != projectRoot {
		t.Fatalf("unexpected env config: %+v", envConfig)
	}
	wantPromptLabels := []string{
		fmt.Sprintf("Initialize tenant %q (path: %s) as the default tenant?", "petios", projectRoot),
		fmt.Sprintf("Initialize default environment %q for tenant %q?", "review", "petios"),
		fmt.Sprintf("Container registry for environment %q in tenant %q", "review", "petios"),
	}
	if len(promptLabels) != len(wantPromptLabels) {
		t.Fatalf("unexpected prompts: %+v", promptLabels)
	}
	for i := range wantPromptLabels {
		if promptLabels[i] != wantPromptLabels[i] {
			t.Fatalf("unexpected prompt %d: got %q want %q", i, promptLabels[i], wantPromptLabels[i])
		}
	}
}

func TestRootCommandHelpFlagPrintsHelp(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	for _, want := range []string{"init", "open", "devops", "mcp", "version"} {
		if !bytes.Contains([]byte(output), []byte(want)) {
			t.Fatalf("expected help output to mention %q, got %q", want, output)
		}
	}
	for _, want := range []string{
		"-v",
		"--dry-run",
		"print trace logs for command flow and side effects",
		"runs the same resolution flow but skips mutating operations",
	} {
		if !bytes.Contains([]byte(output), []byte(want)) {
			t.Fatalf("expected help output to mention %q, got %q", want, output)
		}
	}
}

func TestRootCommandDryRunOpensByPlanningWithoutLaunchingShell(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
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
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "dev", RepoPath: projectRoot, KubernetesContext: "cluster-dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		CheckKubernetesDeployment: func(req common.KubernetesDeploymentCheckParams) (bool, error) {
			return true, nil
		},
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("unexpected shell launch during dry-run: %+v", req)
			return nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stdout.String(); got != "" {
		t.Fatalf("expected no stdout output during dry-run, got %q", got)
	}
	if got := stderr.String(); !bytes.Contains([]byte(got), []byte("kubectl --context cluster-dev --namespace tenant-a-dev wait --for=condition=Available --timeout 2m0s deployment/tenant-a-devops")) {
		t.Fatalf("expected dry-run open trace, got %q", got)
	}
}

func TestRootCommandInitErrorsDoNotPrintHelp(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "", "", common.ErrNotInGitRepository
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if !errors.Is(err, common.ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}

	output := buf.String()
	for _, unwanted := range []string{"Available Commands:", "Usage:"} {
		if bytes.Contains([]byte(output), []byte(unwanted)) {
			t.Fatalf("did not expect help output on init error, got %q", output)
		}
	}
}

func TestInitCommandErrorsDoNotPrintHelp(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "", "", common.ErrNotInGitRepository
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"init"})

	err := cmd.Execute()
	if !errors.Is(err, common.ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}

	output := buf.String()
	for _, unwanted := range []string{"Available Commands:", "Usage:"} {
		if bytes.Contains([]byte(output), []byte(unwanted)) {
			t.Fatalf("did not expect help output on init error, got %q", output)
		}
	}
}

func TestRootCommandInitErrorsDoNotPrintErrorTwice(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "", "", common.ErrNotInGitRepository
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if !errors.Is(err, common.ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}
	output := buf.String()
	want := "erun config is not initialized. Run erun in project directory."
	if count := bytes.Count([]byte(output), []byte(want)); count != 1 {
		t.Fatalf("expected one logged error message, got %q", output)
	}
	if bytes.Contains([]byte(output), []byte("Error:")) {
		t.Fatalf("did not expect Cobra error prefix, got %q", output)
	}
}

func TestConfirmPromptDefaultAndErrors(t *testing.T) {
	run := func(result string, err error) PromptRunner {
		return func(prompt promptui.Prompt) (string, error) {
			return result, err
		}
	}

	if ok, err := confirmPrompt(run("", nil), "label"); err != nil || !ok {
		t.Fatalf("expected default confirmation, got %v %v", ok, err)
	}

	if ok, err := confirmPrompt(run("n", nil), "label"); err != nil || ok {
		t.Fatalf("expected rejection, got %v %v", ok, err)
	}

	if ok, err := confirmPrompt(run("", promptui.ErrAbort), "label"); err != nil || ok {
		t.Fatalf("expected abort to be treated as rejection, got %v %v", ok, err)
	}

	if ok, err := confirmPrompt(run("", promptui.ErrInterrupt), "label"); err == nil || ok {
		t.Fatalf("expected interrupt error, got %v %v", ok, err)
	}

	expectedErr := errors.New("boom")
	if ok, err := confirmPrompt(run("", expectedErr), "label"); !errors.Is(err, expectedErr) || ok {
		t.Fatalf("expected original error, got %v %v", ok, err)
	}
}

func TestExecuteReturnsUnderlyingError(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "", "", common.ErrNotInGitRepository
		},
		PromptRunner: func(promptui.Prompt) (string, error) {
			return "", nil
		},
	})
	cmd.SetArgs([]string{})
	err := func() error {
		return cmd.Execute()
	}()
	if !errors.Is(err, common.ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}
}

func setupRootCmdTestConfigHome(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	previousValue, hadPreviousValue := os.LookupEnv("XDG_CONFIG_HOME")
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		t.Fatalf("set XDG_CONFIG_HOME: %v", err)
	}
	xdg.Reload()
	t.Cleanup(func() {
		var err error
		if hadPreviousValue {
			err = os.Setenv("XDG_CONFIG_HOME", previousValue)
		} else {
			err = os.Unsetenv("XDG_CONFIG_HOME")
		}
		if err != nil {
			t.Fatalf("restore XDG_CONFIG_HOME: %v", err)
		}
		xdg.Reload()
	})
}
