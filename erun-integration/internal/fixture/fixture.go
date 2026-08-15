// Package fixture writes deterministic seed configurations into an isolated
// HOME so erun subprocesses see a known tenant/environment/cloud-context layout
// without invoking interactive prompts.
package fixture

import (
	"archive/tar"
	"bytes"
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

// SeedStoppedTenantEnv seeds a tenant env the operator has stopped, so a
// scenario can exercise the recorded stop intent — the durable half of `erun
// stop` that makes deploy re-render replicas: 0 and open wake the environment.
func SeedStoppedTenantEnv(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	SeedTenantEnv(t, setup, tenant, environment)
	appendEnvConfig(t, setup, tenant, environment, "stopped: true\n")
}

// appendEnvConfig adds extra keys to an already-seeded env config, so seed
// variants stay one line instead of a copy of the whole tree.
func appendEnvConfig(t testing.TB, setup env.Setup, tenant, environment, extra string) {
	t.Helper()
	path := filepath.Join(setup.ConfigHome, "erun", tenant, environment, "config.yaml")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env config %s: %v", path, err)
	}
	mustWrite(t, path, string(existing)+extra)
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

// SeedRemoteTenantEnvWithWorkspaceSync seeds a remote-agent env with SSHD and
// workspace sync enabled, a local mirror directory, and a fixed public-key path,
// pinning a high localportrangestart so the SSH port-forward preview never
// collides with a developer's live erun session on the default 17000 range. It
// backs the `erun doctor --repair-workspace-sync` scenario.
func SeedRemoteTenantEnvWithWorkspaceSync(t testing.TB, setup env.Setup, tenant, environment string, rangeStart int) {
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
	mirrorPath := filepath.Join(setup.Home, "mirror", tenant+"-"+environment)
	if err := os.MkdirAll(mirrorPath, 0o755); err != nil {
		t.Fatalf("mkdir mirror %s: %v", mirrorPath, err)
	}
	keyPath := filepath.Join(setup.Home, ".ssh", "id_ed25519.pub")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("mkdir .ssh %s: %v", filepath.Dir(keyPath), err)
	}
	mustWrite(t, keyPath, "ssh-ed25519 AAAATESTKEY erun-integration\n")

	envContents := "name: " + environment + "\n" +
		"repopath: " + repoPath + "\n" +
		"kubernetescontext: test-context\n" +
		"containerregistry: registry.example/test\n" +
		"runtimeversion: 1.0.0\n" +
		"type: remote-agent\n" +
		"localportrangestart: " + strconv.Itoa(rangeStart) + "\n" +
		"sshd:\n" +
		"  enabled: true\n" +
		"  publickeypath: " + keyPath + "\n" +
		"  workspacesync:\n" +
		"    enabled: true\n" +
		"    localpath: " + mirrorPath + "\n"

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

// SeedRuntimeTenantEnv writes a runtime-type env tree pinned to a version. It is
// the fixture for the retype lane: a runtime env is remote-worktree like a
// remote-agent one, so it is the shape that proves a type change lands between
// two remote-worktree types rather than only out of local-agent.
func SeedRuntimeTenantEnv(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	SeedRemoteTenantEnv(t, setup, tenant, environment)
	envDir := filepath.Join(setup.ConfigHome, "erun", tenant, environment)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+filepath.Join(setup.Home, "git", tenant)+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n"+
			"type: runtime\n",
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

// SeedLocalTenantEnvWithAWSAlias seeds an env on a local cluster carrying an AWS
// alias but no cloud context, with the alias registered in the root config so it
// actually resolves — the shape that produced both the expired-host-credential
// and the empty-AWS_REGION failures. ssoRegion and
// registry select which region tier the env exercises — the alias' Identity
// Center region, the region an ECR registry host encodes, or (both left plain)
// no resolvable region at all.
func SeedLocalTenantEnvWithAWSAlias(t testing.TB, setup env.Setup, tenant, environment, alias, ssoRegion, registry string) {
	t.Helper()
	SeedTenantEnv(t, setup, tenant, environment)
	rootBody := "defaulttenant: " + tenant + "\n" +
		"cloudproviders:\n" +
		"  - alias: " + alias + "\n" +
		"    provider: aws\n" +
		"    profile: erun-sso-test\n"
	if strings.TrimSpace(ssoRegion) != "" {
		rootBody += "    ssoregion: " + ssoRegion + "\n"
	}
	mustWrite(t, filepath.Join(setup.ConfigHome, "erun", "config.yaml"), rootBody)
	if strings.TrimSpace(registry) == "" {
		registry = "registry.example/test"
	}
	envDir := filepath.Join(setup.ConfigHome, "erun", tenant, environment)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+setup.Cwd+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: "+registry+"\n"+
			"runtimeversion: 1.0.0\n"+
			"type: local-agent\n"+
			"cloudprovideralias: "+alias+"\n",
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

// SeedClusterRegistryRemoteTenantEnv writes a remote-agent env whose registry is
// an in-cluster (`--cluster-registry`) entry — the erun-registry Service resolved
// from the kube-context, holding the tenant's built app images but never the erun
// platform chart. It pins a runtimeimage on ghcr so deploy must resolve the
// runtime chart/registry from the runtime image's own registry, not the in-cluster
// pull host. No local <tenant>-devops chart, so deploy takes the published path.
func SeedClusterRegistryRemoteTenantEnv(t testing.TB, setup env.Setup, tenant, environment, remoteRepoPath string) {
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
			"runtimeimage: ghcr.io/sophium/erun-devops\n"+
			"runtimeversion: 1.0.0\n"+
			"type: remote-agent\n"+
			"containerregistries:\n"+
			"    - cluster: {}\n"+
			"      roles:\n"+
			"        - build\n"+
			"        - deploy\n",
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

// WorkspaceSyncSSHStubSpec answers the several different questions one
// workspace-sync pass asks the same `ssh` — the pod's file listing, its
// fingerprints, and the archive its fetch step streams.
type WorkspaceSyncSSHStubSpec struct {
	// IndexPaths is what `git ls-files -coz` reports: the pod's Git-visible files.
	IndexPaths []string
	// StatLines are `stat -c '%s %Y %n'` records (size, mtime, path). They are
	// what lets a pass skip an unchanged file; with none, everything is fetched.
	StatLines []string
	// ArchivePath is a tar file the fetch step receives verbatim, standing in for
	// the pod's `tar -cf -`. Empty makes the fetch fail.
	ArchivePath string
}

// StubWorkspaceSyncSSH writes the argv-branching `ssh` stub a real sync pass
// needs and returns its env routing. The pass's fingerprint listing pipes the
// index listing into stat, so the stat branch has to be matched before the index
// listing it contains.
func StubWorkspaceSyncSSH(t testing.TB, dir string, spec WorkspaceSyncSSHStubSpec) []string {
	t.Helper()
	statBranch := ":"
	if len(spec.StatLines) > 0 {
		statBranch = `printf '%s\n' ` + shellSingleQuoteAll(spec.StatLines)
	}
	indexBranch := ":"
	if len(spec.IndexPaths) > 0 {
		indexBranch = `printf '%s\000' ` + shellSingleQuoteAll(spec.IndexPaths)
	}
	archiveBranch := "exit 1"
	if spec.ArchivePath != "" {
		archiveBranch = "cat " + shellSingleQuote(filepath.ToSlash(spec.ArchivePath))
	}
	// The readiness probe runs a bare `true`; everything not answered here — the
	// outputs listing in particular — is "nothing there yet", which is exit 1.
	StubBinaryWithScript(t, dir, "ssh", `case "$*" in
  *' true') : ;;
  *'stat -c'*) `+statBranch+` ;;
  *'git ls-files -coz'*) `+indexBranch+` ;;
  *'git ls-files -dz'*) : ;;
  *'git ls-files -sz'*) : ;;
  *'tar --null'*) `+archiveBranch+` ;;
  *) exit 1 ;;
esac
exit 0
`)
	return StubEnv(dir, "ssh")
}

// TarEntry is one regular file in an archive written by WriteTarArchive.
type TarEntry struct {
	Path string
	Body []byte
}

// WriteTarArchive writes the archive a stubbed pod streams back. Entries carry
// an explicit mtime so a scenario can assert what the mirror did with the file
// it received rather than with whatever the host clock stamped.
func WriteTarArchive(t testing.TB, path string, entries []TarEntry, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for archive %s: %v", path, err)
	}
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.Path,
			Mode:     0o644,
			Size:     int64(len(entry.Body)),
			ModTime:  modTime,
			Typeflag: tar.TypeReg,
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("write archive header %s: %v", entry.Path, err)
		}
		if _, err := writer.Write(entry.Body); err != nil {
			t.Fatalf("write archive body %s: %v", entry.Path, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close archive %s: %v", path, err)
	}
	mustWrite(t, path, buffer.String())
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
	mustWrite(t, filepath.Join(root, "common.tf"), "terraform {\n  required_version = \">= 1.3\"\n  backend \"local\" {}\n}\n")
	mustWrite(t, filepath.Join(root, "variables.tf"), "variable \"base_domain\" {\n  type = string\n}\n")
	mustWrite(t, filepath.Join(envDir, "main.tf"), "# "+environment+" services\n")
	mustWrite(t, filepath.Join(envDir, environment+".tfvars"), "base_domain = \"erunpaas.com\"\n")
}

// SeedProjectPathsConfig writes the project .erun/config.yaml paths: block so
// build/deploy/terraform resolve the docker/, k8s/, terraform, and VERSION
// locations from config instead of the <tenant>-devops convention. dockerContext
// sets paths.dockercontext (repo-root|component) to override the build-context
// heuristic. Empty fields are omitted.
func SeedProjectPathsConfig(t testing.TB, setup env.Setup, docker, dockerContext, k8s, terraform, version string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("paths:\n")
	for _, kv := range []struct{ key, value string }{
		{"docker", docker},
		{"dockercontext", dockerContext},
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
	body := "#!/bin/sh\n" +
		"# erun integration stub for " + name + "\n"
	if spec.Stdout != "" {
		body += "printf '%s\\n' " + shellSingleQuote(spec.Stdout) + "\n"
	}
	if spec.Stderr != "" {
		body += "printf '%s\\n' " + shellSingleQuote(spec.Stderr) + " >&2\n"
	}
	body += "exit " + strconv.Itoa(spec.ExitCode) + "\n"
	return writeStub(t, dir, name, body)
}

// stubExecPath is the path erun should exec for the stub, and the path StubEnv
// routes ERUN_<NAME>_BIN to. On Windows it is a compiled runner `.exe` (Windows
// can't exec a shebang script by name, and a .bat launcher mangles complex
// argv); everywhere else it is the sh script itself.
func stubExecPath(dir, name string) string {
	path := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		return path + ".exe"
	}
	return path
}

// stubArgvPreamble rebuilds "$@" from the NUL-delimited file the Windows stub
// runner writes, so a stub that branches on "$*"/"$1" sees the exact argv erun
// passed — which a Windows command line cannot preserve through sh. Inert
// everywhere else: with ERUN_STUB_ARGV_FILE unset the script keeps its real argv.
const stubArgvPreamble = "if [ -n \"${ERUN_STUB_ARGV_FILE:-}\" ]; then set --; while IFS= read -r -d '' __erun_arg; do set -- \"$@\" \"$__erun_arg\"; done < \"$ERUN_STUB_ARGV_FILE\"; fi\n"

// writeStub writes the sh-script body (with the argv preamble injected after the
// shebang) to dir/<name> and returns the path erun should exec. On Windows it
// also drops a dir/<name>.exe copy of the stub runner, which forwards argv
// faithfully to the sh script — so a stub erun execs by name works even though
// Windows can't launch a shebang script and a .bat can't preserve complex argv.
func writeStub(t testing.TB, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[:i+1] + stubArgvPreamble + body[i+1:]
	}
	scriptPath := filepath.Join(dir, name)
	if err := os.WriteFile(scriptPath, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", scriptPath, err)
	}
	if runtime.GOOS != "windows" {
		return scriptPath
	}
	exePath := scriptPath + ".exe"
	runner, err := os.ReadFile(stubRunnerExe(t))
	if err != nil {
		t.Fatalf("read stub runner: %v", err)
	}
	if err := os.WriteFile(exePath, runner, 0o755); err != nil {
		t.Fatalf("write stub runner copy %s: %v", exePath, err)
	}
	return exePath
}

var (
	stubRunnerOnce sync.Once
	stubRunnerPath string
	stubRunnerErr  error
)

// stubRunnerExe builds the argv-faithful stub runner once per test process and
// returns its path. Only used on Windows.
func stubRunnerExe(t testing.TB) string {
	t.Helper()
	stubRunnerOnce.Do(func() {
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			stubRunnerErr = fmt.Errorf("cannot locate fixture source to build stub runner")
			return
		}
		moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
		outDir, err := os.MkdirTemp("", "erun-stub-runner")
		if err != nil {
			stubRunnerErr = err
			return
		}
		out := filepath.Join(outDir, "erun-stub-runner.exe")
		cmd := osexec.Command("go", "build", "-o", out, "./internal/fixture/stubrunner")
		cmd.Dir = moduleRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			stubRunnerErr = fmt.Errorf("build stub runner: %v\n%s", err, output)
			return
		}
		stubRunnerPath = out
	})
	if stubRunnerErr != nil {
		t.Fatalf("stub runner: %v", stubRunnerErr)
	}
	return stubRunnerPath
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// shellSingleQuoteAll renders values as space-separated printf operands, so one
// format string repeats over them.
func shellSingleQuoteAll(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, shellSingleQuote(value))
	}
	return strings.Join(quoted, " ")
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
	body := "#!/bin/sh\n# erun integration stub for " + name + "\n" + scriptBody
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return writeStub(t, dir, name, body)
}

// StubBinaryMergingIntoRemoteOnce writes a stub for name that, on its first
// invocation only, commits and pushes one commit from repoDir onto origin's
// branch, then exits 0 like a plain stub. It stands in for a pull request
// merging while a long-running command is working, so a scenario can place the
// move at a chosen point in the command's own execution rather than before it
// starts. git is reached through the ERUN_GIT_BIN seam the scenario already
// routes: the scrubbed PATH holds no git for a stub to find.
func StubBinaryMergingIntoRemoteOnce(t testing.TB, dir, name, repoDir, branch, message string) string {
	t.Helper()
	// Forward slashes: embedded in the sh stub, where Git Bash handles a
	// backslash Windows path unreliably.
	marker := shellSingleQuote(filepath.ToSlash(filepath.Join(dir, name+"-merged-once")))
	repo := shellSingleQuote(filepath.ToSlash(repoDir))
	script := "if [ ! -f " + marker + " ]; then\n" +
		"  : > " + marker + "\n" +
		"  \"$ERUN_GIT_BIN\" -C " + repo + " commit -q --allow-empty -m " + shellSingleQuote(message) + " || exit 1\n" +
		"  \"$ERUN_GIT_BIN\" -C " + repo + " push -q origin " + shellSingleQuote(branch) + " || exit 1\n" +
		"fi\n" +
		"exit 0"
	return StubBinaryWithScript(t, dir, name, script)
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
	// Forward slashes: embedded in the sh stub, where Git Bash handles a
	// backslash Windows path unreliably.
	marker := shellSingleQuote(filepath.ToSlash(filepath.Join(dir, name+"-failed-once")))
	script := "if [ ! -f " + marker + " ]; then\n" +
		"  : > " + marker + "\n" +
		"  printf '%s\\n' " + shellSingleQuote(stderrFirst) + " >&2\n" +
		"  exit " + strconv.Itoa(exitCode) + "\n" +
		"fi\n" +
		"exit 0"
	return StubBinaryWithScript(t, dir, name, script)
}

// StubKubectlRuntimeRunState writes a kubectl stub that answers the runtime
// run-state read `erun stop` and `erun open` perform (`get deployment <release>
// -o jsonpath={.spec.replicas}/{.status.readyReplicas}`) with the supplied
// replica counts. That answer is a dry-run decision input: it is what decides
// whether the environment is stopped and therefore whether a scale is planned,
// and a trace alone cannot supply it. Every other kubectl call exits 0
// silently, which is all the surrounding dry-run scenarios need.
//
// Pass desired=0 for a stopped environment and desired=1 for a running one.
func StubKubectlRuntimeRunState(t testing.TB, dir string, desired, ready int) string {
	t.Helper()
	script := "case \"$*\" in\n" +
		"  *jsonpath=*) printf '%s' " + shellSingleQuote(strconv.Itoa(desired)+"/"+strconv.Itoa(ready)) + "; exit 0 ;;\n" +
		"esac\n" +
		"exit 0"
	return StubBinaryWithScript(t, dir, "kubectl", script)
}

// KubectlWorktreeClaimStubSpec shapes the answer a kubectl stub gives to the
// deploy's worktree-volume check (`get pvc <release>-worktree -o name`).
type KubectlWorktreeClaimStubSpec struct {
	// ClaimName is the PVC the check reads.
	ClaimName string
	// Stderr and ExitCode answer that read. A NotFound stderr with exit 1 is an
	// environment whose worktree still lives on the home volume; any other
	// non-zero answer is a cluster the check cannot read at all, which deploy
	// must treat as "unknown" rather than "settled".
	Stderr   string
	ExitCode int
}

// StubKubectlWorktreeClaim writes a kubectl stub that answers the worktree-claim
// read as the spec says and exits 0 silently for everything else, so the rest of
// a real-run rollout still completes. It branches on argv, which is why it lives
// here rather than inline in a scenario.
func StubKubectlWorktreeClaim(t testing.TB, dir string, spec KubectlWorktreeClaimStubSpec) []string {
	t.Helper()
	script := "case \"$*\" in\n" +
		"  *" + shellGlobEscape("get pvc "+spec.ClaimName+" -o name") + "*)\n"
	if spec.Stderr != "" {
		script += "    printf '%s\\n' " + shellSingleQuote(spec.Stderr) + " >&2\n"
	}
	script += "    exit " + strconv.Itoa(spec.ExitCode) + " ;;\n" +
		"esac\n" +
		"exit 0"
	StubBinaryWithScript(t, dir, "kubectl", script)
	return StubEnv(dir, "kubectl")
}

// shellGlobEscape neutralizes the glob metacharacters a case pattern would
// otherwise interpret, so a literal fragment matches as itself.
func shellGlobEscape(literal string) string {
	var escaped strings.Builder
	for _, r := range literal {
		switch r {
		case '*', '?', '[', ']', '\\', ' ':
			escaped.WriteRune('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}

// StubKubectlScalingRuntime writes a kubectl stub that remembers the replica
// count `kubectl scale` last set and answers every later run-state read with
// it. It is what lets one scenario run a whole stop → reconnect → open sequence
// as a sequence: with the fixed-answer stub above, a later `erun` invocation
// cannot see what an earlier one did, so an ordering regression (a reconnect
// scaling a stopped environment back up) would be invisible.
//
// The deployment-presence read (`-o name`) answers with the release so `open`
// gets past its "is the runtime deployed" check and reaches the wake decision
// this stub exists to drive.
func StubKubectlScalingRuntime(t testing.TB, dir, release string, desired int) string {
	t.Helper()
	// Forward slashes: embedded in the sh stub, where Git Bash handles a
	// backslash Windows path unreliably.
	state := shellSingleQuote(filepath.ToSlash(filepath.Join(dir, "kubectl-replicas")))
	script := "state=" + state + "\n" +
		"[ -f \"$state\" ] || printf '%s' " + shellSingleQuote(strconv.Itoa(desired)) + " > \"$state\"\n" +
		"case \"$*\" in\n" +
		"  *--replicas=*)\n" +
		"    for arg in \"$@\"; do\n" +
		"      case \"$arg\" in --replicas=*) printf '%s' \"${arg#--replicas=}\" > \"$state\" ;; esac\n" +
		"    done\n" +
		"    exit 0 ;;\n" +
		"  *jsonpath=*) replicas=$(cat \"$state\"); printf '%s/%s' \"$replicas\" \"$replicas\"; exit 0 ;;\n" +
		"  *-o\\ name*) printf 'deployment.apps/%s\\n' " + shellSingleQuote(release) + "; exit 0 ;;\n" +
		"esac\n" +
		"exit 0"
	return StubBinaryWithScript(t, dir, "kubectl", script)
}

// CodesignStubSpec configures the codesign stub. Host-side ad-hoc signing asks
// codesign two different questions — "is this already signed?" (`codesign -d`)
// and "sign it" (`codesign -s - -f`) — so the stub has to branch on argv, which
// is why it lives here rather than inline in a scenario.
type CodesignStubSpec struct {
	// AlreadySigned makes the display probe report an existing signature, which
	// is the branch where production must leave the artifact alone.
	AlreadySigned bool
	// SignExitCode and SignStderr shape the signing call's answer; a non-zero
	// exit is the diagnosable failure that must not lose the artifact.
	SignExitCode int
	SignStderr   string
}

// StubCodesign writes the codesign stub and returns the path to a log file that
// records one line per invocation ("<argv>"). The log is the only way a scenario
// can prove a call did *not* happen — that an already-signed artifact was never
// re-signed — because absence leaves no trace in the command's own output.
func StubCodesign(t testing.TB, dir string, spec CodesignStubSpec) string {
	t.Helper()
	// Forward slashes: embedded in the sh stub, where Git Bash handles a
	// backslash Windows path unreliably.
	logPath := filepath.Join(dir, "codesign-calls.log")
	quotedLog := shellSingleQuote(filepath.ToSlash(logPath))
	displayExit := 1
	if spec.AlreadySigned {
		displayExit = 0
	}
	script := "printf '%s\\n' \"$*\" >> " + quotedLog + "\n" +
		"case \"$1\" in\n" +
		"  -d) printf '%s\\n' 'erun stub codesign display' >&2; exit " + strconv.Itoa(displayExit) + " ;;\n" +
		"esac\n"
	if spec.SignStderr != "" {
		script += "printf '%s\\n' " + shellSingleQuote(spec.SignStderr) + " >&2\n"
	}
	script += "exit " + strconv.Itoa(spec.SignExitCode)
	StubBinaryWithScript(t, dir, "codesign", script)
	return logPath
}

// StubEnv returns the env-var pairs that route the named binary lookups to
// the stub at dir/<name>. Pass each result through env.Setup.Env() concat.
func StubEnv(dir string, names ...string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		envName := "ERUN_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_BIN"
		out = append(out, envName+"="+stubExecPath(dir, name))
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

// StartStalePortHolder starts a listener that binds a local port and never
// answers anything through it — the forward whose target pod was replaced. It
// returns the holder's real PID so a scenario can present it to erun's
// adopt-or-replace probes and then assert that erun actually stopped it; a
// fabricated PID must never be used for that, because production kills what the
// probe names.
func StartStalePortHolder(t testing.TB, port int) int {
	t.Helper()
	return startPortHolder(t, port, "--silent")
}

// StartServingPortHolder starts a listener that answers what erun's
// reachability probe asks — the working forward a scenario wants erun to adopt
// rather than replace. Adoption now depends on the tunnel carrying traffic, so
// a scenario that only claims a holder through lsof would be deciding on
// whatever else happens to hold that port on the host.
func StartServingPortHolder(t testing.TB, port int) int {
	t.Helper()
	return startPortHolder(t, port)
}

func startPortHolder(t testing.TB, port int, extra ...string) int {
	t.Helper()
	cmd := osexec.Command(PortSimBinary(t), append([]string{"--port", strconv.Itoa(port)}, extra...)...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start port holder on %d: %v", port, err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("port holder never bound 127.0.0.1:%d", port)
	return 0
}

// StalePortHolderStopped reports whether erun stopped the holder, waiting for
// the port to come free rather than for a fixed delay.
func StalePortHolderStopped(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 100*time.Millisecond)
		if err != nil {
			return true
		}
		_ = conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	return false
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
	// Forward-slash paths embedded into the sh stub body: this script runs
	// under Git Bash / Cygwin sh on Windows, where an unquoted backslash path
	// (e.g. the portsim invocation below) is mangled by backslash-as-escape and
	// the simulator never launches — production's port-forward reachability wait
	// then times out. Inert on Unix (ToSlash is a no-op).
	portsim := filepath.ToSlash(PortSimBinary(t))
	pidFile := filepath.ToSlash(filepath.Join(stubsDir, "portsim-pids"))
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
	// Forward-slash every path embedded into the sh stub body so Git Bash /
	// Cygwin sh on Windows reads them as paths, not escape sequences (ToSlash is
	// a no-op on Unix). See the rationale in StubKubectlDeployed.
	if len(spec.ExecExitCodes) > 0 {
		counterFile := filepath.ToSlash(filepath.Join(stubsDir, "exec-calls"))
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
		arms.WriteString(`    cat > '` + filepath.ToSlash(spec.SeedKeyFile) + `'` + "\n")
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
		podsFile := filepath.ToSlash(filepath.Join(stubsDir, "pods.json"))
		mustWrite(t, podsFile, spec.PodsJSON)
		arms.WriteString(`  *" get pods "*)` + "\n")
		arms.WriteString(`    cat '` + podsFile + `'` + "\n")
		arms.WriteString(`    exit 0 ;;` + "\n")
	}
	if spec.EventsJSON != "" {
		eventsFile := filepath.ToSlash(filepath.Join(stubsDir, "events.json"))
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
