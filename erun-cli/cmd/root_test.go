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
	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/sophium/erun/internal/opener"
)

func TestNewRootCmdRegistersCommands(t *testing.T) {
	cmd := NewRootCmd(Dependencies{})

	for _, name := range []string{"init", "open", "mcp", "version"} {
		found, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find(%q) failed: %v", name, err)
		}
		if found == nil || found.Name() != name {
			t.Fatalf("expected command %q to be registered", name)
		}
	}
}

func TestRootCommandRunsInitWhenNoSubcommand(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	var labels []string
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindCurrentBranch: func(root string) (string, error) {
			if root != projectRoot {
				t.Fatalf("unexpected project root: %s", root)
			}
			return "develop", nil
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			labels = append(labels, fmt.Sprint(prompt.Label))
			return "y", nil
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

	erunConfig, _, err := internal.LoadERunConfig()
	if err != nil {
		t.Fatalf("LoadERunConfig failed: %v", err)
	}
	if erunConfig.DefaultTenant != "tenant-a" {
		t.Fatalf("unexpected default tenant: %+v", erunConfig)
	}

	tenantConfig, _, err := internal.LoadTenantConfig("tenant-a")
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}
	if tenantConfig.ProjectRoot != projectRoot || tenantConfig.DefaultEnvironment != bootstrap.DefaultEnvironment {
		t.Fatalf("unexpected tenant config: %+v", tenantConfig)
	}

	envConfig, _, err := internal.LoadEnvConfig("tenant-a", bootstrap.DefaultEnvironment)
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if envConfig.Name != bootstrap.DefaultEnvironment || envConfig.RepoPath != projectRoot || envConfig.Branch != "develop" {
		t.Fatalf("unexpected env config: %+v", envConfig)
	}

	wantLabels := []string{
		bootstrap.TenantConfirmationLabel("tenant-a", projectRoot),
		bootstrap.EnvironmentConfirmationLabelWithBranch("tenant-a", bootstrap.DefaultEnvironment, "develop"),
	}
	if len(labels) != len(wantLabels) {
		t.Fatalf("unexpected confirmation labels: %+v", labels)
	}
	for i := range wantLabels {
		if labels[i] != wantLabels[i] {
			t.Fatalf("unexpected confirmation label %d: got %q want %q", i, labels[i], wantLabels[i])
		}
	}
}

func TestRootCommandRunsOpenWithDefaults(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
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
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{Name: "dev", RepoPath: projectRoot}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := opener.ShellLaunchRequest{}
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			t.Fatal("unexpected project detection")
			return "", "", nil
		},
		PromptRunner: func(promptui.Prompt) (string, error) {
			t.Fatal("unexpected prompt")
			return "", nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
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
}

func TestRootCommandRunsOpenWithDefaultTenantAndRequestedEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "tenant-a-dev")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: bootstrap.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{Name: "dev", RepoPath: projectRoot}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := opener.ShellLaunchRequest{}
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			t.Fatal("unexpected project detection")
			return "", "", nil
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			t.Fatalf("unexpected confirmation: %+v", prompt)
			return "", nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
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
}

func TestRootCommandRunsOpenWithExplicitTenantAndEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "dog-me")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "dog",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("dog", internal.EnvConfig{Name: "me", RepoPath: projectRoot}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := opener.ShellLaunchRequest{}
	cmd := NewRootCmd(Dependencies{
		LaunchShell: func(req opener.ShellLaunchRequest) error {
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
}

func TestRootCommandExplicitTenantFailsWhenMissing(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	cmd := NewRootCmd(Dependencies{})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"dog", "me"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing tenant error")
	}
	if !errors.Is(err, opener.ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
	if got := err.Error(); !bytes.Contains([]byte(got), []byte("no such tenant exists")) {
		t.Fatalf("expected missing tenant message, got %q", got)
	}
}

func TestInitCommandPreservesExistingTenantDefaultEnvironmentWhenFlagOmitted(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "prod",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{Name: "prod", RepoPath: projectRoot}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	cmd := NewRootCmd(Dependencies{
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

	tenantConfig, _, err := internal.LoadTenantConfig("tenant-a")
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}
	if tenantConfig.DefaultEnvironment != "prod" {
		t.Fatalf("expected tenant default environment to remain prod, got %+v", tenantConfig)
	}

	envConfig, _, err := internal.LoadEnvConfig("tenant-a", "prod")
	if err != nil {
		t.Fatalf("LoadEnvConfig(prod) failed: %v", err)
	}
	if envConfig.Name != "prod" {
		t.Fatalf("unexpected env config: %+v", envConfig)
	}

	if _, _, err := internal.LoadEnvConfig("tenant-a", bootstrap.DefaultEnvironment); !errors.Is(err, internal.ErrNotInitialized) {
		t.Fatalf("expected no implicit %q env config, got %v", bootstrap.DefaultEnvironment, err)
	}
}

func TestInitCommandStoresEnvironmentBranchFlag(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	cmd := NewRootCmd(Dependencies{
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
	cmd.SetArgs([]string{"init", "--branch", "develop", "-y"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	envConfig, _, err := internal.LoadEnvConfig("tenant-a", bootstrap.DefaultEnvironment)
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if envConfig.Branch != "develop" {
		t.Fatalf("expected branch to be stored, got %+v", envConfig)
	}
}

func TestInitCommandDetectsEnvironmentBranch(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindCurrentBranch: func(root string) (string, error) {
			if root != projectRoot {
				t.Fatalf("unexpected project root: %s", root)
			}
			return "develop", nil
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

	envConfig, _, err := internal.LoadEnvConfig("tenant-a", bootstrap.DefaultEnvironment)
	if err != nil {
		t.Fatalf("LoadEnvConfig failed: %v", err)
	}
	if envConfig.Branch != "develop" {
		t.Fatalf("expected detected branch to be stored, got %+v", envConfig)
	}
}

func TestRootCommandHelpFlagPrintsHelp(t *testing.T) {
	cmd := NewRootCmd(Dependencies{})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := buf.String()
	for _, want := range []string{"init", "open", "mcp", "version"} {
		if !bytes.Contains([]byte(output), []byte(want)) {
			t.Fatalf("expected help output to mention %q, got %q", want, output)
		}
	}
}

func TestRootCommandInitErrorsDoNotPrintHelp(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "", "", internal.ErrNotInGitRepository
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if !errors.Is(err, internal.ErrNotInGitRepository) {
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

	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "", "", internal.ErrNotInGitRepository
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"init"})

	err := cmd.Execute()
	if !errors.Is(err, internal.ErrNotInGitRepository) {
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

	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "", "", internal.ErrNotInGitRepository
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if !errors.Is(err, internal.ErrNotInGitRepository) {
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
	previous := defaultPromptRunner
	defaultPromptRunner = func(promptui.Prompt) (string, error) {
		return "", nil
	}
	t.Cleanup(func() {
		defaultPromptRunner = previous
	})

	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "", "", internal.ErrNotInGitRepository
		},
	})
	cmd.SetArgs([]string{})
	err := func() error {
		return cmd.Execute()
	}()
	if !errors.Is(err, internal.ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}
}

func setupRootCmdTestConfigHome(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
	t.Cleanup(func() {
		xdg.Reload()
	})
}
