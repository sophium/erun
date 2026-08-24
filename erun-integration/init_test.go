package integration

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// stubKubectlContexts feeds init's kubernetes-context selection deterministic
// input so the rendered select list stays stable across machines.
func stubKubectlContexts(t *testing.T, dir string, contexts []string, current string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$*" in`,
		`  *current-context*) printf '%s\n' '` + current + `' ;;`,
		`  *get-contexts*) printf '%s\n' ` + shellQuotedLines(contexts) + ` ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, dir, "kubectl", script)
}

func shellQuotedLines(lines []string) string {
	quoted := make([]string, 0, len(lines))
	for _, line := range lines {
		quoted = append(quoted, "'"+line+"'")
	}
	return strings.Join(quoted, " ")
}

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
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/help", normalize.Apply(result.Combined))
	})

	t.Run("remote_dry_run", func(t *testing.T) {
		setup := env.New(t)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--no-git",
			"--bootstrap",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "init/remote_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("remote_with_runtime_image_override", func(t *testing.T) {
		setup := env.New(t)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--runtime-image", "custom-devops",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--no-git",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "init/remote_with_runtime_image_override", normalize.Apply(result.Combined))
	})

	// A tagged --runtime-image is persisted tagless, so the deploy this
	// same init performs (and every later redeploy) pins the image to the env's
	// own runtime version instead of sticking to the tag the operator happened
	// to type at init time.
	t.Run("remote_with_tagged_runtime_image_persists_tagless", func(t *testing.T) {
		setup := env.New(t)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--runtime-image", "custom-devops:stale-tag",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--no-git",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "init/remote_with_tagged_runtime_image_persists_tagless", normalize.Apply(result.Combined))
	})

	t.Run("remote_with_runtime_resources", func(t *testing.T) {
		setup := env.New(t)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--runtime-cpu", "8",
			"--runtime-memory", "16Gi",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--no-git",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "init/remote_with_runtime_resources", normalize.Apply(result.Combined))
	})

	t.Run("remote_without_bootstrap", func(t *testing.T) {
		setup := env.New(t)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--no-git",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "init/remote_without_bootstrap", normalize.Apply(result.Combined))
	})

	t.Run("yes_flag_replaces_confirms", func(t *testing.T) {
		setup := env.New(t)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--no-git",
			"-y",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "init/yes_flag_replaces_confirms", normalize.Apply(result.Combined))
	})

	t.Run("remote_requires_environment", func(t *testing.T) {
		// --remote without an environment must fail before any side effect runs.
		setup := env.New(t)
		result := erun.Run(t, []string{"init", "--remote", "--tenant", "frs", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "init/remote_requires_environment", normalize.Apply(result.Combined))
	})

	t.Run("type_local_agent_dry_run", func(t *testing.T) {
		// --type local-agent is the explicit form of the no-flag default: it
		// creates the env without any remote-init work.
		setup := env.New(t)
		args := []string{
			"init", "team", "local",
			"--type", "local-agent",
			"--project-root", setup.Cwd,
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "init/type_local_agent_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("type_remote_agent_dry_run", func(t *testing.T) {
		// --type remote-agent is the canonical replacement for --remote and must
		// produce the same plan as remote_dry_run.
		setup := env.New(t)
		args := []string{
			"init", "team", "dev",
			"--type", "remote-agent",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--no-git",
			"--bootstrap",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "init/type_remote_agent_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("cluster_registry_dry_run", func(t *testing.T) {
		// --cluster-registry seeds the in-cluster erun-registry as the env's
		// container registry (a cluster: entry whose push/pull hosts resolve from
		// the kube-context at build/deploy time) instead of a static
		// --container-registry, so the resolved plan carries the cluster marker.
		setup := env.New(t)
		args := []string{
			"init", "team", "dev",
			"--type", "remote-agent",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--cluster-registry",
			"--no-git",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		// init's remote bootstrap deploys the runtime chart as part of setup; the
		// ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam confirms erun-devops published
		// so that deploy confirms a chart instead of refusing.
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/cluster_registry_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("cluster_registry_conflicts_with_container_registry", func(t *testing.T) {
		// --cluster-registry and --container-registry are mutually exclusive; supplying
		// both fails fast before any resolution.
		setup := env.New(t)
		args := []string{
			"init", "team", "dev",
			"--type", "remote-agent",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--cluster-registry",
			"--container-registry", "registry.example/test",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for conflicting flags, got 0: %s", result.Combined)
		}
		golden.Equal(t, "init/cluster_registry_conflicts_with_container_registry", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_mcp_auth_public_key", func(t *testing.T) {
		// --mcp-auth-public-key folds the desktop's MCP-auth key into init's single
		// runtime deploy: the runtime (team-devops) chart carries mcpAuth.* helm
		// values and the <release>-mcp-auth Secret apply, so no post-init redeploy
		// is needed to authenticate the env's MCP edge (mirrors the deploy scenario
		// of the same name).
		setup := env.New(t)
		keyPath := filepath.Join(t.TempDir(), "desktopid.pub")
		if err := os.WriteFile(keyPath, []byte("-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAtestkeytestkeytestkeytestkeytestkeytestke=\n-----END PUBLIC KEY-----\n"), 0o600); err != nil {
			t.Fatalf("write public key fixture: %v", err)
		}
		args := []string{
			"init", "team", "dev",
			"--type", "remote-agent",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--mcp-auth-public-key", keyPath,
			"--no-git",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		// init's remote bootstrap deploys the runtime chart as part of setup; the
		// ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam confirms erun-devops published
		// so that deploy confirms a chart instead of refusing.
		envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/dry_run_with_mcp_auth_public_key", normalize.Apply(result.Combined))
	})

	t.Run("type_runtime_dry_run", func(t *testing.T) {
		// --type runtime feeds downstream chart wiring (worktreeStorage=none);
		// because runtime envs live in-cluster, init still walks the remote
		// namespace setup path.
		setup := env.New(t)
		args := []string{
			"init", "team", "prod",
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
		golden.Equal(t, "init/type_runtime_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("type_conflicts_with_remote", func(t *testing.T) {
		// A --type that disagrees with --remote must error before any side
		// effect, so the user never ends up with a half-configured env.
		setup := env.New(t)
		args := []string{
			"init", "team", "local",
			"--type", "local-agent",
			"--remote",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "init/type_conflicts_with_remote", normalize.Apply(result.Combined))
	})

	t.Run("type_rejects_invalid_value", func(t *testing.T) {
		setup := env.New(t)
		args := []string{
			"init", "team", "dev",
			"--type", "invalid",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "init/type_rejects_invalid_value", normalize.Apply(result.Combined))
	})

	t.Run("rejects_hyphenated_tenant", func(t *testing.T) {
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
	})

	t.Run("prompts_for_container_registry_via_stdin", func(t *testing.T) {
		// Exercises the container-registry prompt body itself, so it omits
		// --container-registry and answers the prompt over stdin.
		setup := env.New(t)
		args := []string{
			"init", "team", "local",
			"--type", "local-agent",
			"--project-root", setup.Cwd,
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "registry.example/prompted\n",
		})
		golden.Equal(t, "init/prompts_for_container_registry_via_stdin", normalize.Apply(result.Combined))
	})

	t.Run("container_registry_empty_submit_uses_default", func(t *testing.T) {
		// A bare Enter at the container-registry prompt must submit the prompt's
		// default; this empty-input fallback only exists inside the prompt.
		setup := env.New(t)
		args := []string{
			"init", "team", "local",
			"--type", "local-agent",
			"--project-root", setup.Cwd,
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "\n",
		})
		golden.Equal(t, "init/container_registry_empty_submit_uses_default", normalize.Apply(result.Combined))
	})

	t.Run("selects_kubernetes_context_via_stdin", func(t *testing.T) {
		// Exercises the kubernetes-context select prompt. preferCurrentKubernetesContext
		// moves the current context (ctx-two) to the front, so confirming the
		// highlighted first item selects it, not the alphabetical first.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		stubKubectlContexts(t, stubs, []string{"ctx-one", "ctx-two"}, "ctx-two")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		args := []string{
			"init", "team", "local",
			"--type", "local-agent",
			"--project-root", setup.Cwd,
			"--version", "1.0.0",
			"--container-registry", "registry.example/test",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "\r",
		})
		golden.Equal(t, "init/selects_kubernetes_context_via_stdin", normalize.Apply(result.Combined))
	})

	t.Run("enters_kubernetes_context_manually_via_stdin", func(t *testing.T) {
		// The kubectl stub fails the context listing so init falls to the
		// manual-entry prompt. That listing-failure path is the only way to
		// reach the manual prompt here: choosing manual entry inside the select
		// would need a second promptui prompt, which readline's read-ahead makes
		// unreachable over piped stdin.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
			Stderr:   "error: unable to read kubeconfig",
			ExitCode: 1,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		args := []string{
			"init", "team", "local",
			"--type", "local-agent",
			"--project-root", setup.Cwd,
			"--version", "1.0.0",
			"--container-registry", "registry.example/test",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "manual-ctx\n",
		})
		golden.Equal(t, "init/enters_kubernetes_context_manually_via_stdin", normalize.Apply(result.Combined))
	})

	t.Run("prompts_default_tenant_confirm_via_stdin", func(t *testing.T) {
		// Bare Enter at the default-tenant confirm exercises the default-yes
		// branch. Only one promptui prompt survives per subprocess (readline
		// read-ahead), so the environment confirm is bypassed via its flag.
		setup := env.New(t)
		args := []string{
			"init", "team", "local",
			"--type", "local-agent",
			"--project-root", setup.Cwd,
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "\n",
		})
		golden.Equal(t, "init/prompts_default_tenant_confirm_via_stdin", normalize.Apply(result.Combined))
	})

	t.Run("prompts_environment_confirm_via_stdin", func(t *testing.T) {
		// Typing "y" at the environment confirm exercises the typed-yes branch;
		// the tenant confirm is bypassed via --set-default-tenant to keep the
		// run at one prompt.
		setup := env.New(t)
		args := []string{
			"init", "team", "local",
			"--type", "local-agent",
			"--project-root", setup.Cwd,
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--set-default-tenant=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "y\n",
		})
		golden.Equal(t, "init/prompts_environment_confirm_via_stdin", normalize.Apply(result.Combined))
	})

	t.Run("environment_confirm_declined_via_stdin", func(t *testing.T) {
		// Declining the environment confirm must abort before the env config
		// is created.
		setup := env.New(t)
		args := []string{
			"init", "team", "local",
			"--type", "local-agent",
			"--project-root", setup.Cwd,
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--set-default-tenant=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "n\n",
		})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "init/environment_confirm_declined_via_stdin", normalize.Apply(result.Combined))
	})

	t.Run("remote_real_run_reuses_host_ssh_config", func(t *testing.T) {
		// Must be a real run: reusing an existing ~/.ssh/config and skipping the
		// per-key credential flow depends on the pod's live answers, which
		// dry-run would hardcode. The repository URL has no CLI flag, so it comes
		// over stdin, and -y both auto-approves the host-config reuse and keeps
		// the run to the single prompt readline read-ahead allows.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{SSHConfigExists: true})
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"-y",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "ssh://git.example.com/team/repo.git\n",
		})
		golden.Equal(t, "init/remote_real_run_reuses_host_ssh_config", normalize.Apply(result.Combined))
	})

	t.Run("remote_real_run_host_config_rejected_falls_back_to_key_import", func(t *testing.T) {
		// When the pod's existing ~/.ssh/config fails verification, init must
		// fall back to the per-key credential flow: print the pod's public key,
		// wait for the user to import it, poll access, then clone. The repository
		// URL has no CLI flag, so it comes over stdin.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{
			SSHConfigExists:          true,
			HostConfigVerifyExitCode: 1,
		})
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"-y",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "ssh://git.example.com/team/repo.git\n",
		})
		golden.Equal(t, "init/remote_real_run_host_config_rejected_falls_back_to_key_import", normalize.Apply(result.Combined))
	})

	t.Run("remote_real_run_existing_repo_pulls", func(t *testing.T) {
		// When the pod already has a clone, init must pull instead of prompting
		// for a URL or touching SSH credentials. No stdin: the absence of any
		// prompt is part of the contract this scenario locks.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "init/remote_real_run_existing_repo_pulls", normalize.Apply(result.Combined))
	})

	t.Run("remote_real_run_ghcr_registry_missing_credential_refuses", func(t *testing.T) {
		// #1201: init deployed a pod configured to build+push to ghcr.io, but the
		// pod itself never authenticated (no docker config, no gh session, no
		// GH_TOKEN). Before this fix, init reported success and the failure only
		// surfaced 7 minutes into the first `erun release`, at the push. Init must
		// now refuse right after the deploy, before ever touching git/SSH setup --
		// no repository state probe, no SSH key output.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RegistryCredentialConfigured: false})
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "ghcr.io/sophium",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when the pod has no ghcr.io credential, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "init/remote_real_run_ghcr_registry_missing_credential_refuses", normalize.Apply(result.Combined))
	})

	t.Run("remote_real_run_ghcr_registry_credential_configured_succeeds", func(t *testing.T) {
		// The other half of #1201: a pod that already has a ghcr.io credential
		// (the operator authenticated it, or it inherited one from a prior push)
		// must not be blocked by the new check -- init completes normally.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RegistryCredentialConfigured: true})
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "ghcr.io/sophium",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			// No-git keeps this scenario about the credential check alone; git
			// checkout setup is covered by the other remote real-run scenarios.
			"--no-git",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/remote_real_run_ghcr_registry_credential_configured_succeeds", normalize.Apply(result.Combined))
	})

	t.Run("remote_dry_run_traces_registry_credential_check", func(t *testing.T) {
		// Dry-run must show the new check as a trace line like every other
		// action init takes, and must not fail the plan preview over pod state a
		// preview cannot know (the pod does not exist yet in dry-run).
		setup := env.New(t)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "ghcr.io/sophium",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--no-git",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/remote_dry_run_traces_registry_credential_check", normalize.Apply(result.Combined))
	})

	t.Run("remote_dry_run_provisions_registry_credential_from_host", func(t *testing.T) {
		// When the host running `erun init` already has a ghcr.io credential
		// (docker config, gh session, or GH_TOKEN), init mints the
		// dockerconfigjson Secret the runtime chart mounts, rather than leaving
		// the pod to hand-carry one. DOCKER_CONFIG points the host resolver at
		// this isolated dir instead of the developer's real ~/.docker, the same
		// seam TestVersion's docker-config scenarios use.
		setup := env.New(t)
		dockerCfgDir := filepath.Join(setup.Cwd, "docker-inline")
		if err := os.MkdirAll(dockerCfgDir, 0o755); err != nil {
			t.Fatalf("mkdir docker config dir: %v", err)
		}
		encoded := base64.StdEncoding.EncodeToString([]byte("sophium:s3cret-token"))
		dockerCfg := fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, encoded)
		if err := os.WriteFile(filepath.Join(dockerCfgDir, "config.json"), []byte(dockerCfg), 0o644); err != nil {
			t.Fatalf("write docker config: %v", err)
		}

		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "ghcr.io/sophium",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--no-git",
			"--dry-run",
		}
		envVars := append(setup.Env(), "DOCKER_CONFIG="+dockerCfgDir)
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, "s3cret-token") || strings.Contains(result.Combined, encoded) {
			t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
		}
		golden.Equal(t, "init/remote_dry_run_provisions_registry_credential_from_host", normalize.Apply(result.Combined))
	})

	t.Run("remote_real_run_codecommit_key_import_retry", func(t *testing.T) {
		// CodeCommit real-run: init prints the pod's CodeCommit key with IAM
		// upload instructions, then polls access — the stub fails the first
		// ls-remote to drive the retry loop once before the clone. The key ID
		// comes from --codecommit-ssh-key-id because the interactive key-ID
		// prompt would be a second promptui prompt, unreachable over piped stdin.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{LsRemoteFailures: 1})
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--codecommit-ssh-key-id", "APKAEXAMPLEKEYID",
			"-y",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "ssh://git-codecommit.eu-west-1.amazonaws.com/v1/repos/team\n",
		})
		golden.Equal(t, "init/remote_real_run_codecommit_key_import_retry", normalize.Apply(result.Combined))
	})

	t.Run("remote_real_run_codecommit_key_id_in_url", func(t *testing.T) {
		// The CodeCommit SSH key ID can ride in the URL's user part (the form the
		// AWS console hands out): init must extract it, strip it from the clone
		// URL, and accept it without the --codecommit-ssh-key-id flag or a key
		// prompt. Access succeeds on the first poll, so no retry lines appear.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{})
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"-y",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "ssh://APKAEXAMPLEKEYID@git-codecommit.eu-west-1.amazonaws.com/v1/repos/team\n",
		})
		golden.Equal(t, "init/remote_real_run_codecommit_key_id_in_url", normalize.Apply(result.Combined))
	})

	t.Run("local_real_run_persists_project_registry", func(t *testing.T) {
		// Real-run local-agent init: a --container-registry that differs from the
		// project's base must persist as a per-environment override, and the k8s
		// deploy plan must round-trip in both its scalar (single step) and
		// sequence (parallel group) shapes — round-trips dry-run init never
		// reaches. A pre-existing ~/.claude/settings.json with custom permissions
		// but no bypass mode drives the merge: the custom allow-list must survive
		// while bypass mode is stamped in.
		setup := env.New(t)
		claudeDir := filepath.Join(setup.Home, ".claude")
		if err := os.MkdirAll(claudeDir, 0o700); err != nil {
			t.Fatalf("mkdir ~/.claude: %v", err)
		}
		mustWrite(t, filepath.Join(claudeDir, "settings.json"),
			`{"permissions":{"allow":["Bash(ls:*)"]}}`)
		fixture.SeedProjectK8sConfig(t, setup,
			"containerregistry: registry.example/base\n"+
				"environments:\n"+
				"  local:\n"+
				"    k8s:\n"+
				"      deployments:\n"+
				"        - app\n"+
				"        - [api, worker]\n",
		)
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		args := []string{
			"init", "team", "local",
			"--type", "local-agent",
			"--project-root", setup.Cwd,
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/custom",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/local_real_run_persists_project_registry", normalize.Apply(result.Combined))
		// The rewritten project config is a side effect outside the captured
		// streams, so assert on the file directly.
		raw, err := os.ReadFile(filepath.Join(setup.Cwd, ".erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read project config: %v", err)
		}
		body := string(raw)
		for _, want := range []string{
			"registry: registry.example/base",
			"registry.example/custom",
			"- app",
			"api",
			"worker",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("expected project config to contain %q, got:\n%s", want, body)
			}
		}
		claudeRaw, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
		if err != nil {
			t.Fatalf("read ~/.claude/settings.json: %v", err)
		}
		// JSON re-indentation makes exact-substring asserts brittle; strip
		// all whitespace before matching the merged keys.
		flattened := strings.Join(strings.Fields(string(claudeRaw)), "")
		for _, want := range []string{
			`"Bash(ls:*)"`,
			`"defaultMode":"bypassPermissions"`,
			`"skipDangerousModePermissionPrompt":true`,
		} {
			if !strings.Contains(flattened, want) {
				t.Errorf("expected claude settings to contain %q, got:\n%s", want, claudeRaw)
			}
		}
	})

	t.Run("local_real_run_drops_redundant_registry_override", func(t *testing.T) {
		// Re-initializing an env whose per-environment registry override equals
		// the project-wide base must drop the now-redundant override. Production
		// deletes the whole map entry, so it would also wipe a co-located
		// k8s/docker block if one were present. A pre-seeded fully-configured
		// ~/.claude/settings.json must take EnsureClaudeSettings' already-configured
		// early return and stay byte-identical.
		setup := env.New(t)
		claudeDir := filepath.Join(setup.Home, ".claude")
		if err := os.MkdirAll(claudeDir, 0o700); err != nil {
			t.Fatalf("mkdir ~/.claude: %v", err)
		}
		const configuredClaudeSettings = `{"permissions":{"defaultMode":"bypassPermissions"},"skipDangerousModePermissionPrompt":true}`
		mustWrite(t, filepath.Join(claudeDir, "settings.json"), configuredClaudeSettings)
		fixture.SeedProjectK8sConfig(t, setup,
			"containerregistry: registry.example/base\n"+
				"environments:\n"+
				"  local:\n"+
				"    containerregistry: registry.example/custom\n",
		)
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		args := []string{
			"init", "team", "local",
			"--type", "local-agent",
			"--project-root", setup.Cwd,
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/base",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		raw, err := os.ReadFile(filepath.Join(setup.Cwd, ".erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read project config: %v", err)
		}
		body := string(raw)
		if strings.Contains(body, "registry.example/custom") {
			t.Errorf("expected the per-environment registry override to be removed, got:\n%s", body)
		}
		if !strings.Contains(body, "registry: registry.example/base") {
			t.Errorf("expected the base registry to survive, got:\n%s", body)
		}
		claudeRaw, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
		if err != nil {
			t.Fatalf("read ~/.claude/settings.json: %v", err)
		}
		if string(claudeRaw) != configuredClaudeSettings {
			t.Errorf("expected already-configured claude settings to be left untouched, got:\n%s", claudeRaw)
		}
	})

	t.Run("real_run_via_stubs", func(t *testing.T) {
		// A real run (through stubs) covers the kubectl namespace check/create
		// and helm-runner code that dry-run traces but never executes.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker", "git")...)
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--no-git",
			"--bootstrap",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "init/real_run_via_stubs", normalize.Apply(result.Combined))
	})

	t.Run("reinit_real_run_updates_kubernetes_context", func(t *testing.T) {
		// Re-init with a changed --kubernetes-context: the new context is
		// persisted and its namespace ensured, the cloud-provider alias stays
		// empty (no matching provider), and the unchanged registry is left
		// alone. The kubectl stub reports the namespace NotFound so the create
		// branch runs. The persisted env config must carry the new context.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
			`case "$*" in`,
			`  *"get namespace"*)`,
			`    printf '%s\n' 'Error from server (NotFound): namespaces "team-dev" not found' >&2`,
			`    exit 1 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		args := []string{
			"init", "team", "dev",
			"--kubernetes-context", "new-context",
			"--container-registry", "registry.example/test",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/reinit_real_run_updates_kubernetes_context", normalize.Apply(result.Combined))
		assertEnvConfigContains(t, setup, "team", "dev", "kubernetescontext: new-context")
	})

	t.Run("reinit_remote_real_run_updates_runtime_settings", func(t *testing.T) {
		// Re-init of an existing remote env updates it in place: repopath
		// converges on the in-pod worktree convention path, the runtime version
		// moves to the new --version, and the pod limits are recorded — without
		// recreating the env. The kubectl stub reports the clone already present,
		// so the flow pulls instead of prompting for a URL.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
		envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:2.0.0")
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "2.0.0",
			"--runtime-cpu", "8",
			"--runtime-memory", "16Gi",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/reinit_remote_real_run_updates_runtime_settings", normalize.Apply(result.Combined))
		assertEnvConfigContains(t, setup, "team", "dev", "runtimeversion: 2.0.0", "cpu: \"8\"", "memory: 16Gi")
	})

	t.Run("reinit_remote_real_run_redirects_the_runtime_registry", func(t *testing.T) {
		// The way out of the bootstrap deadlock: runtimeregistry is the only field
		// that redirects chart resolution, and every other writer of it is a side
		// effect of a deploy that already succeeded. --runtime-registry writes it
		// before this run's own deploy resolves a chart, so the recovery is one
		// command rather than a hand-edit of erun's config store. The probe seam
		// publishes the platform chart only in the redirected registry, so the
		// resolved chart reference is the proof the redirect took effect.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
		envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=ghcr.io/sophium/erun-devops:1.0.0")
		args := []string{
			"init", "team", "dev",
			"--remote",
			"--version", "1.0.0",
			"--runtime-registry", "ghcr.io/sophium",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/reinit_remote_real_run_redirects_the_runtime_registry", normalize.Apply(result.Combined))
		assertEnvConfigContains(t, setup, "team", "dev", "runtimeregistry: ghcr.io/sophium")
	})

	t.Run("reinit_records_an_image_pull_secret_without_restating_the_type", func(t *testing.T) {
		// A supplied setting lands on the env for having been supplied. The
		// invocation names no --type, so it resolves to the local-agent default,
		// and the setting still reaches an env stored as remote-agent — accepting
		// the flag and dropping it is the failure this pins. The env keeps its
		// type: a flag nobody passed must not retype anything.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		args := []string{
			"init", "team", "dev",
			"--image-pull-secret", "ecr-pull",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/reinit_records_an_image_pull_secret_without_restating_the_type", normalize.Apply(result.Combined))
		assertEnvConfigContains(t, setup, "team", "dev", "imagepullsecrets:", "- ecr-pull", "type: remote-agent", "runtimeversion: 1.0.0")
	})

	t.Run("reinit_sets_saved_deploy_components", func(t *testing.T) {
		// erun init gained --components so a saved deploy default can be
		// set without hand-editing the env's config.yaml.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		args := []string{
			"init", "team", "dev",
			"--components", "erun-backend-api,erun-backend-db",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/reinit_sets_saved_deploy_components", normalize.Apply(result.Combined))
		assertEnvConfigContains(t, setup, "team", "dev", "components:", "- erun-backend-api", "- erun-backend-db")
	})

	t.Run("reinit_clears_saved_deploy_components_returns_env_to_plan", func(t *testing.T) {
		// The way back: an env stuck on a saved selection that shadows the
		// repo's k8s.deployments plan had no command to return to the plan short
		// of hand-editing erun's config store. `--components ''` is that command:
		// it clears the saved set outright rather than being read as "nothing
		// passed, leave it alone".
		setup := env.New(t)
		fixture.SeedTenantEnvWithDeployComponents(t, setup, "team", "dev", []string{"erun-backend-postgres"})
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		args := []string{
			"init", "team", "dev",
			"--components", "",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/reinit_clears_saved_deploy_components_returns_env_to_plan", normalize.Apply(result.Combined))
		assertEnvConfigLacks(t, setup, "team", "dev", "erun-backend-postgres", "components:")
	})

	t.Run("reinit_runtime_env_records_a_registry_and_keeps_its_type", func(t *testing.T) {
		// The documented escape from a chart-resolution deadlock, run the way an
		// operator reaches for it: --runtime-registry alone. It has to land on an
		// existing env whatever type that env is, and the runtime env has to still
		// be a runtime env afterwards — the default an omitted --type resolves to
		// is local-agent, and acting on it would silently retype the env.
		setup := env.New(t)
		fixture.SeedRuntimeTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		args := []string{
			"init", "team", "dev",
			"--runtime-registry", "ghcr.io/sophium",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/reinit_runtime_env_records_a_registry_and_keeps_its_type", normalize.Apply(result.Combined))
		assertEnvConfigContains(t, setup, "team", "dev", "runtimeregistry: ghcr.io/sophium", "type: runtime")
	})

	t.Run("reinit_retypes_a_runtime_env_to_remote_agent", func(t *testing.T) {
		// Both env types keep their worktree off the laptop, so the old reconcile
		// could never move between them and a runtime env could never become
		// orchestratable. Named explicitly, the type moves, and the run does the
		// remote-agent work — the runtime deploy and the checkout — a fresh
		// --type=remote-agent init would. The kubectl stub reports the clone
		// already present so the flow pulls instead of prompting for a URL.
		setup := env.New(t)
		fixture.SeedRuntimeTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		stubRemoteInitKubectl(t, stubs, remoteInitKubectlStub{RepoExists: true})
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "git")...)
		envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
		args := []string{
			"init", "team", "dev",
			"--type", "remote-agent",
			"--kubernetes-context", "test-context",
			"--container-registry", "registry.example/test",
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/reinit_retypes_a_runtime_env_to_remote_agent", normalize.Apply(result.Combined))
		assertEnvConfigContains(t, setup, "team", "dev", "type: remote-agent", "runtimeversion: 1.0.0")
	})

	t.Run("reinit_retypes_a_remote_env_to_local_agent", func(t *testing.T) {
		// The other direction, which needs more than the type field: a local-agent
		// worktree is hostPath-mounted, and the path a remote env carries names an
		// in-pod directory. The retype adopts the host project root supplied with
		// it, so the env it writes is one that can actually mount.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		args := []string{
			"init", "team", "dev",
			"--type", "local-agent",
			"--project-root", setup.Cwd,
			"--set-default-tenant=true",
			"--confirm-environment=true",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/reinit_retypes_a_remote_env_to_local_agent", normalize.Apply(result.Combined))
		assertEnvConfigContains(t, setup, "team", "dev", "type: local-agent", "localrepopath: "+setup.Cwd)
	})

	t.Run("reinit_refuses_a_local_agent_retype_with_no_host_repo_path", func(t *testing.T) {
		// The refusal that keeps the flag honest: the run is not in a git repo and
		// names no --project-root, so there is no host path to mount. Writing the
		// type anyway would move the failure to a later deploy, and reporting
		// success would drop the flag. It refuses, naming what to supply.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		args := []string{
			"init", "team", "dev",
			"--type", "local-agent",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "init/reinit_refuses_a_local_agent_retype_with_no_host_repo_path", normalize.Apply(result.Combined))
		assertEnvConfigContains(t, setup, "team", "dev", "type: remote-agent")
	})

	t.Run("reinit_dry_run_traces_what_it_keeps_and_what_it_changes", func(t *testing.T) {
		// The audit line for a re-init: every setting the run decided about shows
		// up, whether it was supplied or deliberately left as found. The pod
		// resources are the ones that matter most — they used to be reset to the
		// defaults by any re-init that reached the reconcile at all, so an operator
		// adding one flag lost limits they never mentioned.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		appendEnvConfig(t, setup, "team", "dev", "runtimepod:\n  cpu: \"8\"\n  memory: 16Gi\n")
		args := []string{
			"init", "team", "dev",
			"--runtime-image", "registry.example/team-devops",
			"--image-pull-secret", "ecr-pull",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/reinit_dry_run_traces_what_it_keeps_and_what_it_changes", normalize.Apply(result.Combined))
		// A dry run traces the write and performs none of it: the limits it said it
		// would keep are untouched, and what it said it would set never landed.
		assertEnvConfigContains(t, setup, "team", "dev", "cpu: \"8\"", "memory: 16Gi")
		assertEnvConfigLacks(t, setup, "team", "dev", "imagepullsecrets", "runtimeimage")
	})
}
