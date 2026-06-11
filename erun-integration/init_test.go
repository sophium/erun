package integration

import (
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

// stubKubectlContexts writes a kubectl stub whose `config get-contexts
// -o=name` arm prints the given context names (one per line) and whose
// `config current-context` arm prints current. Init's interactive
// kubernetes-context selection uses both calls as decision input, so the
// stub keeps the rendered select list deterministic across machines.
// Package-private test helper; lives here because init owns the
// kubernetes-context prompt flow.
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

// seedTenantWithoutDefault writes a tenant + env config tree like
// fixture.SeedTenantEnv but deliberately leaves the root erun config.yaml
// (default tenant) unwritten and roots the tenant's project at
// HOME/git/<tenant> instead of the scenario cwd. With no default tenant and
// a cwd outside every tenant's project root, the bare root command's
// bootstrap falls through to the interactive tenant selection prompt.
// Package-private test helper; lives here because init owns that prompt.
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

// remoteInitKubectlStub configures the kubectl stub for non-dry-run remote
// init scenarios. Remote init drives every in-pod step through
// `kubectl exec ... /bin/sh -lc <script>`, so the stub branches on
// distinctive substrings of the script argv:
//   - the repository-state script (contains __ERUN_REMOTE_PUBLIC_KEY__)
//     answers with a parseable state block built from RepoExists and
//     SSHConfigExists;
//   - the existing-host-config verification script (contains `test -s`)
//     exits with HostConfigVerifyExitCode;
//   - `git ls-remote` verification calls fail LsRemoteFailures times before
//     succeeding, so scenarios can drive the SSH-key-import retry loop;
//   - everything else (helm-side kubectl calls, clone, pull, marker write)
//     exits 0 silently.
type remoteInitKubectlStub struct {
	RepoExists               bool
	SSHConfigExists          bool
	HostConfigVerifyExitCode int
	LsRemoteFailures         int
}

// stubRemoteInitKubectl writes the kubectl stub described by spec into dir.
// The stub is decision input for the real-run remote init flow: it stands in
// for the remote pod's shell so the scenario stays deterministic without a
// cluster.
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
	if spec.HostConfigVerifyExitCode != 0 {
		lines = append(lines,
			`  *'test -s'*)`,
			`    printf 'Permission denied (publickey).\n' >&2`,
			`    exit `+strconv.Itoa(spec.HostConfigVerifyExitCode)+` ;;`,
		)
	}
	if spec.LsRemoteFailures > 0 {
		counter := filepath.Join(dir, "ls-remote-calls")
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
		// Exercises init.go --remote validation: passing --remote without
		// an environment must fail with the standard error message
		// before any side effect runs.
		setup := env.New(t)
		result := erun.Run(t, []string{"init", "--remote", "--tenant", "frs", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "init/remote_requires_environment", normalize.Apply(result.Combined))
	})

	t.Run("type_local_agent_dry_run", func(t *testing.T) {
		// --type local-agent is the explicit form of today's no-flag default.
		// Validates that an explicit type flag is accepted and the dry-run
		// trace shows the env being created without --remote-style remote
		// init work.
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
		// --type remote-agent is the canonical replacement for --remote.
		// The expected trace mirrors remote_dry_run but is driven by the
		// new flag rather than the legacy bool.
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

	t.Run("type_runtime_dry_run", func(t *testing.T) {
		// --type runtime persists Type=runtime so downstream chart wiring
		// can request worktreeStorage=none. Init still walks the remote
		// namespace setup path because runtime envs live in-cluster.
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
		// --type and --remote that disagree must error before any side
		// effect, so a user catching themselves mid-migration sees the
		// conflict immediately instead of getting a half-configured env.
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

	t.Run("prompts_for_container_registry_via_stdin", func(t *testing.T) {
		// Executes containerRegistryPrompt (init.go): --container-registry is
		// omitted so the bootstrap falls through to the interactive promptui
		// Prompt, answered with a typed registry over piped stdin. Scripted
		// stdin is the honest tool here — the scenario exists to execute the
		// prompt body itself, not to test behavior around it, so the
		// "prefer flags that bypass prompts" guidance does not apply.
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
		// Executes containerRegistryPrompt's empty-input fallback: stdin
		// sends Ctrl+U (clear the prefilled default from the readline
		// buffer) followed by Enter, so the prompt returns an empty string
		// and init must fall back to the default container registry.
		// Scripted stdin is the honest tool here: the fallback branch only
		// exists inside the interactive prompt.
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
			Stdin: "\x15\n",
		})
		golden.Equal(t, "init/container_registry_empty_submit_uses_default", normalize.Apply(result.Combined))
	})

	t.Run("selects_kubernetes_context_via_stdin", func(t *testing.T) {
		// Executes kubernetesContextPrompt + selectKubernetesContextPrompt
		// (init.go): --kubernetes-context is omitted so init lists contexts
		// via `kubectl config get-contexts` / `current-context` (stubbed for
		// deterministic decision input) and renders a promptui Select.
		// Stdin "\r" confirms the highlighted first item, which is the
		// current context (ctx-two) because preferCurrentKubernetesContext
		// moves it to the front. Scripted stdin is the honest tool here:
		// the scenario exists to execute the select-prompt body itself.
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
		// Executes the manual-entry fallback of kubernetesContextPrompt plus
		// manualKubernetesContextPrompt (init.go): the kubectl stub fails the
		// context listing, so init skips the select list and falls straight
		// to the manual promptui Prompt, answered over piped stdin. Scripted
		// stdin is the honest tool here: the scenario exists to execute the
		// manual-prompt body. (Choosing "Enter Kubernetes context manually"
		// inside the select would require a second sequential promptui
		// prompt; readline's read-ahead swallows the rest of a piped stdin
		// when the first prompt closes, so only the listing-failure fallback
		// is reachable from this harness.)
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
		// Executes confirmPrompt (init.go) for the default-tenant confirm:
		// --set-default-tenant is omitted so init asks "Initialize tenant
		// ... as the default tenant?", answered with bare Enter to exercise
		// the default-yes branch. One prompt per subprocess: readline's
		// read-ahead drops any stdin left over when a prompt closes, so the
		// environment confirm is bypassed via its flag. Scripted stdin is
		// the honest tool here: the scenario exists to execute the
		// confirm-prompt body.
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
		// Executes confirmPrompt (init.go) for the environment confirm:
		// --confirm-environment is omitted so init asks "Initialize default
		// environment ...?", answered "y" to exercise the typed-yes branch.
		// The tenant confirm is bypassed via --set-default-tenant so the
		// run needs exactly one prompt (see readline read-ahead note above).
		// Scripted stdin is the honest tool here: the scenario exists to
		// execute the confirm-prompt body.
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
		// Executes confirmPrompt's "n" branch: the environment confirm is
		// declined, so init must stop with "environment initialization
		// cancelled by user" before creating the env config. Scripted stdin
		// is the honest tool here: the cancel branch only exists inside the
		// interactive prompt.
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
		// Real-run remote init (no --dry-run, no --no-git) so the in-pod git
		// plumbing in erun-common/init_remote.go actually executes against
		// the kubectl stub: remoteRepositoryState parses the pod's state
		// block (repo missing, ~/.ssh/config exists), the repository URL is
		// prompted over stdin (remoteRepositoryURLPrompt — there is no CLI
		// flag for it, so scripted stdin is the only honest way to supply
		// it), and resolveExistingRemoteHostConfig verifies access with the
		// existing host config and auto-approves its reuse via -y, skipping
		// the per-key credential flow before the clone. These branches
		// depend on the pod's answers, which dry-run hardcodes, so this must
		// be a real run. -y also keeps the run at exactly one prompt:
		// readline's read-ahead drops leftover piped stdin when a prompt
		// closes, so the host-config confirm could never be answered as a
		// second prompt.
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
		// Real-run remote init where the pod has an ~/.ssh/config but it
		// does not grant access: resolveExistingRemoteHostConfig's
		// verification (the `test -s` script) fails, so init must fall back
		// to remoteRepositorySpecWithCredentials — print the pod's
		// ed25519 public key, ask the user to import it
		// (waitForRemoteKeyImport), poll access via WaitForGitAccess, and
		// clone with the per-key SSH command. The repository URL is prompted
		// over stdin (no CLI flag exists for it).
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
		// Real-run remote init where the pod already has a clone:
		// remoteRepositoryState reports repo_exists, so init must pull
		// (pullRemoteRepository) instead of prompting for a URL or touching
		// SSH credentials. No stdin: the absence of any prompt is part of
		// the contract this scenario locks.
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

	t.Run("remote_real_run_codecommit_key_import_retry", func(t *testing.T) {
		// Real-run remote init against a CodeCommit URL typed over stdin
		// (remoteRepositoryURLPrompt): parseRemoteRepositorySpec recognizes
		// the git-codecommit host, resolveRemoteRepositoryCredentials takes
		// the SSH key ID from --codecommit-ssh-key-id (the interactive key
		// ID prompt would be a second sequential promptui prompt, which a
		// piped stdin cannot reach — see the readline read-ahead note), and
		// waitForRemoteKeyImport prints the pod's CodeCommit RSA key with
		// the IAM upload instructions, then polls access: the stub fails the
		// first `git ls-remote` so the WaitForGitAccess retry loop runs once
		// before access is confirmed and the clone proceeds with the
		// CodeCommit host config.
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
		// Real-run remote init where the CodeCommit SSH key ID rides inside
		// the typed URL's user part (the form the AWS console hands out:
		// ssh://<key-id>@git-codecommit...). parseRemoteRepositorySpec must
		// extract the key ID and strip it from the clone URL, and
		// resolveRemoteRepositoryCredentials must accept that key without
		// the --codecommit-ssh-key-id flag or a key prompt. Access succeeds
		// on the first poll, so no retry lines appear.
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
		// Real-run local-agent init against a project whose .erun/config.yaml
		// already declares a base container registry and a k8s deploy plan.
		// --container-registry differs from the base, so the bootstrap's
		// saveProjectContainerRegistry path must load the project config
		// (LoadProjectConfig), set the per-environment override
		// (SetContainerRegistryForEnvironment add-entry branch), and persist
		// it (SaveProjectConfig + writeFileAtomic). Saving round-trips the
		// k8s deployments through ProjectK8sDeploymentStep.MarshalYAML —
		// the scalar arm for the single-component step and the sequence arm
		// for the parallel group — none of which dry-run init can reach.
		// kubectl is stubbed so the namespace ensure succeeds offline.
		// A pre-existing ~/.claude/settings.json with custom permissions
		// (but no bypass mode) drives EnsureClaudeSettings' merge path:
		// the custom allow-list must survive while defaultMode and the
		// dangerous-prompt skip are stamped in.
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
		// streams: the env override must be persisted and the k8s deploy
		// plan must round-trip in its natural shape (scalar step + group).
		raw, err := os.ReadFile(filepath.Join(setup.Cwd, ".erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read project config: %v", err)
		}
		body := string(raw)
		for _, want := range []string{
			"containerregistry: registry.example/base",
			"registry.example/custom",
			"- app",
			"api",
			"worker",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("expected project config to contain %q, got:\n%s", want, body)
			}
		}
		// EnsureClaudeSettings merged the bypass mode into the pre-existing
		// settings without dropping the custom allow-list.
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
		// Exercises SetContainerRegistryForEnvironment's delete-entry branch:
		// re-initializing an env whose project config carries a
		// per-environment registry override with a registry equal to the
		// project-wide base must remove the now-redundant override entry.
		// The seeded env entry holds only the override — production deletes
		// the whole map entry, which would also wipe a k8s/docker block if
		// one were present (see the sibling scenario's k8s round-trip).
		// ~/.claude/settings.json is pre-seeded in its fully-configured
		// bypass shape so EnsureClaudeSettings takes the already-configured
		// early return and leaves the file byte-identical.
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
		if !strings.Contains(body, "containerregistry: registry.example/base") {
			t.Errorf("expected the base registry to survive, got:\n%s", body)
		}
		// EnsureClaudeSettings' already-configured early return: the seeded
		// bypass settings must come through byte-identical (no rewrite).
		claudeRaw, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
		if err != nil {
			t.Fatalf("read ~/.claude/settings.json: %v", err)
		}
		if string(claudeRaw) != configuredClaudeSettings {
			t.Errorf("expected already-configured claude settings to be left untouched, got:\n%s", claudeRaw)
		}
	})

	t.Run("real_run_via_stubs", func(t *testing.T) {
		// Run init for real (without --dry-run) but route every external
		// call through stubs. Covers the kubectl namespace check/create
		// branches in kubernetes_namespace.go and the helm-runner code in
		// deploy.go that the dry-run path traces but never executes.
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
}
