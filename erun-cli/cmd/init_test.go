package cmd

import (
	"errors"
	"testing"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

// The init command's dry-run trace, --remote validation, positional-arg
// mapping, runtime-version override, and default-selection overrides are
// covered by the integration suite (erun-integration/init_test.go). The
// kubectl namespace check/create branches that were previously unit-tested
// via PATH stubs were removed because driving production code with a stub
// `kubectl` binary is a policy violation (see AGENTS.md). The cases below
// stay as unit tests because they exercise interactive promptui flows
// (kubectl-context selection, container-registry default/cancel) that
// integration scenarios cannot reach without scripting stdin, plus a pure
// helper that reorders contexts.

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
