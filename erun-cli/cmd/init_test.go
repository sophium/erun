package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
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
	if !errors.Is(err, common.ErrKubernetesContextCancelled) {
		t.Fatalf("expected ErrKubernetesContextCancelled, got %v", err)
	}
}

func TestPreferCurrentKubernetesContextMovesCurrentToFront(t *testing.T) {
	got := preferCurrentKubernetesContext([]string{"cluster-a", "cluster-b", "cluster-c"}, "cluster-b\n")
	want := []string{"cluster-b", "cluster-a", "cluster-c"}
	if len(got) != len(want) {
		t.Fatalf("unexpected contexts length: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected context at %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestContainerRegistryPromptUsesDefaultOnEmptyInput(t *testing.T) {
	got, err := containerRegistryPrompt(func(prompt promptui.Prompt) (string, error) {
		if prompt.Label != "Choose registry" {
			t.Fatalf("unexpected prompt label: %v", prompt.Label)
		}
		if prompt.Default != common.DefaultContainerRegistry {
			t.Fatalf("unexpected prompt default: %q", prompt.Default)
		}
		return "", nil
	}, "Choose registry")
	if err != nil {
		t.Fatalf("containerRegistryPrompt failed: %v", err)
	}
	if got != common.DefaultContainerRegistry {
		t.Fatalf("unexpected registry: %q", got)
	}
}

func TestContainerRegistryPromptReturnsCancellationOnAbort(t *testing.T) {
	_, err := containerRegistryPrompt(func(promptui.Prompt) (string, error) {
		return "", promptui.ErrAbort
	}, "Choose registry")
	if !errors.Is(err, common.ErrContainerRegistryCancelled) {
		t.Fatalf("expected ErrContainerRegistryCancelled, got %v", err)
	}
}

func TestInitCommandDryRunDoesNotPersistConfiguration(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	namespaceEnsured := false
	cmd := newTestRootCmd(testRootDeps{
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
	if _, _, err := common.LoadERunConfig(); !errors.Is(err, common.ErrNotInitialized) {
		t.Fatalf("expected erun config to remain absent, got %v", err)
	}
	if _, _, err := common.LoadTenantConfig("tenant-a"); !errors.Is(err, common.ErrNotInitialized) {
		t.Fatalf("expected tenant config to remain absent, got %v", err)
	}
	if _, _, err := common.LoadEnvConfig("tenant-a", common.DefaultEnvironment); !errors.Is(err, common.ErrNotInitialized) {
		t.Fatalf("expected env config to remain absent, got %v", err)
	}
	if got := stderr.String(); !bytes.Contains([]byte(got), []byte("write-yaml")) {
		t.Fatalf("expected dry-run trace output, got %q", got)
	}
}

func TestInitCommandDryRunPrintsConcretePlannedActions(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	cmd := newTestRootCmd(testRootDeps{
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
	cmd.SetArgs([]string{"init", "--dry-run", "-v"})

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
	envConfigPath, err := xdg.ConfigFile(filepath.Join("erun", "tenant-a", common.DefaultEnvironment, "config.yaml"))
	if err != nil {
		t.Fatalf("xdg env path: %v", err)
	}
	projectConfigPath := filepath.Join(projectRoot, ".erun", "config.yaml")
	devopsVersionPath := filepath.Join(projectRoot, "tenant-a-devops", "VERSION")
	devopsDockerfilePath := filepath.Join(projectRoot, "tenant-a-devops", "docker", "tenant-a-devops", "Dockerfile")

	output := stderr.String()
	for _, want := range []string{
		"kubectl --context cluster-local get namespace tenant-a-local -o name",
		"kubectl --context cluster-local create namespace tenant-a-local",
		"write-yaml " + rootConfigPath,
		"write-yaml " + tenantConfigPath,
		"write-yaml " + envConfigPath,
		"write-yaml " + projectConfigPath,
		"write-file " + devopsVersionPath,
		"write-file " + devopsDockerfilePath,
	} {
		if !bytes.Contains([]byte(output), []byte(want)) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
}

func TestEnsureKubernetesNamespaceReturnsNilWhenNamespaceAlreadyExists(t *testing.T) {
	kubectlDir := t.TempDir()
	kubectlPath := filepath.Join(kubectlDir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte(`#!/bin/sh
if [ "$1" = "--context" ] && [ "$3" = "get" ] && [ "$4" = "namespace" ] && [ "$5" = "tenant-a-local" ]; then
  echo "namespace/tenant-a-local"
  exit 0
fi
echo "unexpected kubectl invocation: $@" >&2
exit 1
`), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", kubectlDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := ensureKubernetesNamespace("cluster-local", "tenant-a-local"); err != nil {
		t.Fatalf("ensureKubernetesNamespace failed: %v", err)
	}
}

func TestEnsureKubernetesNamespaceCreatesWhenNamespaceMissing(t *testing.T) {
	kubectlDir := t.TempDir()
	kubectlPath := filepath.Join(kubectlDir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte(`#!/bin/sh
if [ "$1" = "--context" ] && [ "$3" = "get" ] && [ "$4" = "namespace" ] && [ "$5" = "tenant-a-local" ]; then
  echo 'Error from server (NotFound): namespaces "tenant-a-local" not found' >&2
  exit 1
fi
if [ "$1" = "--context" ] && [ "$3" = "create" ] && [ "$4" = "namespace" ] && [ "$5" = "tenant-a-local" ]; then
  echo "namespace/tenant-a-local"
  exit 0
fi
echo "unexpected kubectl invocation: $@" >&2
exit 1
`), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", kubectlDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := ensureKubernetesNamespace("cluster-local", "tenant-a-local"); err != nil {
		t.Fatalf("ensureKubernetesNamespace failed: %v", err)
	}
}
