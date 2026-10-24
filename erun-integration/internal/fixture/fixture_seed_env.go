package fixture

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
)

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

// SeedHostTenantEnv seeds a host env: a worktree with no pod and no cluster at
// all, so unlike SeedTenantEnv it carries no kubernetescontext,
// containerregistry, or runtimeversion — none of those apply to a type with no
// pod to configure.
func SeedHostTenantEnv(t testing.TB, setup env.Setup, tenant, environment string) {
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
			"type: host\n",
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

// SeedTenantEnvDeployingComponents seeds an env whose saved deploy selection
// names platform components: the shape an operator meets whenever an
// environment hosting the platform's own API and database is stopped, and the
// pods those components keep running are read as a stop that did not finish.
// The runtime's own name is in the selection on purpose — `erun deploy` accepts
// it — so a scenario can pin that a stop reports the components and never
// itself.
func SeedTenantEnvDeployingComponents(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	SeedTenantEnv(t, setup, tenant, environment)
	appendEnvConfig(t, setup, tenant, environment,
		"deploy:\n"+
			"  components:\n"+
			"    - "+tenant+"-devops\n"+
			"    - "+tenant+"-backend-api\n"+
			"    - "+tenant+"-backend-postgres\n")
}

// SeedStoppedTenantEnvDeployingComponents is that env already stopped — the
// state a Stop press meets as a no-op, where the surviving components are the
// whole explanation for why nothing changed.
func SeedStoppedTenantEnvDeployingComponents(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	SeedTenantEnvDeployingComponents(t, setup, tenant, environment)
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
