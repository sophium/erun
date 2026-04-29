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
	jetbrainsconfig "github.com/sophium/erun/internal/jetbrainsconfig"
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

	projectRoot := filepath.Join(t.TempDir(), "petios")
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
	stubKubectlContexts(t, []string{"rancher-desktop"}, "rancher-desktop")

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

func TestDoctorCommandRepairsJetBrainsGatewayMetadata(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	prevHome := ideUserHomeDir
	prevGlob := ideGlob
	t.Cleanup(func() {
		ideUserHomeDir = prevHome
		ideGlob = prevGlob
	})

	projectRoot := filepath.Join(t.TempDir(), "petios")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "petios"}); err != nil {
		t.Fatalf("SaveERunConfig failed: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{Name: "petios", ProjectRoot: projectRoot, DefaultEnvironment: "rihards"}); err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}
	if err := common.SaveEnvConfig("petios", common.EnvConfig{
		Name:              "rihards",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-dev",
		SSHD: common.SSHDConfig{
			Enabled:   true,
			LocalPort: 17422,
		},
	}); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}

	root := t.TempDir()
	optionsDir := filepath.Join(root, "JetBrains", "IntelliJIdea2025.3", "options")
	if err := os.MkdirAll(optionsDir, 0o700); err != nil {
		t.Fatalf("mkdir options dir: %v", err)
	}
	configID := jetbrainsconfig.StableConfigID("erun-petios-rihards")
	recentPath := filepath.Join(optionsDir, "sshRecentConnections.v2.xml")
	if err := os.WriteFile(recentPath, []byte(`<application>
  <component name="SshLocalRecentConnectionsManager">
    <option name="connections">
      <list>
        <LocalRecentConnectionState>
          <option name="configId" value="`+configID+`"></option>
          <option name="projects">
            <list>
              <RecentProjectState>
                <option name="date" value="1777477119934"></option>
                <option name="latestUsedIde">
                  <RecentProjectInstalledIde>
                    <option name="buildNumber" value="261.23567.71"></option>
                    <option name="pathToIde" value="/home/erun/.cache/JetBrains/RemoteDev/dist/fd6f0251cd1fc_idea-261.23567.71-aarch64"></option>
                    <option name="productCode" value="IU"></option>
                  </RecentProjectInstalledIde>
                </option>
                <option name="productCode" value="IU"></option>
                <option name="projectPath" value="/home/erun/git/petios"></option>
              </RecentProjectState>
            </list>
          </option>
        </LocalRecentConnectionState>
      </list>
    </option>
  </component>
</application>
`), 0o600); err != nil {
		t.Fatalf("write recent projects: %v", err)
	}

	ideUserHomeDir = func() (string, error) { return root, nil }
	ideGlob = func(pattern string) ([]string, error) {
		if strings.Contains(pattern, "IntelliJIdea*") {
			return []string{optionsDir}, nil
		}
		return nil, nil
	}

	cmd := newTestRootCmd(testRootDeps{})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"doctor", "petios", "rihards", "--repair-jetbrains-gateway"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"Target: petios/rihards",
		"Running: Clear cached JetBrains Gateway backend metadata",
		"Cleared cached JetBrains Gateway backend metadata",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
	recent := string(mustReadFile(t, recentPath))
	if strings.Contains(recent, "latestUsedIde") || strings.Contains(recent, "pathToIde") {
		t.Fatalf("expected cached IDE metadata to be removed, got:\n%s", recent)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
