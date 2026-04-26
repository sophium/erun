package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

func TestHelmDeployRecoveryClearsPendingMetadataAndRetries(t *testing.T) {
	stderr := new(bytes.Buffer)
	deployCalls := 0
	var recovered common.HelmReleaseRecoveryParams

	deploy := wrapHelmDeployWithReleaseRecovery(
		func(prompt promptui.Prompt) (string, error) {
			if !prompt.IsConfirm {
				t.Fatalf("expected confirm prompt, got %+v", prompt)
			}
			want := "clear pending Helm metadata for release erun-devops from namespace erun-local in context rancher-desktop and retry deploy"
			if prompt.Label != want {
				t.Fatalf("unexpected prompt label %q, want %q", prompt.Label, want)
			}
			return "y", nil
		},
		func(params common.HelmDeployParams) error {
			deployCalls++
			if deployCalls == 1 {
				return &common.HelmReleasePendingOperationError{
					ReleaseName:       params.ReleaseName,
					Namespace:         params.Namespace,
					KubernetesContext: params.KubernetesContext,
					Message:           "Error: UPGRADE FAILED: another operation (install/upgrade/rollback) is in progress",
					Err:               errors.New("exit status 1"),
				}
			}
			return nil
		},
		func(params common.HelmReleaseRecoveryParams) error {
			recovered = params
			return nil
		},
	)

	err := deploy(common.HelmDeployParams{
		ReleaseName:       "erun-devops",
		Namespace:         "erun-local",
		KubernetesContext: "rancher-desktop",
		Stderr:            stderr,
	})
	if err != nil {
		t.Fatalf("deploy failed: %v", err)
	}
	if deployCalls != 2 {
		t.Fatalf("expected deploy to retry once, got %d calls", deployCalls)
	}
	if recovered.ReleaseName != "erun-devops" || recovered.Namespace != "erun-local" || recovered.KubernetesContext != "rancher-desktop" {
		t.Fatalf("unexpected recovery params: %+v", recovered)
	}
	if strings.Contains(stderr.String(), "helm uninstall") {
		t.Fatalf("did not expect destructive uninstall command, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "kubectl --context rancher-desktop --namespace erun-local delete 'secrets,configmaps'") {
		t.Fatalf("expected recovery command in stderr, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "owner=helm,name=erun-devops,status in (pending-install,pending-upgrade,pending-rollback)") {
		t.Fatalf("expected pending metadata selector in stderr, got %q", stderr.String())
	}
}
