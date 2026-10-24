package integration

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// seedTenantWithoutDefault leaves the default tenant unwritten and roots the
// project outside the scenario cwd, so the bare root command falls through to
// the interactive tenant-selection prompt.
func seedTenantWithoutDefault(t *testing.T, setup env.Setup, tenant, environment string) {
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
	writeTestFile(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: "+repoPath+"\n"+
			"name: "+tenant+"\n"+
			"defaultenvironment: "+environment+"\n",
	)
	writeTestFile(t, filepath.Join(envDir, "config.yaml"),
		"name: "+environment+"\n"+
			"repopath: "+repoPath+"\n"+
			"kubernetescontext: test-context\n"+
			"containerregistry: registry.example/test\n"+
			"runtimeversion: 1.0.0\n",
	)
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// remoteInitKubectlStub parameterizes the stubbed remote pod shell for
// non-dry-run remote-init scenarios.
type remoteInitKubectlStub struct {
	RepoExists               bool
	SSHConfigExists          bool
	HostConfigVerifyExitCode int
	LsRemoteFailures         int
	// RegistryCredentialConfigured answers the #1201 registry-credential-check
	// script: whether the pod appears to have a ghcr.io credential (docker
	// config, gh session, or GH_TOKEN/GITHUB_TOKEN). Defaults to false (no
	// credential), matching a freshly-deployed pod.
	RegistryCredentialConfigured bool
}

// stubRemoteInitKubectl stands in for the remote pod's shell so real-run
// remote-init scenarios stay deterministic without a cluster.
func stubRemoteInitKubectl(t *testing.T, dir string, spec remoteInitKubectlStub) {
	t.Helper()
	repoState := "repo_missing"
	if spec.RepoExists {
		repoState = "repo_exists"
	}
	sshConfigState := "missing"
	if spec.SSHConfigExists {
		sshConfigState = "exists"
	}
	lines := []string{
		`case "$*" in`,
		`  *__ERUN_REMOTE_PUBLIC_KEY__*)`,
		`    printf '` + repoState + `\n'`,
		`    printf '__ERUN_REMOTE_PUBLIC_KEY__\n'`,
		`    printf 'ssh-ed25519 AAAA-test-ed25519-key erun-test\n'`,
		`    printf '__ERUN_REMOTE_CODECOMMIT_PUBLIC_KEY__\n'`,
		`    printf 'ssh-rsa AAAA-test-codecommit-key erun-test\n'`,
		`    printf '__ERUN_REMOTE_SSH_CONFIG__\n'`,
		`    printf '` + sshConfigState + `\n'`,
		`    exit 0 ;;`,
	}
	credentialAnswer := "0"
	if spec.RegistryCredentialConfigured {
		credentialAnswer = "1"
	}
	lines = append(lines,
		`  *'.docker/config.json'*)`,
		`    printf '`+credentialAnswer+`\n'`,
		`    exit 0 ;;`,
	)
	if spec.HostConfigVerifyExitCode != 0 {
		lines = append(lines,
			`  *'test -s'*)`,
			`    printf 'Permission denied (publickey).\n' >&2`,
			`    exit `+strconv.Itoa(spec.HostConfigVerifyExitCode)+` ;;`,
		)
	}
	if spec.LsRemoteFailures > 0 {
		// Forward slashes: this path is embedded in the sh stub, and Git Bash's sh
		// handles a backslash Windows path in redirections unreliably.
		counter := filepath.ToSlash(filepath.Join(dir, "ls-remote-calls"))
		lines = append(lines,
			`  *ls-remote*)`,
			`    count=0`,
			`    if [ -f '`+counter+`' ]; then count=$(wc -l < '`+counter+`' | tr -d '[:space:]'); fi`,
			`    printf 'call\n' >> '`+counter+`'`,
			`    if [ "$count" -lt `+strconv.Itoa(spec.LsRemoteFailures)+` ]; then`,
			`      printf 'Permission denied (publickey).\n' >&2`,
			`      exit 1`,
			`    fi`,
			`    exit 0 ;;`,
		)
	}
	lines = append(lines,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	)
	fixture.StubBinaryWithScript(t, dir, "kubectl", strings.Join(lines, "\n"))
}

func TestInit(t *testing.T) {
	t.Parallel()
	t.Run("help", testInitHelp)
	initRemoteFlagScenarios(t)
	initTypeScenarios(t)
	initRegistryScenarios(t)
	t.Run("dry_run_creates_no_stray_config_directory", testInitDryRunCreatesNoStrayConfigDirectory)
	t.Run("rejects_hyphenated_tenant", testInitRejectsHyphenatedTenant)
	initPromptScenarios(t)
	initRemoteRealRunScenarios(t)
	initCredentialProvisioningScenarios(t)
	initLocalRealRunScenarios(t)
	initReinitScenarios(t)
}

func testInitHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/help", normalize.Apply(result.Combined))
}

func testInitDryRunCreatesNoStrayConfigDirectory(t *testing.T) {
	// `erun init --dry-run` created the environment's (and its tenant's)
	// config directory on disk even though the write-yaml the directory
	// exists for was correctly traced only. Reproduces the exact repro
	// shape — a brand-new tenant and a brand-new host environment — and
	// asserts the config root is byte-identical afterwards, not merely
	// that the command exited 0 and printed traces.
	setup := env.New(t)
	args := []string{
		"init", "erun", "ios-host",
		"--type", "host",
		"--project-root", setup.Cwd,
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	before := snapshotConfigTree(t, setup.ConfigHome)
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	after := snapshotConfigTree(t, setup.ConfigHome)
	if len(after) != 0 {
		t.Fatalf("dry-run init left stray entries under the config directory: %v\noutput:\n%s", after, result.Combined)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("dry-run init mutated the config directory\nbefore: %v\nafter:  %v", before, after)
	}
}

func testInitRejectsHyphenatedTenant(t *testing.T) {
	// A new tenant name containing a hyphen is rejected at creation, before
	// any tenant/env config is written, so the `<tenant>-<env>` namespace
	// mapping stays unambiguous (split on the first hyphen) and injective.
	// The gate fires only when the tenant is new; existing hyphenated
	// tenants load without re-validation, preserving back-compat.
	setup := env.New(t)
	args := []string{
		"init", "team-one", "prod",
		"--type", "runtime",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--no-git",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "init/rejects_hyphenated_tenant", normalize.Apply(result.Combined))
}
