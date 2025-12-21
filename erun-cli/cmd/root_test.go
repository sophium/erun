package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

const (
	testConfigRoot = "erun"
	testConfigFile = "config.yaml"
)

func setupCmdTestConfigHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
	t.Cleanup(func() {
		xdg.Reload()
	})
}

func resetCmdState() {
	eRunConfig = internal.ERunConfig{}
	tenantConfig = internal.TenantConfig{}
	envConfig = internal.EnvConfig{}
	verbosity = 0
	promptRunner = defaultPromptRunner
	initConfigFunc = initConfig
	findProjectRoot = internal.FindProjectRoot
	rootCmd = NewRootCmd()
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	configureLogging()
}

func captureOutput(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	original := *target
	*target = w

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	*target = original

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	return buf.String()
}

func TestConfigureLoggingUsesVerbosity(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	verbosity = 2
	configureLogging()
	out := captureOutput(t, &os.Stdout, func() {
		log.Trace("trace")
	})
	if !strings.Contains(out, "trace") {
		t.Fatalf("expected trace output, got %q", out)
	}

	verbosity = 0
	configureLogging()
	out = captureOutput(t, &os.Stdout, func() {
		log.Trace("hidden")
	})
	if out != "" {
		t.Fatalf("expected silence, got %q", out)
	}
}

func TestInitConfigLoadsExistingConfiguration(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	tenant := "tenant-a"
	env := "dev"
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := internal.SaveTenantConfig(internal.TenantConfig{ProjectRoot: projectRoot, Name: tenant, DefaultEnvironment: env}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig(tenant, internal.EnvConfig{Name: env}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	if err := initConfig(); err != nil {
		t.Fatalf("initConfig failed: %v", err)
	}

	if eRunConfig.DefaultTenant != tenant {
		t.Fatalf("expected tenant %q, got %q", tenant, eRunConfig.DefaultTenant)
	}
	if tenantConfig.ProjectRoot != projectRoot {
		t.Fatalf("unexpected project root: %s", tenantConfig.ProjectRoot)
	}
	if envConfig.Name != env {
		t.Fatalf("unexpected env: %+v", envConfig)
	}
}

func TestInitConfigBootstrapsNewProject(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	repoRoot := filepath.Join(t.TempDir(), "project")
	nested := filepath.Join(repoRoot, "nested", "service")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir git: %v", err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	promptRunner = func(prompt promptui.Prompt) (string, error) { return "y", nil }
	t.Cleanup(func() { promptRunner = defaultPromptRunner })
	findProjectRoot = func() (string, string, error) {
		return filepath.Base(repoRoot), repoRoot, nil
	}
	t.Cleanup(func() { findProjectRoot = internal.FindProjectRoot })

	if err := initConfig(); err != nil {
		t.Fatalf("initConfig failed: %v", err)
	}

	if eRunConfig.DefaultTenant != filepath.Base(repoRoot) {
		t.Fatalf("expected default tenant %q, got %q", filepath.Base(repoRoot), eRunConfig.DefaultTenant)
	}
	if tenantConfig.ProjectRoot != repoRoot {
		t.Fatalf("unexpected project root: %s", tenantConfig.ProjectRoot)
	}
	if envConfig.Name != defaultEnvironment {
		t.Fatalf("unexpected env name: %s", envConfig.Name)
	}
}

func TestInitConfigFailsOutsideGitRepo(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	findProjectRoot = func() (string, string, error) {
		return "", "", internal.ErrNotInGitRepository
	}
	t.Cleanup(func() { findProjectRoot = internal.FindProjectRoot })

	if err := initConfig(); !errors.Is(err, internal.ErrNotInGitRepository) {
		t.Fatalf("expected ErrNotInGitRepository, got %v", err)
	}
}

func TestInitConfigTenantConfirmationRejected(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	promptRunner = func(prompt promptui.Prompt) (string, error) { return "n", nil }
	t.Cleanup(func() { promptRunner = defaultPromptRunner })
	findProjectRoot = func() (string, string, error) {
		return "tenant", "/tmp/project", nil
	}
	t.Cleanup(func() { findProjectRoot = internal.FindProjectRoot })

	err := initConfig()
	if err == nil || !strings.Contains(err.Error(), "tenant initialization cancelled") {
		t.Fatalf("expected tenant cancellation, got %v", err)
	}
}

func TestInitConfigEnvConfirmationRejected(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	tenant := "tenant-a"
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{ProjectRoot: "/tmp/project", Name: tenant, DefaultEnvironment: defaultEnvironment}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}

	promptRunner = func(prompt promptui.Prompt) (string, error) { return "n", nil }
	t.Cleanup(func() { promptRunner = defaultPromptRunner })

	err := initConfig()
	if err == nil || !strings.Contains(err.Error(), "environment initialization cancelled") {
		t.Fatalf("expected environment cancellation, got %v", err)
	}
}

func TestInitConfigPromptError(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	promptRunner = func(prompt promptui.Prompt) (string, error) { return "", promptui.ErrInterrupt }
	t.Cleanup(func() { promptRunner = defaultPromptRunner })
	findProjectRoot = func() (string, string, error) {
		return "tenant", "/tmp/project", nil
	}
	t.Cleanup(func() { findProjectRoot = internal.FindProjectRoot })

	err := initConfig()
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("expected interrupt error, got %v", err)
	}
}

func TestInitConfigDefaultSaveError(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), testConfigRoot)
	if err := os.MkdirAll(dir, 0o555); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reset chmod: %v", err)
		}
	})

	promptRunner = func(prompt promptui.Prompt) (string, error) { return "y", nil }
	t.Cleanup(func() { promptRunner = defaultPromptRunner })
	findProjectRoot = func() (string, string, error) {
		return "tenant", "/tmp/project", nil
	}
	t.Cleanup(func() { findProjectRoot = internal.FindProjectRoot })

	err := initConfig()
	if !errors.Is(err, internal.ErrFailedToSaveConfig) {
		t.Fatalf("expected save error, got %v", err)
	}
}

func TestInitConfigTenantSaveError(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	promptRunner = func(prompt promptui.Prompt) (string, error) { return "y", nil }
	t.Cleanup(func() { promptRunner = defaultPromptRunner })
	findProjectRoot = func() (string, string, error) {
		return "tenant", "/tmp/project", nil
	}
	t.Cleanup(func() { findProjectRoot = internal.FindProjectRoot })

	tenantDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), testConfigRoot, "tenant")
	if err := os.MkdirAll(filepath.Dir(tenantDir), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(tenantDir, []byte("block"), 0o644); err != nil {
		t.Fatalf("write block: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(tenantDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cleanup tenant dir: %v", err)
		}
	})

	err := initConfig()
	if !errors.Is(err, internal.ErrNoUserDataFolder) {
		t.Fatalf("expected directory error, got %v", err)
	}
}

func TestInitConfigTenantConfigCorrupted(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	tenant := "tenant-a"
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), testConfigRoot, tenant, testConfigFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir tenant dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("-"), 0o644); err != nil {
		t.Fatalf("write corrupted tenant: %v", err)
	}

	if err := initConfig(); !errors.Is(err, internal.ErrConfigCorrupted) {
		t.Fatalf("expected tenant config error, got %v", err)
	}
}

func TestInitConfigEnvPromptError(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	tenant := "tenant-a"
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{ProjectRoot: "/tmp/project", Name: tenant, DefaultEnvironment: defaultEnvironment}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}

	promptRunner = func(prompt promptui.Prompt) (string, error) { return "", promptui.ErrInterrupt }
	t.Cleanup(func() { promptRunner = defaultPromptRunner })

	if err := initConfig(); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("expected env prompt error, got %v", err)
	}
}

func TestInitConfigEnvSaveError(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	tenant := "tenant-a"
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{ProjectRoot: "/tmp/project", Name: tenant, DefaultEnvironment: defaultEnvironment}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}

	promptRunner = func(prompt promptui.Prompt) (string, error) { return "y", nil }
	t.Cleanup(func() { promptRunner = defaultPromptRunner })
	envDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), testConfigRoot, tenant, defaultEnvironment)
	if err := os.MkdirAll(envDir, 0o555); err != nil {
		t.Fatalf("mkdir env dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(envDir, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reset env chmod: %v", err)
		}
	})

	if err := initConfig(); !errors.Is(err, internal.ErrFailedToSaveConfig) {
		t.Fatalf("expected env save error, got %v", err)
	}
}

func TestInitConfigEnvCorruptedConfig(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	tenant := "tenant-a"
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: tenant}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{ProjectRoot: "/tmp/project", Name: tenant, DefaultEnvironment: defaultEnvironment}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	envPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), testConfigRoot, tenant, defaultEnvironment, testConfigFile)
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatalf("mkdir env dir: %v", err)
	}
	if err := os.WriteFile(envPath, []byte("-"), 0o644); err != nil {
		t.Fatalf("write corrupted env: %v", err)
	}

	if err := initConfig(); !errors.Is(err, internal.ErrConfigCorrupted) {
		t.Fatalf("expected env config error, got %v", err)
	}
}

func TestExecuteSuccessAndFailure(t *testing.T) {
	resetCmdState()
	initConfigFunc = func() error { return nil }
	t.Cleanup(func() { initConfigFunc = initConfig })

	if rootCmd == nil {
		t.Fatal("expected root command to be initialized")
	}

	t.Run("success", func(t *testing.T) {
		cmd := &cobra.Command{Use: "test"}
		cmd.Run = func(cmd *cobra.Command, args []string) {}
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		prev := rootCmd
		rootCmd = cmd
		defer func() { rootCmd = prev }()

		if err := Execute(); err != nil {
			t.Fatalf("expected success, got %v", err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		cmd := &cobra.Command{Use: "test"}
		cmd.RunE = func(cmd *cobra.Command, args []string) error { return errors.New("boom") }
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		prev := rootCmd
		rootCmd = cmd
		defer func() { rootCmd = prev }()

		err := Execute()
		if err == nil || !strings.Contains(err.Error(), "cli execution failed") {
			t.Fatalf("expected wrapped error, got %v", err)
		}
	})
}

func TestConfirmPromptDefaultAndErrors(t *testing.T) {
	setupCmdTestConfigHome(t)
	resetCmdState()

	promptRunner = func(prompt promptui.Prompt) (string, error) { return "", nil }
	if ok, err := confirmPrompt("label"); err != nil || !ok {
		t.Fatalf("expected default confirmation, got %v %v", ok, err)
	}

	promptRunner = func(prompt promptui.Prompt) (string, error) { return "n", nil }
	if ok, err := confirmPrompt("label"); err != nil || ok {
		t.Fatalf("expected rejection, got %v %v", ok, err)
	}

	promptRunner = func(prompt promptui.Prompt) (string, error) { return "", promptui.ErrInterrupt }
	if ok, err := confirmPrompt("label"); err == nil || ok || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("expected interrupt error, got %v %v", ok, err)
	}

	expectedErr := errors.New("boom")
	promptRunner = func(prompt promptui.Prompt) (string, error) { return "", expectedErr }
	if ok, err := confirmPrompt("label"); !errors.Is(err, expectedErr) || ok {
		t.Fatalf("expected original error, got %v %v", ok, err)
	}
}
