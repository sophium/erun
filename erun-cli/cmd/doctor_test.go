package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

func TestDoctorCommandPromptsAndRunsSelectedCleanup(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("SaveERunConfig failed: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", ProjectRoot: projectRoot, DefaultEnvironment: "local"}); err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "local", RepoPath: projectRoot, KubernetesContext: "cluster-local"}); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}

	kubectlDir := t.TempDir()
	kubectlPath := filepath.Join(kubectlDir, "kubectl")
	kubectlScript := `#!/bin/sh
last=""
for arg in "$@"; do
  last="$arg"
done
if [ "$1" = "--context" ]; then
  shift 4
fi
if [ "$1" = "wait" ]; then
  exit 0
fi
if [ "$1" = "exec" ]; then
  case "$last" in
    *"docker image prune -a -f"*)
      printf 'images pruned\n'
      exit 0
      ;;
    *"docker system df"*)
      printf '== Docker system df ==\nTYPE TOTAL ACTIVE SIZE RECLAIMABLE\n'
      exit 0
      ;;
  esac
fi
echo "unexpected kubectl invocation: $@" >&2
exit 1
`
	if err := os.WriteFile(kubectlPath, []byte(kubectlScript), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", kubectlDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	prompts := make([]string, 0, 3)
	cmd := newTestRootCmd(testRootDeps{
		PromptRunner: func(prompt promptui.Prompt) (string, error) {
			label := fmt.Sprint(prompt.Label)
			prompts = append(prompts, label)
			if strings.Contains(label, "Docker images") {
				return "y", nil
			}
			return "n", nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"doctor"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"Target: tenant-a/local",
		"== Docker system df ==",
		"Running: Remove unused Docker images",
		"images pruned",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
	if len(prompts) != 3 {
		t.Fatalf("expected 3 prompts, got %+v", prompts)
	}
}

func TestDoctorCommandDryRunShowsDindTraceForSelectedAction(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("SaveERunConfig failed: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", ProjectRoot: projectRoot, DefaultEnvironment: "local"}); err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "local", RepoPath: projectRoot, KubernetesContext: "cluster-local"}); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"doctor", "--dry-run", "--prune-images"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	for _, want := range []string{
		"kubectl --context rancher-desktop --namespace tenant-a-local wait --for=condition=Available --timeout 2m0s deployment/tenant-a-devops",
		"kubectl --context rancher-desktop --namespace tenant-a-local exec -c erun-dind deployment/tenant-a-devops -- /bin/sh -lc '<remote-script>'",
		"docker image prune -a -f",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected trace to contain %q, got:\n%s", want, output)
		}
	}
}
