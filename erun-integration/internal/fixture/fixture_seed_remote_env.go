package fixture

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
)

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

// SeedRuntimeTenantEnvWithDeployComponents writes a runtime-type env tree that
// also carries a saved deploy.components selection. It models the env as the
// host sees it, so a scenario can set the in-pod identity and still hand the
// process a selection that resolved to something — the contrast to
// SeedRuntimeTenantEnv, whose config has no deploy block at all and is what the
// pod actually carries.
func SeedRuntimeTenantEnvWithDeployComponents(t testing.TB, setup env.Setup, tenant, environment string, components []string) {
	t.Helper()
	SeedRuntimeTenantEnv(t, setup, tenant, environment)
	envDir := filepath.Join(setup.ConfigHome, "erun", tenant, environment)
	contents := "name: " + environment + "\n" +
		"repopath: " + filepath.Join(setup.Home, "git", tenant) + "\n" +
		"kubernetescontext: test-context\n" +
		"containerregistry: registry.example/test\n" +
		"runtimeversion: 1.0.0\n" +
		"type: runtime\n"
	if len(components) > 0 {
		contents += "deploy:\n  components:\n"
		for _, component := range components {
			contents += "    - " + component + "\n"
		}
	}
	mustWrite(t, filepath.Join(envDir, "config.yaml"), contents)
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

// SeedRuntimeTenantEnvNoRepoPath writes a runtime-type env tree with NO
// repopath at all, reproducing the shape the backend's provisioning deploy,
// stop, and delete Jobs seed for a control-plane-created tenant with no
// project (deployexec.bootstrapEnvironmentScript writes exactly this: type
// and kubernetescontext — never repopath, since nothing on the tenant's side
// was ever `erun init`ed, and never a runtime version, which nothing on this
// path reads back). Every other runtime fixture pins a repopath, so this is
// the single fixture that locks the repo-path-optional resolution for a
// genuinely projectless env.
func SeedRuntimeTenantEnvNoRepoPath(t testing.TB, setup env.Setup, tenant, environment string) {
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
		"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"type: runtime\n"+
			"kubernetescontext: test-context\n",
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
