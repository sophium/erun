package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

func TestDeleteCommandDeletesRemoteNamespaceAndConfigAfterConfirmation(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", DefaultEnvironment: "dev"}), "SaveTenantConfig failed")
	requireNoError(t, common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "dev", KubernetesContext: "cluster-dev", Remote: true}), "SaveEnvConfig failed")

	var deletedContext string
	var deletedNamespace string
	cmd := newTestRootCmd(testRootDeps{
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			if !strings.Contains(fmt.Sprint(prompt.Label), "tenant-a-dev") {
				t.Fatalf("unexpected prompt label: %q", prompt.Label)
			}
			return "tenant-a-dev", nil
		},
		DeleteKubernetesNamespace: func(contextName, namespace string) error {
			deletedContext = contextName
			deletedNamespace = namespace
			return nil
		},
	})
	stdout := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"delete", "tenant-a", "dev"})

	requireNoError(t, cmd.Execute(), "Execute failed")
	if deletedContext != "cluster-dev" || deletedNamespace != "tenant-a-dev" {
		t.Fatalf("unexpected namespace delete: context=%q namespace=%q", deletedContext, deletedNamespace)
	}
	if _, _, err := common.LoadEnvConfig("tenant-a", "dev"); !errors.Is(err, common.ErrNotInitialized) {
		t.Fatalf("expected env config to be deleted, got %v", err)
	}
	if !strings.Contains(stdout.String(), "deleted environment: tenant-a/dev") {
		t.Fatalf("expected delete confirmation output, got %q", stdout.String())
	}
}

func TestDeleteCommandDeletesConfigWhenNamespaceDeleteFails(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", DefaultEnvironment: "dev"}), "SaveTenantConfig failed")
	requireNoError(t, common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "dev", KubernetesContext: "cluster-dev", Remote: true}), "SaveEnvConfig failed")

	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		PromptRunner: func(promptui.Prompt) (string, error) {
			return "tenant-a-dev", nil
		},
		DeleteKubernetesNamespace: func(string, string) error {
			return errors.New("cluster unavailable")
		},
	})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"delete", "tenant-a", "dev"})

	requireNoError(t, cmd.Execute(), "Execute failed")
	if _, _, err := common.LoadEnvConfig("tenant-a", "dev"); !errors.Is(err, common.ErrNotInitialized) {
		t.Fatalf("expected env config to be deleted, got %v", err)
	}
	if !strings.Contains(stderr.String(), "warning: failed to delete namespace") {
		t.Fatalf("expected namespace warning, got %q", stderr.String())
	}
}

func TestDeleteCommandRejectsMismatchedConfirmation(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", DefaultEnvironment: "dev"}), "SaveTenantConfig failed")
	requireNoError(t, common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "dev", KubernetesContext: "cluster-dev", Remote: true}), "SaveEnvConfig failed")

	deletedNamespace := false
	cmd := newTestRootCmd(testRootDeps{
		PromptRunner: func(promptui.Prompt) (string, error) {
			return "tenant-a-prod", nil
		},
		DeleteKubernetesNamespace: func(string, string) error {
			deletedNamespace = true
			return nil
		},
	})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"delete", "tenant-a", "dev"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `delete confirmation did not match "tenant-a-dev"`) {
		t.Fatalf("expected confirmation mismatch, got %v", err)
	}
	if deletedNamespace {
		t.Fatal("did not expect namespace deletion")
	}
	if _, _, err := common.LoadEnvConfig("tenant-a", "dev"); err != nil {
		t.Fatalf("expected env config to remain, got %v", err)
	}
}

func TestDeleteCommandDryRunSkipsPromptAndDeletion(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", DefaultEnvironment: "dev"}), "SaveTenantConfig failed")
	requireNoError(t, common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "dev", KubernetesContext: "cluster-dev", Remote: true}), "SaveEnvConfig failed")

	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		PromptRunner: func(promptui.Prompt) (string, error) {
			t.Fatal("did not expect confirmation prompt during dry-run")
			return "", nil
		},
		DeleteKubernetesNamespace: func(string, string) error {
			t.Fatal("did not expect namespace deletion during dry-run")
			return nil
		},
	})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"delete", "tenant-a", "dev", "--dry-run"})

	requireNoError(t, cmd.Execute(), "Execute failed")
	if _, _, err := common.LoadEnvConfig("tenant-a", "dev"); err != nil {
		t.Fatalf("expected env config to remain during dry-run, got %v", err)
	}
	if !strings.Contains(stderr.String(), "kubectl --context cluster-dev delete namespace tenant-a-dev --ignore-not-found") {
		t.Fatalf("expected dry-run trace, got %q", stderr.String())
	}
}
