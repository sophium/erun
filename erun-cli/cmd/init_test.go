package cmd

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
)

func TestKubernetesContextPromptSelectsExistingContext(t *testing.T) {
	got, err := kubernetesContextPrompt(
		func(promptui.Prompt) (string, error) {
			t.Fatal("unexpected manual prompt")
			return "", nil
		},
		func(prompt promptui.Select) (int, string, error) {
			if prompt.Label != "Choose context" {
				t.Fatalf("unexpected select label: %v", prompt.Label)
			}
			return 1, "cluster-b", nil
		},
		func() ([]string, error) {
			return []string{"cluster-a", "cluster-b", "cluster-a", "", " cluster-b "}, nil
		},
		"Choose context",
	)
	if err != nil {
		t.Fatalf("kubernetesContextPrompt failed: %v", err)
	}
	if got != "cluster-b" {
		t.Fatalf("unexpected context: %q", got)
	}
}

func TestKubernetesContextPromptAllowsManualEntryAfterSelection(t *testing.T) {
	got, err := kubernetesContextPrompt(
		func(prompt promptui.Prompt) (string, error) {
			if prompt.Label != "Choose context" {
				t.Fatalf("unexpected prompt label: %v", prompt.Label)
			}
			return "manual-context", nil
		},
		func(prompt promptui.Select) (int, string, error) {
			return 2, enterKubernetesContextManuallyOption, nil
		},
		func() ([]string, error) {
			return []string{"cluster-a", "cluster-b"}, nil
		},
		"Choose context",
	)
	if err != nil {
		t.Fatalf("kubernetesContextPrompt failed: %v", err)
	}
	if got != "manual-context" {
		t.Fatalf("unexpected context: %q", got)
	}
}

func TestKubernetesContextPromptFallsBackToManualWhenLookupFails(t *testing.T) {
	got, err := kubernetesContextPrompt(
		func(prompt promptui.Prompt) (string, error) {
			if prompt.Label != "Choose context" {
				t.Fatalf("unexpected prompt label: %v", prompt.Label)
			}
			return "manual-context", nil
		},
		func(promptui.Select) (int, string, error) {
			t.Fatal("unexpected selection prompt")
			return 0, "", nil
		},
		func() ([]string, error) {
			return nil, errors.New("kubectl failed")
		},
		"Choose context",
	)
	if err != nil {
		t.Fatalf("kubernetesContextPrompt failed: %v", err)
	}
	if got != "manual-context" {
		t.Fatalf("unexpected context: %q", got)
	}
}

func TestKubernetesContextPromptReturnsCancellationOnSelectAbort(t *testing.T) {
	_, err := kubernetesContextPrompt(
		func(promptui.Prompt) (string, error) {
			t.Fatal("unexpected manual prompt")
			return "", nil
		},
		func(promptui.Select) (int, string, error) {
			return 0, "", promptui.ErrAbort
		},
		func() ([]string, error) {
			return []string{"cluster-a"}, nil
		},
		"Choose context",
	)
	if !errors.Is(err, bootstrap.ErrKubernetesContextCancelled) {
		t.Fatalf("expected ErrKubernetesContextCancelled, got %v", err)
	}
}

func TestContainerRegistryPromptUsesDefaultOnEmptyInput(t *testing.T) {
	got, err := containerRegistryPrompt(func(prompt promptui.Prompt) (string, error) {
		if prompt.Label != "Choose registry" {
			t.Fatalf("unexpected prompt label: %v", prompt.Label)
		}
		if prompt.Default != bootstrap.DefaultContainerRegistry {
			t.Fatalf("unexpected prompt default: %q", prompt.Default)
		}
		return "", nil
	}, "Choose registry")
	if err != nil {
		t.Fatalf("containerRegistryPrompt failed: %v", err)
	}
	if got != bootstrap.DefaultContainerRegistry {
		t.Fatalf("unexpected registry: %q", got)
	}
}

func TestContainerRegistryPromptReturnsCancellationOnAbort(t *testing.T) {
	_, err := containerRegistryPrompt(func(promptui.Prompt) (string, error) {
		return "", promptui.ErrAbort
	}, "Choose registry")
	if !errors.Is(err, bootstrap.ErrContainerRegistryCancelled) {
		t.Fatalf("expected ErrContainerRegistryCancelled, got %v", err)
	}
}

func TestInitCommandDryRunDoesNotPersistConfiguration(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	namespaceEnsured := false
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			if prompt.IsConfirm {
				return "y", nil
			}
			return "", nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			return 0, "cluster-local", nil
		},
		ListKubernetesContexts: func() ([]string, error) {
			return []string{"cluster-local"}, nil
		},
		EnsureKubernetesNamespace: func(contextName, namespace string) error {
			namespaceEnsured = true
			return nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"init", "--dry-run", "-v"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if namespaceEnsured {
		t.Fatal("did not expect namespace creation during dry-run")
	}
	if _, _, err := internal.LoadERunConfig(); !errors.Is(err, internal.ErrNotInitialized) {
		t.Fatalf("expected erun config to remain absent, got %v", err)
	}
	if _, _, err := internal.LoadTenantConfig("tenant-a"); !errors.Is(err, internal.ErrNotInitialized) {
		t.Fatalf("expected tenant config to remain absent, got %v", err)
	}
	if _, _, err := internal.LoadEnvConfig("tenant-a", bootstrap.DefaultEnvironment); !errors.Is(err, internal.ErrNotInitialized) {
		t.Fatalf("expected env config to remain absent, got %v", err)
	}
	if got := stderr.String(); !bytes.Contains([]byte(got), []byte("[dry-run] decision: dry-run suppresses configuration writes and namespace creation")) {
		t.Fatalf("expected dry-run trace output, got %q", got)
	}
}

func TestInitCommandDryRunPrintsConcretePlannedActions(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			if prompt.IsConfirm {
				return "y", nil
			}
			return "", nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			return 0, "cluster-local", nil
		},
		ListKubernetesContexts: func() ([]string, error) {
			return []string{"cluster-local"}, nil
		},
	})
	stderr := new(bytes.Buffer)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"init", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	rootConfigPath, err := xdg.ConfigFile(filepath.Join("erun", "config.yaml"))
	if err != nil {
		t.Fatalf("xdg config path: %v", err)
	}
	tenantConfigPath, err := xdg.ConfigFile(filepath.Join("erun", "tenant-a", "config.yaml"))
	if err != nil {
		t.Fatalf("xdg tenant path: %v", err)
	}
	envConfigPath, err := xdg.ConfigFile(filepath.Join("erun", "tenant-a", bootstrap.DefaultEnvironment, "config.yaml"))
	if err != nil {
		t.Fatalf("xdg env path: %v", err)
	}
	projectConfigPath := filepath.Join(projectRoot, ".erun", "config.yaml")

	output := stderr.String()
	for _, want := range []string{
		"[dry-run] kubectl create namespace tenant-a-local --dry-run=client -o yaml",
		"[dry-run] kubectl --context cluster-local apply -f -",
		"[dry-run] write-yaml " + rootConfigPath,
		"[dry-run] write-yaml " + tenantConfigPath,
		"[dry-run] write-yaml " + envConfigPath,
		"[dry-run] write-yaml " + projectConfigPath,
	} {
		if !bytes.Contains([]byte(output), []byte(want)) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
}
