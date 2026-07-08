// Package fixture writes deterministic seed configurations into an isolated
// HOME so erun subprocesses see a known tenant/environment/cloud-context layout
// without invoking interactive prompts.
package fixture

import (
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
)

func osExecCommand(name string, args []string, dir string) *osexec.Cmd {
	cmd := osexec.Command(name, args...)
	cmd.Dir = dir
	return cmd
}

// SeedTenantEnv writes the minimum erun config tree so commands resolve a
// tenant/environment without prompting.
func SeedTenantEnv(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+setup.Cwd+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+setup.Cwd+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"type: local-agent\n",
	)
}

// SeedTenantEnvWithContext puts an env on a caller-chosen kubernetes context so
// a scenario can place a second environment on a distinct cluster (e.g. expose's
// cross-cluster DNS routing between a PowerDNS platform env and a target env).
func SeedTenantEnvWithContext(t testing.TB, setup env.Setup, tenant, environment, kubernetesContext string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+setup.Cwd+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+setup.Cwd+"\n"+
			"kubernetescontext: "+kubernetesContext+"\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"type: local-agent\n",
	)
}

// SeedTenantEnvWithDeployTimeout persists a per-env deploy.timeout so deploy
// scenarios can exercise the configurable helm rollout timeout.
func SeedTenantEnvWithDeployTimeout(t testing.TB, setup env.Setup, tenant, environment, timeout string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+setup.Cwd+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+setup.Cwd+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"type: local-agent\n"+
			"deploy:\n"+
			"  timeout: "+timeout+"\n",
	)
}

// SeedTenantEnvWithDeployComponents persists a saved deploy selection so deploy
// scenarios can exercise the saved-set precedence tier: a deploy with no
// --components resolves to exactly the saved charts, and the runtime deploys
// only if it too is saved.
func SeedTenantEnvWithDeployComponents(t testing.TB, setup env.Setup, tenant, environment string, components []string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+setup.Cwd+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	body := "name: " + environment + "\n" +
		"repopath: " + setup.Cwd + "\n" +
		"kubernetescontext: test-context\n" +
		"containerregistry: registry.example/test\n" +
		"runtimeversion: 1.0.0\n" +
		"type: local-agent\n" +
		"deploy:\n" +
		"  components:\n"
	for _, component := range components {
		body += "    - " + component + "\n"
	}
	mustWrite(t, filepath.Join(envDir, "config.yaml"), body)
}

// SeedTenantEnvNoRegistry omits the per-env container registry so the registry
// list resolves entirely from the project's .erun/config.yaml (seed it with
// SeedProjectK8sConfig). Use it for marked-registry-list scenarios where the
// project config — not a migrated env-config scalar — must drive BUILD/FROM/
// TO/DEPLOY resolution.
func SeedTenantEnvNoRegistry(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+setup.Cwd+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+setup.Cwd+"\n"+
			"kubernetescontext: test-context\n"+
			"runtimeversion: 1.0.0\n"+
			"type: local-agent\n",
	)
}

// SeedTenantEnvWithLocalPortRangeStart persists a fixed localportrangestart so
// open exercises the persisted-range branch instead of the resolver's
// alphabetical walker.
func SeedTenantEnvWithLocalPortRangeStart(t testing.TB, setup env.Setup, tenant, environment string, rangeStart int) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+setup.Cwd+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+setup.Cwd+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"type: local-agent\n"+
			"localportrangestart: "+strconv.Itoa(rangeStart)+"\n",
	)
}

// SeedSecondaryTenantEnv writes a second tenant/env into an already-seeded XDG
// tree so resolver scenarios can exercise cross-tenant interactions (walker
// skip, overlap detection) without overwriting the primary tenant.
func SeedSecondaryTenantEnv(t testing.TB, setup env.Setup, tenant, environment string, rangeStart int) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+setup.Cwd+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	envContents := "name: " + environment + "\n" +
		"repopath: " + setup.Cwd + "\n" +
		"kubernetescontext: test-context\n" +
		"containerregistry: registry.example/test\n" +
		"runtimeversion: 1.0.0\n" +
		"type: local-agent\n"
	if rangeStart > 0 {
		envContents += "localportrangestart: " + strconv.Itoa(rangeStart) + "\n"
	}
	mustWrite(t, filepath.Join(envDir, "config.yaml"), envContents)
}

// SeedTenantEnvWithSnapshot persists the env's snapshot preference so open
// exercises the persisted-preference branch instead of the user passing
// --snapshot every time.
func SeedTenantEnvWithSnapshot(t testing.TB, setup env.Setup, tenant, environment string, enabled bool) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+setup.Cwd+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	snapshot := "false"
	if enabled {
		snapshot = "true"
	}
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+setup.Cwd+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"snapshot: "+snapshot+"\n",
	)
}

// SeedRemoteTenantEnvWithSSHD marks SSHD enabled on a remote env so
// open --vscode / --intellij reach past the validateIDEOptions guard.
func SeedRemoteTenantEnvWithSSHD(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	seedRemoteTenantEnvWithSSHD(t, setup, tenant, environment, 0)
}

// SeedRemoteTenantEnvWithSSHDPortRange pins a high localportrangestart (e.g.
// 26100) so a real-run open scenario's port-forward simulators never collide
// with a developer's live erun session on the default 17000 range; without the
// pin those scenarios silently skip on busy hosts and their coverage evaporates.
func SeedRemoteTenantEnvWithSSHDPortRange(t testing.TB, setup env.Setup, tenant, environment string, rangeStart int) {
	t.Helper()
	seedRemoteTenantEnvWithSSHD(t, setup, tenant, environment, rangeStart)
}

func seedRemoteTenantEnvWithSSHD(t testing.TB, setup env.Setup, tenant, environment string, rangeStart int) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	repoPath := filepath.Join(setup.Home, "git", tenant)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo %s: %v", repoPath, err)
	}

	envContents := "name: " + environment + "\n" +
		"repopath: " + repoPath + "\n" +
		"kubernetescontext: test-context\n" +
		"containerregistry: registry.example/test\n" +
		"runtimeversion: 1.0.0\n" +
		"type: remote-agent\n" +
		"sshd:\n" +
		"  enabled: true\n"
	if rangeStart > 0 {
		envContents += "localportrangestart: " + strconv.Itoa(rangeStart) + "\n"
	}

	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+repoPath+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"), envContents)
}

// SeedRemoteTenantEnv marks the env remote so commands like open, api, and mcp
// exercise the kubectl port-forward and remote-runtime traces. Its project root
// lives under a real on-disk repo dir so cwd resolution still works when the
// scenario chdirs into it.
func SeedRemoteTenantEnv(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	repoPath := filepath.Join(setup.Home, "git", tenant)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo %s: %v", repoPath, err)
	}

	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+repoPath+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+repoPath+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"type: remote-agent\n",
	)
}

// SeedRuntimeTenantEnvNoVersion writes a runtime-type env tree with NO
// runtimeversion (and no local/published chart), reproducing the fresh-env
// decision path that the desktop create regression hit: with no version
// to deploy, the published-chart resolver bails with "runtime version is
// required". Every other fixture pins runtimeversion: 1.0.0, so this is the
// single fixture that locks the empty-version path under `open --deploy`.
func SeedRuntimeTenantEnvNoVersion(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	repoPath := filepath.Join(setup.Home, "git", tenant)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo %s: %v", repoPath, err)
	}

	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+repoPath+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+repoPath+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"type: runtime\n",
	)
}

// SeedLegacyRemoteTenantEnv writes a tenant/env tree whose env config carries
// the retired `remote: true` shape with no `type` and no `snapshot`.
// It exists to exercise EnvConfig.UnmarshalYAML's legacy migration on read:
// remote with no build-here signal resolves to runtime. All other fixtures use
// the modern `type:` field; this one is the single deliberate legacy shape.
func SeedLegacyRemoteTenantEnv(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	repoPath := filepath.Join(setup.Home, "git", tenant)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo %s: %v", repoPath, err)
	}

	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+repoPath+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+repoPath+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"remote: true\n",
	)
}

// SeedRemoteTenantEnvWithPortRange pins a high localportrangestart (e.g. 26100)
// on a remote env (no sshd) so a real-run shell scenario's port-forward
// simulators never collide with a developer's live erun session on the default
// 17000 range; without the pin those scenarios would silently skip on busy
// hosts and their coverage would evaporate.
func SeedRemoteTenantEnvWithPortRange(t testing.TB, setup env.Setup, tenant, environment string, rangeStart int) {
	t.Helper()
	SeedRemoteTenantEnv(t, setup, tenant, environment)
	envDir := filepath.Join(setup.ConfigHome, "erun", tenant, environment)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+filepath.Join(setup.Home, "git", tenant)+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"type: remote-agent\n"+
			"localportrangestart: "+strconv.Itoa(rangeStart)+"\n",
	)
}

// SeedRemoteTenantEnvWithClaude attaches a claude: config block so scenarios can
// exercise the per-env Claude launch flags (--effort / --model / --verbose
// --debug) that the AI tab's persistent session resolves from env config.
func SeedRemoteTenantEnvWithClaude(t testing.TB, setup env.Setup, tenant, environment, claudeBlock string) {
	t.Helper()
	SeedRemoteTenantEnv(t, setup, tenant, environment)
	envDir := filepath.Join(setup.ConfigHome, "erun", tenant, environment)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+filepath.Join(setup.Home, "git", tenant)+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"type: remote-agent\n"+
			claudeBlock,
	)
}

// SeedRemoteTenantEnvWithAWSAlias attaches an AWS cloud alias — the operator
// opting the env into acting on their behalf — so it drives the deploy plumbing
// that injects host AWS credentials into the remote runtime.
func SeedRemoteTenantEnvWithAWSAlias(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	SeedRemoteTenantEnv(t, setup, tenant, environment)
	envDir := filepath.Join(setup.ConfigHome, "erun", tenant, environment)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+filepath.Join(setup.Home, "git", tenant)+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"type: remote-agent\n"+
			"cloudprovideralias: ops+123456789012@aws\n",
	)
}

// SeedRemoteRepoPathTenantEnv points the env's repopath at a nonexistent
// remote-host path while keeping the tenant projectroot at setup.Cwd (so chart
// resolution still finds the local <tenant>-devops module). Use it for
// regression scenarios where deploy must not readdir() the env's remote-host
// path.
func SeedRemoteRepoPathTenantEnv(t testing.TB, setup env.Setup, tenant, environment, remoteRepoPath string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, tenant)
	envDir := filepath.Join(tenantDir, environment)
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	mustWrite(t, filepath.Join(root, "config.yaml"), "defaulttenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+setup.Cwd+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+remoteRepoPath+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"type: remote-agent\n",
	)
}

// SeedReleaseRepo materializes the minimal erun-devops layout and a git repo
// that release scenarios need as a project root.
func SeedReleaseRepo(t testing.TB, dir, branch string) string {
	t.Helper()
	releaseRoot := filepath.Join(dir, "erun-devops")
	for _, sub := range []string{
		filepath.Join(releaseRoot, "k8s", "api"),
		filepath.Join(releaseRoot, "k8s", "base"),
		filepath.Join(releaseRoot, "docker", "api"),
		filepath.Join(releaseRoot, "docker", "base"),
	} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	mustWrite(t, filepath.Join(releaseRoot, "VERSION"), "1.4.2\n")
	mustWrite(t, filepath.Join(releaseRoot, "k8s", "api", "Chart.yaml"), "apiVersion: v2\nname: api\nversion: 0.1.0\nappVersion: 0.1.0\n")
	// base is a version-pinned base: its image carries its own VERSION (9.9.9)
	// and is not re-pushed at the release version, but its co-located chart must
	// still publish at the release version so platform deploys resolve it.
	mustWrite(t, filepath.Join(releaseRoot, "k8s", "base", "Chart.yaml"), "apiVersion: v2\nname: base\nversion: 0.1.0\nappVersion: 0.1.0\n")
	mustWrite(t, filepath.Join(releaseRoot, "docker", "api", "Dockerfile"), "FROM alpine:3.22\n")
	mustWrite(t, filepath.Join(releaseRoot, "docker", "base", "Dockerfile"), "FROM alpine:3.22\n")
	mustWrite(t, filepath.Join(releaseRoot, "docker", "base", "VERSION"), "9.9.9\n")

	if err := exec("git", []string{"init", "-q", "-b", branch}, dir); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := exec("git", []string{"config", "user.email", "test@example"}, dir); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if err := exec("git", []string{"config", "user.name", "Test"}, dir); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	if err := exec("git", []string{"add", "."}, dir); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := exec("git", []string{"commit", "-q", "-m", "initial"}, dir); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	return dir
}

// SeedMarketplaceJSON writes a placeholder .claude-plugin/marketplace.json so
// release scenarios can exercise the marketplace.json source.sha bump path.
func SeedMarketplaceJSON(t testing.TB, dir string) {
	t.Helper()
	dst := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	mustWrite(t, filepath.Join(dst, "marketplace.json"), `{
  "name": "sophium/erun",
  "plugins": [
    {
      "name": "erun-tools",
      "source": {
        "source": "git-subdir",
        "url": "https://github.com/sophium/erun.git",
        "path": "erun-skills",
        "ref": "main",
        "sha": "0000000000000000000000000000000000000000"
      }
    }
  ]
}
`)
}

// SeedScoopManifest writes bucket/erun.json with the given content so stable
// release scenarios exercise the Scoop manifest validation and version/checksum
// sync paths; a valid manifest reaches the happy path, a malformed one drives
// the validation-failure branch.
func SeedScoopManifest(t testing.TB, dir, content string) {
	t.Helper()
	dst := filepath.Join(dir, "bucket")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	mustWrite(t, filepath.Join(dst, "erun.json"), content)
}

// SeedHomebrewFormula writes Formula/erun.rb so stable release scenarios
// exercise the Homebrew packaging path. The url/sha256 lines use the exact
// two-space indentation the production regexes anchor on — reformatting them
// breaks the match.
func SeedHomebrewFormula(t testing.TB, dir string) {
	t.Helper()
	dst := filepath.Join(dir, "Formula")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	mustWrite(t, filepath.Join(dst, "erun.rb"), `class Erun < Formula
  desc "erun developer toolkit"
  homepage "https://github.com/sophium/erun"
  url "https://github.com/sophium/erun/archive/refs/tags/v1.0.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "MIT"

  def install
    system "go", "build", "-trimpath", "-o", bin/"erun", "."
  end
end
`)
}

// ReleaseGitStubSpec configures StubReleaseGit, the argv-branching git stub for
// real-run release scenarios: every mutation is a silent no-op so the captured
// output stays deterministic without a real remote or network.
type ReleaseGitStubSpec struct {
	// Branch is returned for `rev-parse --abbrev-ref HEAD`.
	Branch string
	// ShortCommit is returned for `rev-parse --short HEAD`.
	ShortCommit string
	// TagSHA, when non-empty, is returned for the `<tag>^{}` tag-existence
	// probe and for `rev-parse HEAD`, so the release tag resolves to a
	// commit and (for the non-force path) that commit equals HEAD. Empty
	// makes the `^{}` probe exit 1: the tag does not exist yet.
	TagSHA string
	// RemoteTag, when non-empty, makes `ls-remote --tags` report the tag as
	// present on origin at TagSHA, so --force runs the remote tag deletion.
	// Empty reports no remote tag.
	RemoteTag string
}

// StubReleaseGit writes a git stub implementing spec and returns its
// ERUN_GIT_BIN routing pair. show-ref --verify --quiet always exits 1 (no
// develop branch) so stable releases skip the sync-develop stage and push only
// the main branch.
func StubReleaseGit(t testing.TB, stubsDir string, spec ReleaseGitStubSpec) []string {
	t.Helper()
	tagProbe := `exit 1`
	headResolve := `exit 1`
	if spec.TagSHA != "" {
		tagProbe = `echo ` + shellSingleQuote(spec.TagSHA)
		headResolve = `echo ` + shellSingleQuote(spec.TagSHA)
	}
	remoteTags := `:`
	if spec.RemoteTag != "" {
		remoteTags = `printf '%s\trefs/tags/%s\n' ` + shellSingleQuote(spec.TagSHA) + ` ` + shellSingleQuote(spec.RemoteTag)
	}
	StubBinaryWithScript(t, stubsDir, "git", `case "$*" in
  *'rev-parse --abbrev-ref HEAD'*) echo `+shellSingleQuote(spec.Branch)+` ;;
  *'rev-parse --short HEAD'*) echo `+shellSingleQuote(spec.ShortCommit)+` ;;
  *'ls-remote --tags'*) `+remoteTags+` ;;
  *'show-ref --verify --quiet'*) exit 1 ;;
  *'^{}'*) `+tagProbe+` ;;
  *'rev-parse HEAD'*) `+headResolve+` ;;
  *) : ;;
esac
exit 0
`)
	return StubEnv(stubsDir, "git")
}

// RunGit runs git in dir so scenarios can set up branches, tags, or remotes
// after SeedReleaseRepo.
func RunGit(t testing.TB, dir string, args ...string) {
	t.Helper()
	if err := exec("git", args, dir); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

// SeedDevopsRepo creates a minimal <tenant>-devops chart layout (including a
// per-environment values file) so deploy/build commands resolve a kubernetes
// deploy context.
func SeedDevopsRepo(t testing.TB, setup env.Setup, tenant, environment string) string {
	t.Helper()
	return SeedDevopsRepoAt(t, setup.Cwd, tenant, environment)
}

// SeedDevopsRepoAt is SeedDevopsRepo with an explicit project root, for
// scenarios whose tenant project root is not the default setup.Cwd (e.g. a
// git-initialized repo directory named after the tenant).
func SeedDevopsRepoAt(t testing.TB, root, tenant, environment string) string {
	t.Helper()
	devops := filepath.Join(root, tenant+"-devops")
	chart := filepath.Join(devops, "k8s", tenant+"-devops")
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", chart, err)
	}
	mustWrite(t, filepath.Join(chart, "Chart.yaml"),
		"apiVersion: v2\nname: "+tenant+"-devops\nversion: 0.0.1\n",
	)
	mustWrite(t, filepath.Join(chart, "values.yaml"), "tenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(chart, "values."+strings.ToLower(strings.TrimSpace(environment))+".yaml"),
		"environment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(devops, "VERSION"), "1.0.0\n")
	return chart
}

// SeedDevopsRuntimeDockerfile writes the runtime-image Dockerfile so commands
// that resolve runtime-image builds (notably open --snapshot for the local
// environment) reach the docker-build branch in dry-run.
func SeedDevopsRuntimeDockerfile(t testing.TB, setup env.Setup, tenant string) {
	t.Helper()
	dir := filepath.Join(setup.Cwd, tenant+"-devops", "docker", tenant+"-devops")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	mustWrite(t, filepath.Join(dir, "Dockerfile"), "FROM alpine:3.22\n")
}

// SeedProjectDockerfile writes a minimal Dockerfile under setup.Cwd so
// commands that key off "current directory contains a Dockerfile" (notably
// the root `erun push` shorthand) register for the test invocation.
func SeedProjectDockerfile(t testing.TB, setup env.Setup) {
	t.Helper()
	mustWrite(t, filepath.Join(setup.Cwd, "Dockerfile"), "FROM alpine\n")
}

// SeedProjectK8sConfig writes the project .erun/config.yaml so deploy commands
// pick up the configured deployment plan (ordering + parallel grouping) instead
// of the hardcoded fallback.
func SeedProjectK8sConfig(t testing.TB, setup env.Setup, body string) {
	t.Helper()
	dir := filepath.Join(setup.Cwd, ".erun")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	mustWrite(t, filepath.Join(dir, "config.yaml"), body)
}

// SeedTerraformEnvRoot materializes a platform's per-env Terraform root so erun
// terraform resolves a folder to run in, mirroring the layout the
// erun-blueprint-platform skill scaffolds. The per-env common.tf is a real
// symlink in production trees; the command only needs the folder + tfvars to
// resolve, so the fixture writes plain files.
func SeedTerraformEnvRoot(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	SeedTerraformEnvRootAt(t, filepath.Join(setup.Cwd, "terraform-"+tenant), environment)
}

// SeedTerraformEnvRootAt is SeedTerraformEnvRoot with an explicit base dir, for
// scenarios that relocate the Terraform base via .erun/config.yaml paths.terraform
// instead of using the conventional terraform-<tenant>/ root.
func SeedTerraformEnvRootAt(t testing.TB, root, environment string) {
	t.Helper()
	envDir := filepath.Join(root, environment)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", envDir, err)
	}
	mustWrite(t, filepath.Join(root, "common.tf"), "terraform {\n  required_version = \">= 1.3\"\n}\n")
	mustWrite(t, filepath.Join(root, "variables.tf"), "variable \"base_domain\" {\n  type = string\n}\n")
	mustWrite(t, filepath.Join(envDir, "main.tf"), "# "+environment+" services\n")
	mustWrite(t, filepath.Join(envDir, environment+".tfvars"), "base_domain = \"erunpaas.com\"\n")
}

// SeedProjectPathsConfig writes the project .erun/config.yaml paths: block so
// build/deploy/terraform resolve the docker/, k8s/, terraform, and VERSION
// locations from config instead of the <tenant>-devops convention. Empty fields
// are omitted.
func SeedProjectPathsConfig(t testing.TB, setup env.Setup, docker, k8s, terraform, version string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("paths:\n")
	for _, kv := range []struct{ key, value string }{
		{"docker", docker},
		{"k8s", k8s},
		{"terraform", terraform},
		{"version", version},
	} {
		if strings.TrimSpace(kv.value) != "" {
			b.WriteString("    " + kv.key + ": " + kv.value + "\n")
		}
	}
	SeedProjectK8sConfig(t, setup, b.String())
}

// SeedDockerComponentAt writes <dockerDir>/<component>/Dockerfile so a build
// resolves a component build context at a caller-chosen docker root. dockerDir
// must be named "docker" — the standard-layout checks key off the folder name.
func SeedDockerComponentAt(t testing.TB, dockerDir, component string) {
	t.Helper()
	dir := filepath.Join(dockerDir, component)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	mustWrite(t, filepath.Join(dir, "Dockerfile"), "FROM alpine:3.22\n")
}

// SeedK8sChartAt writes <k8sDir>/<chartName>/Chart.yaml (plus values files) so a
// deploy resolves a chart at a caller-chosen k8s root. k8sDir must be named
// "k8s" — the chart-discovery checks key off the folder name.
func SeedK8sChartAt(t testing.TB, k8sDir, chartName, tenant, environment string) {
	t.Helper()
	chart := filepath.Join(k8sDir, chartName)
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", chart, err)
	}
	mustWrite(t, filepath.Join(chart, "Chart.yaml"), "apiVersion: v2\nname: "+chartName+"\nversion: 0.0.1\n")
	mustWrite(t, filepath.Join(chart, "values.yaml"), "tenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(chart, "values."+strings.ToLower(strings.TrimSpace(environment))+".yaml"), "environment: "+environment+"\n")
}

// SeedDevopsBackendCharts seeds the three opt-in backend charts
// (erun-backend-postgres, erun-backend-db, erun-backend-api) alongside
// SeedDevopsRepo's runtime chart so deploy --dry-run can resolve them.
func SeedDevopsBackendCharts(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	envSlug := strings.ToLower(strings.TrimSpace(environment))
	k8s := filepath.Join(setup.Cwd, tenant+"-devops", "k8s")
	for _, name := range []string{"erun-backend-postgres", "erun-backend-db", "erun-backend-api"} {
		chart := filepath.Join(k8s, name)
		if err := os.MkdirAll(chart, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", chart, err)
		}
		mustWrite(t, filepath.Join(chart, "Chart.yaml"),
			"apiVersion: v2\nname: "+name+"\nversion: 0.0.1\n",
		)
		mustWrite(t, filepath.Join(chart, "values.yaml"), "tenant: "+tenant+"\n")
		mustWrite(t, filepath.Join(chart, "values."+envSlug+".yaml"), "environment: "+environment+"\n")
	}
}

// SeedDevopsComponentChart seeds a single non-opt-in, non-runtime component
// chart (e.g. a docs chart) to prove opt-in-only resolution does not auto-deploy
// it. Usable with or without SeedDevopsRepo: without it the tree is
// component-only and the runtime deploys via the published erun-devops chart.
func SeedDevopsComponentChart(t testing.TB, setup env.Setup, tenant, environment, chartName string) {
	t.Helper()
	envSlug := strings.ToLower(strings.TrimSpace(environment))
	chart := filepath.Join(setup.Cwd, tenant+"-devops", "k8s", chartName)
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", chart, err)
	}
	mustWrite(t, filepath.Join(chart, "Chart.yaml"), "apiVersion: v2\nname: "+chartName+"\nversion: 0.0.1\n")
	mustWrite(t, filepath.Join(chart, "values.yaml"), "tenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(chart, "values."+envSlug+".yaml"), "environment: "+environment+"\n")
}

// SeedDevopsUmbrellaChart seeds a component chart with an OCI dependency on a
// published erun-* subchart (the erun-blueprint-platform pattern, e.g.
// team-backend-api wrapping erun-backend-api). deploy must helm dependency build
// such a chart before install, so use it to prove the dependency-build step is
// traced.
func SeedDevopsUmbrellaChart(t testing.TB, setup env.Setup, tenant, environment, chartName, dependencyName string) {
	t.Helper()
	envSlug := strings.ToLower(strings.TrimSpace(environment))
	chart := filepath.Join(setup.Cwd, tenant+"-devops", "k8s", chartName)
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", chart, err)
	}
	mustWrite(t, filepath.Join(chart, "Chart.yaml"),
		"apiVersion: v2\nname: "+chartName+"\nversion: 0.1.0\n"+
			"dependencies:\n"+
			"  - name: "+dependencyName+"\n"+
			"    version: \"1.0.0\"\n"+
			"    repository: \"oci://ghcr.io/sophium/charts\"\n",
	)
	mustWrite(t, filepath.Join(chart, "values.yaml"), "tenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(chart, "values."+envSlug+".yaml"),
		dependencyName+":\n  tenant: "+tenant+"\n  environment: "+environment+"\n",
	)
}

// StubBinary writes a POSIX-shell stub (routed via ERUN_<NAME>_BIN) that prints
// stdout and exits 0, to drive non-dry-run code paths without the real
// aws/kubectl/helm/docker binaries on PATH.
//
// For dry-run scenarios that need the stub to produce decision-input output
// (e.g. kubectl reporting "deployment exists" vs "not found" so the open runner
// can pick its branch), prefer StubBinaryAdvanced, which also sets stderr and
// exit code.
func StubBinary(t testing.TB, dir, name, stdout string) string {
	return StubBinaryAdvanced(t, dir, name, StubBinarySpec{Stdout: stdout})
}

// StubBinarySpec configures a stub binary's response. The stub always
// matches its argv to the spec deterministically: same Stdout/Stderr/ExitCode
// regardless of how it is called. Use one stub per per-binary persona; if a
// scenario needs a binary to behave differently across calls, write multiple
// stubs at distinct paths.
type StubBinarySpec struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// StubBinaryAdvanced is StubBinary with explicit stderr and exit code. See
// StubBinary for the env-var routing contract.
func StubBinaryAdvanced(t testing.TB, dir, name string, spec StubBinarySpec) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\n" +
		"# erun integration stub for " + name + "\n"
	if spec.Stdout != "" {
		body += "printf '%s\\n' " + shellSingleQuote(spec.Stdout) + "\n"
	}
	if spec.Stderr != "" {
		body += "printf '%s\\n' " + shellSingleQuote(spec.Stderr) + " >&2\n"
	}
	body += "exit " + strconv.Itoa(spec.ExitCode) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", path, err)
	}
	return path
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// StubBinaryWithScript writes a stub binary whose POSIX-shell body is the
// caller-supplied script. Use this when a stub must branch on argv (e.g.,
// the AWS CLI returning JSON for sts get-caller-identity but exit 0 for
// configure set). Less ergonomic than StubBinary, so prefer that for
// scenarios where one fixed response is enough.
//
// The script body has full shell access; "$*" / "$1" / "$2" are the args
// the production code passed. The body should end with an explicit exit.
func StubBinaryWithScript(t testing.TB, dir, name, scriptBody string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\n# erun integration stub for " + name + "\n" + scriptBody
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", path, err)
	}
	return path
}

// StubBinaryFailFirstThenSucceed writes a stub that fails on its first
// invocation — printing stderrFirst to stderr and exiting exitCode — then
// drops a marker file so every later invocation exits 0 silently. Use it to
// drive a production retry/recovery loop through its failure arm into the
// success path (e.g. helm aborting an upgrade once, then succeeding after the
// recovery deletes the conflicting object). The marker lives in dir, so it is
// scoped to the scenario's stub directory and reset per scenario by env.New.
func StubBinaryFailFirstThenSucceed(t testing.TB, dir, name, stderrFirst string, exitCode int) string {
	t.Helper()
	marker := shellSingleQuote(filepath.Join(dir, name+"-failed-once"))
	script := "if [ ! -f " + marker + " ]; then\n" +
		"  : > " + marker + "\n" +
		"  printf '%s\\n' " + shellSingleQuote(stderrFirst) + " >&2\n" +
		"  exit " + strconv.Itoa(exitCode) + "\n" +
		"fi\n" +
		"exit 0"
	return StubBinaryWithScript(t, dir, name, script)
}

// StubEnv returns the env-var pairs that route the named binary lookups to
// the stub at dir/<name>. Pass each result through env.Setup.Env() concat.
func StubEnv(dir string, names ...string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		envName := "ERUN_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_BIN"
		out = append(out, envName+"="+filepath.Join(dir, name))
	}
	return out
}

// SeedGitRepo runs `git init` inside the given dir and creates one commit so
// commands that look at git state (release, diff, exec) can resolve a project
// root without any prompts.
func SeedGitRepo(t testing.TB, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := exec("git", []string{"init", "-q", "-b", "main"}, dir); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := exec("git", []string{"config", "user.email", "test@example"}, dir); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if err := exec("git", []string{"config", "user.name", "Test"}, dir); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	mustWrite(t, filepath.Join(dir, "README.md"), "# test\n")
	if err := exec("git", []string{"add", "."}, dir); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := exec("git", []string{"commit", "-q", "-m", "initial"}, dir); err != nil {
		t.Fatalf("git commit: %v", err)
	}
}

func exec(name string, args []string, dir string) error {
	cmd := osExecCommand(name, args, dir)
	return cmd.Run()
}

func mustWrite(t testing.TB, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

var (
	portSimBuildOnce sync.Once
	portSimPath      string
	portSimBuildErr  error
)

// PortSimBinary builds (once per process) a small Go program that listens on
// a TCP port until killed. Used as the body of a `kubectl port-forward` stub
// so production code's "is the local port reachable?" polling can succeed in
// integration tests without standing up a real cluster.
func PortSimBinary(t testing.TB) string {
	t.Helper()
	portSimBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "erun-portsim-*")
		if err != nil {
			portSimBuildErr = fmt.Errorf("mkdir portsim cache: %w", err)
			return
		}
		out := filepath.Join(dir, "portsim")
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			portSimBuildErr = fmt.Errorf("resolve fixture package path")
			return
		}
		pkgDir := filepath.Join(filepath.Dir(thisFile), "portsim")
		cmd := osexec.Command("go", "build", "-o", out, ".")
		cmd.Dir = pkgDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if combined, err := cmd.CombinedOutput(); err != nil {
			portSimBuildErr = fmt.Errorf("build portsim: %w: %s", err, combined)
			return
		}
		portSimPath = out
	})
	if portSimBuildErr != nil {
		t.Fatalf("%v", portSimBuildErr)
	}
	return portSimPath
}

// KubectlDeployedStubSpec describes the deployment shape the stubbed kubectl get
// deployment -o json should report so production's deployment-match check
// returns true and the open flow proceeds past the redeploy gate.
//
// The optional fields below extend the stub for real-run shell scenarios. Every
// zero value preserves the original silent-exit-0 behavior, so existing callers
// are unaffected.
type KubectlDeployedStubSpec struct {
	DeploymentName string
	ContainerName  string
	RepoPath       string
	SSHDEnabled    bool
	MCPPort        int
	SSHPort        int
	// ExecExitCodes, when non-empty, drives the interactive shell exec
	// branch (`kubectl ... exec -it deployment/... -- /bin/sh -lc <script>`):
	// the Nth call exits with the Nth code and the last code repeats for any
	// further calls. Calls are recorded one line per invocation in
	// <stubsDir>/exec-calls so tests can assert how many times the shell
	// loop re-entered kubectl exec (e.g. after a reattach-deploy handoff).
	ExecExitCodes []int
	// WaitExitCode, when non-zero, fails the deployment-availability wait
	// (`kubectl ... wait --for=condition=Available ... deployment/...`)
	// with this exit code after printing WaitStderr to stderr. Zero keeps
	// the wait succeeding silently.
	WaitExitCode int
	WaitStderr   string
	// PodsJSON, when non-empty, is written to <stubsDir>/pods.json and
	// returned verbatim for any `kubectl get pods ...` query. The open
	// flow's runtime diagnostics (`get pods -l app=<release> -o json`) and
	// the pod-replacement probe both read this shape.
	PodsJSON string
	// EventsJSON, when non-empty, is written to <stubsDir>/events.json and
	// returned verbatim for `kubectl get events -o json` (the runtime
	// diagnostics' per-pod warning-event lookup).
	EventsJSON string
	// SeedKeyFile, when non-empty, captures stdin of the non-interactive
	// SSH-key seeding call (`kubectl ... exec -i deployment/... -- /bin/sh
	// -c <seed-script>`) into this file so tests can assert the private key
	// was streamed on stdin and never via argv.
	SeedKeyFile string
	// DeploymentNotFound flips the deployment-check arms to report the
	// deployment as absent (NotFound on stderr, exit 1) instead of
	// present-and-matching, while keeping the port-forward simulator for
	// the forwards the open flow starts after its own deploy. Real-run
	// open scenarios use it to drive the deploy-then-forward path.
	DeploymentNotFound bool
}

// StubKubectlDeployed writes a kubectl stub that reports the named deployment as
// present-and-matching and runs the port-forward simulator for any port-forward
// invocation.
//
// The simulator processes are tracked via a PID file inside stubsDir so the
// t.Cleanup hook can reap them after the test; otherwise leftover listeners
// would hold the production ports across runs and break the next scenario with
// "local SSH port already in use".
func StubKubectlDeployed(t testing.TB, stubsDir string, spec KubectlDeployedStubSpec) []string {
	t.Helper()
	if err := os.MkdirAll(stubsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", stubsDir, err)
	}
	portsim := PortSimBinary(t)
	pidFile := filepath.Join(stubsDir, "portsim-pids")
	deploymentJSON := fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":%q,"env":[{"name":"ERUN_REPO_PATH","value":%q},{"name":"ERUN_SSHD_ENABLED","value":%q},{"name":"ERUN_MCP_PORT","value":"%d"},{"name":"ERUN_SSHD_PORT","value":"%d"}],"resources":{"limits":{}}}]}}}}`,
		spec.ContainerName, spec.RepoPath, formatStubBool(spec.SSHDEnabled), spec.MCPPort, spec.SSHPort)
	script := strings.Join([]string{
		// Production passes the port-forward mapping as a single positional
		// after the resource reference (e.g. "deployment/team-devops
		// 17000:17000").
		`is_port_forward=0`,
		`local_port=""`,
		`for arg in "$@"; do`,
		`  case "$arg" in`,
		`    port-forward) is_port_forward=1 ;;`,
		`    *:*)`,
		`      if [ "$is_port_forward" = "1" ]; then`,
		`        case "$arg" in`,
		`          *[!0-9:]*) : ;;`,
		`          *) local_port="${arg%%:*}" ;;`,
		`        esac`,
		`      fi ;;`,
		`  esac`,
		`done`,
		`if [ "$is_port_forward" = "1" ] && [ -n "$local_port" ]; then`,
		// Production-side reachability checks differ by service:
		//   SSH expects the server to greet with a "SSH-" prefix
		//   MCP expects a successful HTTP response on GET /mcp
		//   API expects a 2xx HTTP response on GET /healthz
		// portsim writes the per-port banner to satisfy the SSH check; for
		// the MCP/API ports it answers the probe's HTTP request with a 200.
		// Only the SSH port needs a banner here.
		`  banner=""`,
		`  if [ "$local_port" = "` + strconv.Itoa(spec.SSHPort) + `" ]; then`,
		`    banner="SSH-2.0-erun-portsim\r\n"`,
		`  fi`,
		`  ` + portsim + ` --port "$local_port" --banner "$banner" >/dev/null 2>&1 &`,
		`  pid=$!`,
		`  printf '%s %s\n' "$local_port" "$pid" >> '` + pidFile + `'`,
		`  # Stay alive while the simulator runs so production code that`,
		`  # treats the kubectl process as the port-forward owner sees it`,
		`  # as live. Block until the simulator exits.`,
		`  wait "$pid"`,
		`  exit 0`,
		`fi`,
		`# Argv-driven branching for non-port-forward kubectl invocations.`,
		`case "$*" in`,
	}, "\n")
	script += "\n" + kubectlDeployedOptionalArms(t, stubsDir, spec)
	if spec.DeploymentNotFound {
		script += strings.Join([]string{
			`  *"get deployment ` + spec.DeploymentName + ` -o name"*)`,
			`    printf '%s\n' 'Error from server (NotFound): deployments.apps "` + spec.DeploymentName + `" not found' >&2`,
			`    exit 1 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
	} else {
		script += strings.Join([]string{
			`  *"get deployment ` + spec.DeploymentName + ` -o name"*)`,
			`    printf 'deployment.apps/%s\n' '` + spec.DeploymentName + `' ;;`,
			`  *"get deployment ` + spec.DeploymentName + ` -o json"*)`,
			`    printf '%s' '` + deploymentJSON + `' ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
	}
	StubBinaryWithScript(t, stubsDir, "kubectl", script)
	t.Cleanup(func() {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			port, portErr := strconv.Atoi(fields[0])
			pid, pidErr := strconv.Atoi(fields[1])
			if pidErr != nil || pid <= 0 {
				continue
			}
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
			}
			// SIGKILL is async: block until the simulator's listener is
			// actually released so a sibling real-run scenario reusing the
			// same fixed ports does not trip its port-busy guard on this
			// scenario's teardown.
			if portErr == nil && port > 0 {
				waitForPortClosed(port, 3*time.Second)
			}
		}
	})
	return StubEnv(stubsDir, "kubectl")
}

// kubectlDeployedOptionalArms emits the optional stub case arms
// most-specific-first: the interactive exec -it arm leads so the bootstrap
// script passed as its last argv can never fall through into the
// pods/events/wait arms by substring accident.
func kubectlDeployedOptionalArms(t testing.TB, stubsDir string, spec KubectlDeployedStubSpec) string {
	t.Helper()
	var arms strings.Builder
	if len(spec.ExecExitCodes) > 0 {
		counterFile := filepath.Join(stubsDir, "exec-calls")
		arms.WriteString(`  *" exec -it "*)` + "\n")
		arms.WriteString(`    count=0` + "\n")
		arms.WriteString(`    if [ -f '` + counterFile + `' ]; then count=$(wc -l < '` + counterFile + `' | tr -d '[:space:]'); fi` + "\n")
		arms.WriteString(`    printf 'call\n' >> '` + counterFile + `'` + "\n")
		arms.WriteString(`    case "$count" in` + "\n")
		for index, code := range spec.ExecExitCodes[:len(spec.ExecExitCodes)-1] {
			fmt.Fprintf(&arms, `      %d) exit %d ;;`+"\n", index, code)
		}
		fmt.Fprintf(&arms, `      *) exit %d ;;`+"\n", spec.ExecExitCodes[len(spec.ExecExitCodes)-1])
		arms.WriteString(`    esac ;;` + "\n")
	}
	if spec.SeedKeyFile != "" {
		arms.WriteString(`  *" exec -i deployment/"*)` + "\n")
		arms.WriteString(`    cat > '` + spec.SeedKeyFile + `'` + "\n")
		arms.WriteString(`    exit 0 ;;` + "\n")
	}
	if spec.WaitExitCode != 0 {
		arms.WriteString(`  *" wait --for=condition=Available"*)` + "\n")
		if spec.WaitStderr != "" {
			arms.WriteString(`    printf '%s\n' ` + shellSingleQuote(spec.WaitStderr) + ` >&2` + "\n")
		}
		fmt.Fprintf(&arms, `    exit %d ;;`+"\n", spec.WaitExitCode)
	}
	if spec.PodsJSON != "" {
		podsFile := filepath.Join(stubsDir, "pods.json")
		mustWrite(t, podsFile, spec.PodsJSON)
		arms.WriteString(`  *" get pods "*)` + "\n")
		arms.WriteString(`    cat '` + podsFile + `'` + "\n")
		arms.WriteString(`    exit 0 ;;` + "\n")
	}
	if spec.EventsJSON != "" {
		eventsFile := filepath.Join(stubsDir, "events.json")
		mustWrite(t, eventsFile, spec.EventsJSON)
		arms.WriteString(`  *" get events "*)` + "\n")
		arms.WriteString(`    cat '` + eventsFile + `'` + "\n")
		arms.WriteString(`    exit 0 ;;` + "\n")
	}
	return arms.String()
}

// waitForPortClosed lets StubKubectlDeployed's teardown block until a killed
// port-forward simulator's listener is fully gone before the next scenario
// probes the same fixed port.
func waitForPortClosed(port int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(50 * time.Millisecond)
	}
}

func formatStubBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// SeedDeployInflightMarker writes an in-flight deploy marker into the test's
// XDG config dir so erun deploy --dry-run exercises the dedup branches
// (skip on identical hash, conflict on different hash, reclaim on dead PID).
// The marker filename mirrors erun-common's helmDeployReleaseKey format
// (<context>-<namespace>-<release>.json), so the sanitize logic here must track
// production's.
func SeedDeployInflightMarker(t testing.TB, setup env.Setup, kubernetesContext, namespace, releaseName string, record DeployInflightRecord) string {
	t.Helper()
	if record.StartedAt == "" {
		record.StartedAt = "2026-05-10T12:00:00Z"
	}
	dir := filepath.Join(setup.ConfigHome, "erun", "deploys")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir deploys dir: %v", err)
	}
	key := sanitizeFilename(kubernetesContext) + "-" + sanitizeFilename(namespace) + "-" + sanitizeFilename(releaseName)
	path := filepath.Join(dir, key+".json")
	body := fmt.Sprintf(`{
  "pid": %d,
  "started_at": %q,
  "params_hash": %q,
  "tenant": %q,
  "environment": %q,
  "version": %q
}
`, record.PID, record.StartedAt, record.ParamsHash, record.Tenant, record.Environment, record.Version)
	mustWrite(t, path, body)
	return path
}

// DeployInflightRecord captures the fields tests need to seed for the dedup
// integration scenarios.
type DeployInflightRecord struct {
	PID         int
	StartedAt   string
	ParamsHash  string
	Tenant      string
	Environment string
	Version     string
}

func sanitizeFilename(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}
