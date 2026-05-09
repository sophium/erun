// Package fixture writes deterministic seed configurations into an isolated
// HOME so erun subprocesses see a known tenant/environment/cloud-context layout
// without invoking interactive prompts.
package fixture

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
)

func osExecCommand(name string, args []string, dir string) *osexec.Cmd {
	cmd := osexec.Command(name, args...)
	cmd.Dir = dir
	return cmd
}

// SeedTenantEnv writes a minimal config tree:
//
//	$XDG_CONFIG_HOME/erun/config.yaml          (default tenant)
//	$XDG_CONFIG_HOME/erun/<tenant>/config.yaml (tenant)
//	$XDG_CONFIG_HOME/erun/<tenant>/<env>/config.yaml (env)
//
// Callers pass in the env Setup so we know the right XDG root.
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
			"runtimeversion: 1.0.0\n",
	)
}

// SeedTenantEnvWithSnapshot writes the same minimal config tree as
// SeedTenantEnv but persists snapshot=<enabled> on the env config so
// commands that key off EnvConfig.SnapshotEnabled() (notably `erun open`)
// exercise the persisted-preference branch instead of relying on the user
// passing --snapshot every time.
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

// SeedRemoteTenantEnvWithSSHD writes the same tree as SeedRemoteTenantEnv
// and additionally marks SSHD as enabled, so commands that gate on the
// SSHD-enabled remote environment (notably `erun open --vscode` and
// `--intellij`) reach past the validateIDEOptions guard.
func SeedRemoteTenantEnvWithSSHD(t testing.TB, setup env.Setup, tenant, environment string) {
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
			"remote: true\n"+
			"sshd:\n"+
			"  enabled: true\n",
	)
}

// SeedRemoteTenantEnv writes the same minimal config tree as SeedTenantEnv
// but marks the environment as remote so commands like `open`, `api`, and
// `mcp` exercise the kubectl port-forward and remote-runtime traces. The
// tenant's project root is rooted under setup.Home/git/<tenant> so cwd
// resolution still works when the scenario chdirs into it.
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
			"remote: true\n",
	)
}

// SeedRemoteRepoPathTenantEnv writes a tenant/env tree where the env's
// repopath points to a path that does not exist locally and the env is
// flagged remote: true. The tenant projectroot still points at setup.Cwd so
// chart resolution finds the local <tenant>-devops module via cwd-based
// detection. Use this for regression scenarios where deploy must not
// readdir() the env's remote-host path.
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
			"remote: true\n",
	)
}

// SeedReleaseRepo materializes a minimal erun-devops layout (chart, two
// dockerfiles, VERSION file) inside dir, runs `git init -b <branch>`, and
// produces one initial commit. Use this for `release` scenarios that need
// a project root with the layout the release command expects to find.
// Returns the project root path (== dir).
func SeedReleaseRepo(t testing.TB, dir, branch string) string {
	t.Helper()
	releaseRoot := filepath.Join(dir, "erun-devops")
	for _, sub := range []string{
		filepath.Join(releaseRoot, "k8s", "api"),
		filepath.Join(releaseRoot, "docker", "api"),
		filepath.Join(releaseRoot, "docker", "base"),
	} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	mustWrite(t, filepath.Join(releaseRoot, "VERSION"), "1.4.2\n")
	mustWrite(t, filepath.Join(releaseRoot, "k8s", "api", "Chart.yaml"), "apiVersion: v2\nname: api\nversion: 0.1.0\nappVersion: 0.1.0\n")
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

// RunGit runs `git <args...>` inside dir. Useful for scenarios that need
// to set up branches, tags, or remotes after SeedReleaseRepo.
func RunGit(t testing.TB, dir string, args ...string) {
	t.Helper()
	if err := exec("git", args, dir); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

// SeedDevopsRepo creates a minimal <tenant>-devops chart layout under
// setup.Cwd so commands that look for a kubernetes deploy context find one.
// Writes a values.<environment>.yaml so deploy --dry-run can resolve the
// per-environment values file. Returns the path to the chart directory in case
// tests want to assert on it.
func SeedDevopsRepo(t testing.TB, setup env.Setup, tenant, environment string) string {
	t.Helper()
	devops := filepath.Join(setup.Cwd, tenant+"-devops")
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

// SeedDevopsRuntimeDockerfile writes a Dockerfile at the canonical location
// <setup.Cwd>/<tenant>-devops/docker/<tenant>-devops/Dockerfile so commands
// that resolve runtime-image builds (notably `erun open --snapshot` for the
// local environment) reach the docker-build branch in dry-run.
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

// SeedProjectK8sConfig writes setup.Cwd/.erun/config.yaml with a k8s
// section so deploy commands pick up the configured deployment plan
// (ordering + parallel grouping) instead of the hardcoded fallback.
func SeedProjectK8sConfig(t testing.TB, setup env.Setup, body string) {
	t.Helper()
	dir := filepath.Join(setup.Cwd, ".erun")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	mustWrite(t, filepath.Join(dir, "config.yaml"), body)
}

// SeedDevopsBackendCharts seeds the three opt-in backend charts
// (erun-backend-postgres, erun-backend-db, erun-backend-api) alongside the
// runtime chart created by SeedDevopsRepo. Each chart gets a Chart.yaml plus a
// values.<environment>.yaml so deploy --dry-run can resolve them.
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

// StubBinary writes a small POSIX shell script that the production runners
// pick up via the ERUN_<NAME>_BIN environment variable. The script prints
// stdout and exits 0. Tests use this to drive non-dry-run code paths without
// needing the real `aws`/`kubectl`/`helm`/`docker` binaries on PATH.
//
// For dry-run scenarios that need the stub to produce decision-input output
// (e.g. kubectl reporting "deployment exists" vs "not found" so the open
// runner can pick its branch), prefer StubBinaryAdvanced which also lets
// callers set stderr and exit code.
//
// Returns the absolute path to the stub. Set ERUN_<NAME>_BIN to that path in
// the subprocess env to route invocations through the stub.
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

// StubBinaryAdvanced writes a POSIX shell stub that emits the given stdout
// and stderr and exits with the given code. See StubBinary for the env-var
// routing contract.
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

// KubectlDeployedStubSpec describes the deployment shape the stubbed
// `kubectl get deployment ... -o json` should report so production code's
// deployment-match check (eruncommon/deploy.go::deploymentMatchesExpectedSettings)
// returns true and the open flow proceeds past the redeploy gate.
type KubectlDeployedStubSpec struct {
	DeploymentName string
	ContainerName  string
	RepoPath       string
	SSHDEnabled    bool
	MCPPort        int
	APIPort        int
	SSHPort        int
}

// StubKubectlDeployed writes a kubectl stub at <stubsDir>/kubectl that
// reports the named deployment as present and matching the spec for
// deployment-check JSON queries, runs the port-forward simulator for any
// `port-forward` invocation, and exits 0 silently for everything else.
//
// The simulator processes are tracked via a PID file inside stubsDir so
// the t.Cleanup hook can reap them after the test; otherwise leftover
// listeners would hold the production ports across runs and break the
// next scenario with "local SSH port already in use".
//
// The returned env-var slice routes production kubectl invocations through
// the stub via ERUN_KUBECTL_BIN.
func StubKubectlDeployed(t testing.TB, stubsDir string, spec KubectlDeployedStubSpec) []string {
	t.Helper()
	if err := os.MkdirAll(stubsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", stubsDir, err)
	}
	portsim := PortSimBinary(t)
	pidFile := filepath.Join(stubsDir, "portsim-pids")
	deploymentJSON := fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":%q,"env":[{"name":"ERUN_REPO_PATH","value":%q},{"name":"ERUN_SSHD_ENABLED","value":%q},{"name":"ERUN_MCP_PORT","value":"%d"},{"name":"ERUN_API_PORT","value":"%d"},{"name":"ERUN_SSHD_PORT","value":"%d"}],"resources":{"limits":{}}}]}}}}`,
		spec.ContainerName, spec.RepoPath, formatStubBool(spec.SSHDEnabled), spec.MCPPort, spec.APIPort, spec.SSHPort)
	script := strings.Join([]string{
		// Find the local port in `port-forward ... LOCAL:REMOTE [...]` argv.
		// Production passes the mapping as a single positional after the
		// resource reference (e.g. "deployment/team-devops 17000:17000").
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
		//   SSH expects the first 4 bytes to be "SSH-"
		//   MCP expects an HTTP response on POST /mcp
		//   API expects an HTTP response on /healthz
		// The simulator sends a per-port banner that satisfies the SSH
		// check; MCP/API checks fall back to bare-TCP-reachable on a
		// 200ms timeout, which a plain accept-and-close already passes.
		`  banner=""`,
		`  if [ "$local_port" = "` + strconv.Itoa(spec.SSHPort) + `" ]; then`,
		`    banner="SSH-2.0-erun-portsim\r\n"`,
		`  fi`,
		`  ` + portsim + ` --port "$local_port" --banner "$banner" >/dev/null 2>&1 &`,
		`  pid=$!`,
		`  printf '%s\n' "$pid" >> '` + pidFile + `'`,
		`  # Stay alive while the simulator runs so production code that`,
		`  # treats the kubectl process as the port-forward owner sees it`,
		`  # as live. Block until the simulator exits.`,
		`  wait "$pid"`,
		`  exit 0`,
		`fi`,
		`# Argv-driven branching for non-port-forward kubectl invocations.`,
		`case "$*" in`,
		`  *"get deployment ` + spec.DeploymentName + ` -o name"*)`,
		`    printf 'deployment.apps/%s\n' '` + spec.DeploymentName + `' ;;`,
		`  *"get deployment ` + spec.DeploymentName + ` -o json"*)`,
		`    printf '%s' '` + deploymentJSON + `' ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	StubBinaryWithScript(t, stubsDir, "kubectl", script)
	t.Cleanup(func() {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			pid, err := strconv.Atoi(line)
			if err != nil || pid <= 0 {
				continue
			}
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
			}
		}
	})
	return StubEnv(stubsDir, "kubectl")
}


func formatStubBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

